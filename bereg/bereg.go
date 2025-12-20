// Package bereg contains postgresql session provider based on jackc connection to PG and pgp_session.
// Requirements:
//
//	jackc connection to PG Sql https://github.com/jackc/pgx
//	PG_CRYPTO extension must be installed CREATE EXTENSION pgrypto, PGP_SYM_DECRYPT, PGP_SYM_ENCRYPT functions are used,
//	If encryption is not necessary - correct sql in SessionRead/SessionClose functions
//	Some SQL scripts are nesessary:
//		session_vals.sql contains table for holding session values
//		session_vals_process.sql trigger function for updating login information (logins table must be present in database)
//		session_vals_trigger.sql creating trigger script
//
// Internally php_session_decorer encoder is used for data serialization. Session data is read at start and kept in memory SessionStore structure.
// Session key-value pares are kept in storeValue type.
package bereg

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync"
	"time"

	"github.com/dronm/session"

	"github.com/yvasiyarov/php_session_decoder"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrKeyNotFound  = errors.New("key not found")
	ErrValMustBePtr = errors.New("value must be of type ptr")
	ErrKeyLen       = errors.New("session key length exceeded max value")
)

// Session key ID length. As it is stored it pg data base in varchar column its length is limited.
const sessIDLen = 46 //

const providerID = "bereg"

const logPref = "bereg provider:"

// pder holds pointer to Provider struct.
var pder = &Provider{}

// storeValue holds session key-value pares.
// type storeValue php_session_decoder.PhpSession

// SessionStore contains session information.
type SessionStore struct {
	sid           string // session id
	mx            sync.RWMutex
	timeAccessed  time.Time                      // last modified
	timeCreated   time.Time                      // when created
	value         php_session_decoder.PhpSession // key-value pair
	valueModified bool
}

// Set sets inmemory value. No database flush is done.
func (st *SessionStore) Set(key string, value any) error {
	//type assertion is needed
	/*
		var v interface{}

		switch value.(type) {
		case int:
			v = int64(value.(int))
		case int32:
			v = int64(value.(int32))
		case float32:
			v = float64(value.(float32))
		default:
			v = value
		}
	*/
	if !reflect.DeepEqual(st.value[key], value) {
		st.mx.Lock()
		st.value[key] = value
		st.valueModified = true
		st.timeAccessed = time.Now()
		st.mx.Unlock()
	}
	return nil
}

func (st *SessionStore) Put(key string, value any) error {
	if err := st.Set(key, value); err != nil {
		return err
	}
	return st.Flush()
}

// Flush performs the actual write to database.
func (st *SessionStore) Flush() error {
	// flush val only if it's been modified
	if st.valueModified {
		// modified
		val, err := getForDB(st.value)
		if err != nil {
			return err
		}
		conn, err := pder.dbpool.Acquire(context.Background())
		if err != nil {
			return err
		}
		defer conn.Release()

		if _, err = conn.Exec(context.Background(),
			`SELECT sess_enc_write($1, $2, $3, $4)`,
			st.sid,
			val,
			pder.encrkey,
			"", // remote IP
		); err != nil {
			return err
		}
		st.mx.Lock()
		st.valueModified = false
		st.mx.Unlock()
	}

	return nil
}

// Get returns session value by its key. Value is retrieved from memory.
func (st *SessionStore) Get(key string, val any) error {
	storeVal, ok := st.value[key]
	if !ok {
		return ErrKeyNotFound
	}

	// Get the type of val
	valType := reflect.TypeOf(val)

	// Make sure val is a pointer
	if valType.Kind() != reflect.Ptr {
		return ErrValMustBePtr
	}

	// Dereference the pointer and check if it's assignable
	valElem := valType.Elem()
	if !reflect.TypeOf(storeVal).AssignableTo(valElem) {
		return errors.New("value type mismatch")
	}

	// Assign the value to val
	reflect.ValueOf(val).Elem().Set(reflect.ValueOf(storeVal))

	return nil
}

// GetBool returns bool value by key.
func (st *SessionStore) GetBool(key string) bool {
	v, ok := st.value[key]
	if !ok {
		return false
	}
	st.mx.Lock()
	defer st.mx.Unlock()
	st.timeAccessed = time.Now()

	if vBool, ok := v.(bool); ok {
		return vBool
	}

	return false
}

