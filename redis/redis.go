// Package redis contains postgresql session provider based on go-redis client.
// Requirements:
//
//	redis client https://github.com/redis/go-redis
package redis

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/dronm/session"
	"github.com/redis/go-redis/v9"
)

var EKeyNotFound = errors.New("key not found")

const PROVIDER = "redis"

// Session key ID length.
const SESS_ID_LEN = 36

const LOG_PREF = "redis provider:"

// pder holds pointer to Provider struct.
var pder = &Provider{}

// SessionStore contains session id.
type SessionStore struct {
	sid string
}

// Set sets redis value, updates access time.
func (st *SessionStore) Set(key string, value interface{}) error {
	if err := pder.setValue(st.sid, key, value); err != nil {
		return err
	}
	return nil
}

// Set sets redis value, updates access time.
func (st *SessionStore) Put(key string, value interface{}) error {
	if err := pder.setValue(st.sid, key, value); err != nil {
		return err
	}
	return st.Flush()
}

func (st *SessionStore) Flush() error {
	pder.sessionAccessed(st.sid)
	return nil
}

// Get retrieves session value by its key.
// If there is no key error is returned.
func (st *SessionStore) Get(key string, val interface{}) error {
	if err := pder.getValue(st.sid, key, val); err != nil {
		return err
	}
	return nil
}

// GetBool returns bool value by key.
func (st *SessionStore) GetBool(key string) bool {
	var v bool
	_ = pder.getValue(st.sid, key, &v)
	return v
}

// GetString returns string value by key.
func (st *SessionStore) GetString(key string) string {
	var v string
	_ = pder.getValue(st.sid, key, &v)
	return v
}

// GetInt returns int value by key.
func (st *SessionStore) GetInt(key string) int64 {
	var v int64
	pder.getValue(st.sid, key, &v)
	return v
}

// GetFloat returns float value by key.
func (st *SessionStore) GetFloat(key string) float64 {
	var v float64
	_ = pder.getValue(st.sid, key, &v)
	return v
}

// GetDate returns time.Time value by key.
func (st *SessionStore) GetDate(key string) time.Time {
	var v time.Time
	_ = pder.getValue(st.sid, key, &v)
	return v
}

// Delete deletes session value from memmory by key.
func (st *SessionStore) Delete(key string) error {
	pder.client.Del(context.Background(), pder.getPrefixedKey(st.sid, key))
	pder.sessionAccessed(st.sid)

	return nil
}

// SessionID returns session unique ID.
func (st *SessionStore) SessionID() string {
	return st.sid
}

// TimeCreated returns timeCreated property.
func (st *SessionStore) TimeCreated() time.Time {
	return st.GetDate("time_created")
}

// TimeCreated returns timeAccessed property.
func (st *SessionStore) TimeAccessed() time.Time {
	return st.GetDate("time_accessed")
}

// Provider structure holds provider information.
type Provider struct {
	client      *redis.Client
	namespace   string //key prefix
	maxLifeTime int64
	maxIdleTime int64
}

// SessionInit initializes session with given ID.
func (pder *Provider) SessionInit(sid string) (session.Session, error) {
	if pder.client == nil {
		return nil, errors.New("Provider not initialized")
	}

	if len(sid) > SESS_ID_LEN {
		return nil, errors.New("Session key length exceeded max value")
	}

	return &SessionStore{sid: sid}, nil
}

func (pder *Provider) SessionRead(sid string) (session.Session, error) {
	return &SessionStore{sid: sid}, nil
}

// SessionClose is a stub
func (pder *Provider) SessionClose(sid string) error {
	return nil
}

// SessionDestroy destoys session by its ID.
func (pder *Provider) SessionDestroy(sid string) error {
	return pder.removeSession(sid)
}

// SessionGC removes unused sessions.
// Handle max idle time only.
// Max life time is controled by REDIS.
func (pder *Provider) SessionGC(l io.Writer, logLev session.LogLevel) {
	//life time is controled by radis
	if pder.maxIdleTime == 0 {
		return
	}
	ctx := context.Background()
	iter := pder.client.Scan(ctx, 0, pder.namespace+":*:time_accessed", 0).Iterator()
	tm := time.Now().Unix()
	for iter.Next(ctx) {
		var t time.Time
		key := iter.Val()
		if err := pder.getValueForKey(key, &t); err != nil {
			if l != nil {
				session.WriteToLog(l, fmt.Sprintf(LOG_PREF+"pder.getValueForKey() failed for key %s: %v", key, err), session.LOG_LEVEL_ERROR)
			}
			continue
		}
		if t.Unix()+pder.maxIdleTime <= tm {
			sess_keys := strings.Replace(key, "time_accessed", "*", 1)
			if l != nil && logLev >= session.LOG_LEVEL_DEBUG {
				session.WriteToLog(l, LOG_PREF+"SessionGC(): deleting keys on pattern: "+sess_keys, session.LOG_LEVEL_DEBUG)
			}
			pder.removeOnPattern(sess_keys)
		}
	}
}