// GetString returns string value by key.
func (st *SessionStore) GetString(key string) string {
	v, ok := st.value[key]
	if !ok {
		return ""
	}

	st.mx.Lock()
	defer st.mx.Unlock()
	st.timeAccessed = time.Now()

	if vStr, ok := v.(string); ok {
		return vStr
	} else if vStr, ok := v.([]byte); ok {
		return string(vStr)
	}
	return ""
}

// GetInt returns int value by key.
func (st *SessionStore) GetInt(key string) int64 {
	v, ok := st.value[key]
	if !ok {
		return 0
	}

	st.mx.Lock()
	defer st.mx.Unlock()
	st.timeAccessed = time.Now()

	if vInt, ok := v.(int64); ok {
		return vInt
	} else if vInt, ok := v.(int); ok {
		return int64(vInt)
	}
	return 0
}

// GetFloat returns float value by key.
func (st *SessionStore) GetFloat(key string) float64 {
	v, ok := st.value[key]
	if !ok {
		return 0
	}

	st.mx.Lock()
	defer st.mx.Unlock()
	st.timeAccessed = time.Now()

	if vFloat, ok := v.(float64); ok {
		return vFloat
	} else if vFloat, ok := v.(float32); ok {
		return float64(vFloat)
	}
	return 0
}

// GetDate returns time.Time value by key.
func (st *SessionStore) GetDate(key string) time.Time {
	v, ok := st.value[key]
	if !ok {
		return time.Time{}
	}

	st.mx.Lock()
	defer st.mx.Unlock()
	st.timeAccessed = time.Now()

	if vTm, ok := v.(time.Time); ok {
		return vTm
	}
	return time.Time{}
}

// Delete deletes session value from memmory by key. No flushing is done.
func (st *SessionStore) Delete(key string) error {
	_, ok := st.value[key]
	if !ok {
		return nil
	}

	st.mx.Lock()
	defer st.mx.Unlock()
	st.timeAccessed = time.Now()
	delete(st.value, key)

	return nil
}

// SessionID returns session unique ID.
func (st *SessionStore) SessionID() string {
	return st.sid
}

// TimeCreated returns timeCreated property.
func (st *SessionStore) TimeCreated() time.Time {
	return st.timeCreated
}

// TimeAccessed returns timeAccessed property.
func (st *SessionStore) TimeAccessed() time.Time {
	return st.timeAccessed
}

// Provider structure holds provider information.
type Provider struct {
	dbpool      *pgxpool.Pool
	encrkey     string
	maxLifeTime int64
	maxIdleTime int64
}

func (pder *Provider) NewSessionStore(sid string) *SessionStore {
	return &SessionStore{
		sid:          sid,
		timeAccessed: time.Now(),
		timeCreated:  time.Now(),
		value:        make(php_session_decoder.PhpSession),
	}
}

// SessionInit initializes session with given ID.
func (pder *Provider) SessionInit(sid string) (session.Session, error) {
	if pder.dbpool == nil {
		return nil, errors.New("Provider not initialized")
	}

	if len(sid) > sessIDLen {
		return nil, ErrKeyLen
	}

	return pder.NewSessionStore(sid), nil
}

// SessionRead reads session data from db to memory.
func (pder *Provider) SessionRead(sid string) (session.Session, error) {
	var val string

	store := pder.NewSessionStore(sid)

	if err := pder.dbpool.QueryRow(context.Background(),
		`SELECT 
			PGP_SYM_DECRYPT(sess.data_enc, $2),
			sess.set_time,
			sess.create_time
		FROM sessions AS sess
		WHERE id = $1
		LIMIT 1`,
		sid, pder.encrkey).Scan(
		&val,
		&store.timeAccessed,
		&store.timeCreated,
	); err != nil && err == pgx.ErrNoRows {
		// no such session
		return pder.SessionInit(sid)
	} else if err != nil {
		return nil, err
	}

	var err error
	store.value, err = setFromDB(val)
	if err != nil {
		return nil, err
	}

	return store, nil
}

func (pder *Provider) SessionClose(sid string) error {
	return nil
}

// SessionDestroy destoys session by its ID.
func (pder *Provider) SessionDestroy(sid string) error {
	if err := pder.removeSessionFromDb(sid); err != nil {
		return err
	}
	return nil
}

// SessionGC clears unused sessions
func (pder *Provider) SessionGC(l io.Writer, logLev session.LogLevel) {
	if pder.maxIdleTime == 0 && pder.maxLifeTime == 0 {
		return
	}

	// inactive sessions
	if pder.maxIdleTime > 0 {
		if _, err := pder.dbpool.Exec(context.Background(),
			fmt.Sprintf(
				`DELETE FROM sessions 
				WHERE set_time + ('%d seconds')::interval <= now()`, pder.maxIdleTime),
		); err != nil {
			// log error
			if l != nil {
				session.WriteToLog(l, fmt.Sprintf(logPref+"Exec() failed on DELETE FROM sessions WHERE set_time: %v", err), session.LOG_LEVEL_ERROR)
			}
		}
	}

	if pder.maxLifeTime > 0 {
		if _, err := pder.dbpool.Exec(context.Background(),
			fmt.Sprintf(`DELETE FROM sessions WHERE create_time + ('%d seconds')::interval <= now()`, pder.maxLifeTime),
		); err != nil {
			// log error
			if l != nil {
				session.WriteToLog(l, fmt.Sprintf(logPref+"Exec() failed on DELETE FROM sessions WHERE create_time: %v", err), session.LOG_LEVEL_ERROR)
			}
		}
	}
}

func (pder *Provider) DestroyAllSessions(l io.Writer, logLev session.LogLevel) {
	if _, err := pder.dbpool.Exec(context.Background(), `DELETE FROM sessions`); err != nil {
		if l != nil {
			session.WriteToLog(l, fmt.Sprintf(logPref+"Exec() failed on DELETE FROM sessions: %v", err), session.LOG_LEVEL_ERROR)
		}
	}
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

// InitProvider initializes postgresql provider.
// Function expects two parameters:
//
//	First parameter: *pgxpool.Pool
//	Second parameter: encryptKey application unique,if to set no encryption used
func (pder *Provider) InitProvider(provParams []any) error {
	if len(provParams) < 2 {
		return errors.New("InitProvider missing parameters: *pgxpool.Pool, encryptKey")
	}
	var ok bool
	pder.dbpool, ok = provParams[0].(*pgxpool.Pool)
	if !ok {
		return errors.New("InitProvider db connection parameter(0) must be of type *pgxpool.Pool")
	}

	pder.encrkey, ok = provParams[1].(string)
	if !ok {
		return errors.New("InitProvider encryptKey parameter(1) must be a string")
	}

	return nil
}

// CloseProvider closes all database connections.
func (pder *Provider) CloseProvider() {
}

func (pder *Provider) removeSessionFromDb(sid string) error {
	if _, err := pder.dbpool.Exec(context.Background(), `DELETE FROM sessions WHERE id = $1`, sid); err != nil {
		return err
	}
	return nil
}

func (pder *Provider) GetSessionIDLen() int {
	return sessIDLen
}

// setFromDB is a helper function, called on retrieving value from data base.
// It decodes data base value for in-memory store.
func setFromDB(dbVal string) (php_session_decoder.PhpSession, error) {
	if len(dbVal) == 0 {
		return nil, nil
	}
	dbValB, err := base64.StdEncoding.DecodeString(dbVal)
	if err != nil {
		return nil, fmt.Errorf("base64.StdEncoding.DecodeString(): %v", err)
	}

	dec := php_session_decoder.NewPhpDecoder(string(dbValB))
	sessData, err := dec.Decode()
	if err != nil {
		return nil, fmt.Errorf("php_session_decoder Decode(): %v", err)
	}
	return sessData, nil
}

// getForDB is a helper function called before putting value to database.
// It encodes in-memory session value for data base.
func getForDB(data php_session_decoder.PhpSession) (string, error) {
	encoder := php_session_decoder.NewPhpEncoder(data)
	result, err := encoder.Encode()
	if err != nil {
		return "", fmt.Errorf("php_session_decoder.Encode(): %v", err)
	}
	result = base64.StdEncoding.EncodeToString([]byte(result))
	return result, nil
}

func init() {
	session.Register(providerID, pder)
}