func (pder *Provider) DestroyAllSessions(l io.Writer, logLev session.LogLevel) {
	sess_keys := pder.namespace + ":*"
	if l != nil && logLev >= session.LOG_LEVEL_DEBUG {
		session.WriteToLog(l, LOG_PREF+"DestroyAllSessions(): deleting keys on pattern: "+sess_keys, session.LOG_LEVEL_DEBUG)
	}
	pder.removeOnPattern(sess_keys)
}

func (pder *Provider) SetMaxLifeTime(maxLifeTime int64) {
	pder.maxLifeTime = maxLifeTime
}
func (pder *Provider) GetMaxLifeTime() int64 {
	return pder.maxLifeTime
}

func (pder *Provider) SetMaxIdleTime(maxIdleTime int64) {
	pder.maxIdleTime = maxIdleTime
}

func (pder *Provider) GetMaxIdleTime() int64 {
	return pder.maxIdleTime
}

func (pder *Provider) CloseProvider() {
}

// InitProvider initializes postgresql provider.
// Function expects two parameters:
//
//	0 parameter: Redis url string, redis://<user>:<pass>@localhost:6379/<db>
//	1 parameter: redis namespace (username)
func (pder *Provider) InitProvider(provParams []interface{}) error {
	if len(provParams) < 2 {
		return errors.New("InitProvider missing parameters: <redis connection string>, <redis namespace>")
	}
	conn_url, ok := provParams[0].(string)
	if !ok {
		return errors.New("InitProvider redis connection parameter(0) must be a string")
	}

	pder.namespace, ok = provParams[1].(string)
	if !ok {
		return errors.New("InitProvider redis namespace parameter(1) must be a string")
	}

	redis_opts, err := redis.ParseURL(conn_url)
	if err != nil {
		return err
	}
	pder.client = redis.NewClient(redis_opts)
	if _, err := pder.client.Ping(context.Background()).Result(); err != nil {
		return err
	}

	return nil
}

func (pder *Provider) GetSessionIDLen() int {
	return SESS_ID_LEN
}

// removeSession removes all values with keys sess:SESSION_ID:*
// helper function for SessionDestroy and SessionGC
func (pder *Provider) removeSession(sid string) error {
	return pder.removeOnPattern(pder.getPrefixedKey(sid, "*"))
}

// removeOnKey removes all kes on pattern
func (pder *Provider) removeOnPattern(pattern string) error {
	ctx := context.Background()
	iter := pder.client.Scan(ctx, 0, pattern, 0).Iterator()
	for iter.Next(ctx) {
		if err := pder.client.Del(ctx, iter.Val()).Err(); err != nil {
			return err
		}
	}
	return iter.Err()
}

// protected
func (pder *Provider) sessionAccessed(sid string) error {
	return pder.setValue(sid, "time_accessed", time.Now())
}

func (pder *Provider) getValue(sid, key string, t interface{}) error {
	if err := pder.getValueForKey(pder.getPrefixedKey(sid, key), t); err != nil {
		return err
	}
	pder.sessionAccessed(sid)
	return nil
}

func (pder *Provider) getValueForKey(redisKey string, t interface{}) error {
	val_b, err := pder.client.Get(context.Background(), redisKey).Bytes()
	if err != nil {
		return err
	}
	if len(val_b) == 0 {
		return EKeyNotFound //no value found
	}
	dec := gob.NewDecoder(bytes.NewBuffer(val_b))
	if err := dec.Decode(t); err != nil {
		return err
	}
	return nil
}

func (pder *Provider) setValue(sid string, key string, val interface{}) error {
	var b bytes.Buffer //value to bytes
	enc := gob.NewEncoder(&b)
	if err := enc.Encode(val); err != nil {
		return err
	}
	prefixed_key := pder.getPrefixedKey(sid, key)
	return pder.client.Set(context.Background(), prefixed_key, b.Bytes(), time.Duration(pder.maxLifeTime)*time.Second).Err()
}

func (pder *Provider) getPrefixedKey(sid, key string) string {
	return pder.namespace + ":" + sid + ":" + key
}

func init() {
	session.Register(PROVIDER, pder)
}
