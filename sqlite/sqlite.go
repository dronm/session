// Package sqlite contains postgresql session provider based on jackc connection to PG.
// Requirements:
//
//	 Sqlite connection github.com/mattn/go-sqlite3
//		Some SQL scripts are nesessary:
//			session_vals.sql contains table for holding session values
//			session_vals_process.sql trigger function for updating login information (logins table must be present in database)
//			session_vals_trigger.sql creating trigger script
//
// Internally gob encoder is used for data serialization. Session data is read at start and kept in memory SessionStore structure.
// Session key-value pares are kept in storeValue type.
package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync"
	"time"

	"github.com/dronm/session"
	_ "github.com/mattn/go-sqlite3"
)

var EKeyNotFound = errors.New("key not found")
var EValMustBePtr = errors.New("value must be of type ptr")

// Session key ID length. As it is stored it pg data base in varchar column its length is limited.
const SESS_ID_LEN = 36

const PROVIDER = "sqlite3"

const LOG_PREF = "sqlite provider:"

// pder holds pointer to Provider struct.
var pder = &Provider{}

// storeValue holds session key-value pares.
type storeValue map[string]interface{}

// SessionStore contains session information.
type SessionStore struct {
	sid           string //session id
	mx            sync.RWMutex
	timeAccessed  time.Time  //last modified
	timeCreated   time.Time  //when created
	value         storeValue //key-value pair
	valueModified bool
}

// Set sets inmemory value. No database flush is done.
func (st *SessionStore) Set(key string, value interface{}) error {
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

func (st *SessionStore) Put(key string, value interface{}) error {
	if err := st.Set(key, value); err != nil {
		return err
	}
	return st.Flush()
}

// Flush performs the actual write to database.
func (st *SessionStore) Flush() error {
	//flush val only if it's been modified
	if st.valueModified {
		//modified
		val, err := getForDb(&st.value)
		if err != nil {
			return err
		}

		st.mx.Lock()
		defer st.mx.Unlock()

		if _, err = pder.dbConn.ExecContext(context.Background(),
			`UPDATE session_vals
			SET
				val = $1,
				accessed_time = datetime()
			WHERE id = $2`,
			val,
			st.sid,
		); err != nil {
			return err
		}
		st.valueModified = false
	}

	return nil
}

// Get returns session value by its key. Value is retrieved from memory.
func (st *SessionStore) Get(key string, val interface{}) error {
	store_val, ok := st.value[key]
	if !ok {
		return EKeyNotFound
	}

	// Get the type of val
	val_type := reflect.TypeOf(val)

	// Make sure val is a pointer
	if val_type.Kind() != reflect.Ptr {
		return EValMustBePtr
	}

	// Dereference the pointer and check if it's assignable
	val_elem := val_type.Elem()
	if !reflect.TypeOf(store_val).AssignableTo(val_elem) {
		return errors.New("value type mismatch")
	}

	// Assign the value to val
	reflect.ValueOf(val).Elem().Set(reflect.ValueOf(store_val))

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

	if v_bool, ok := v.(bool); ok {
		return v_bool
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

	if v_str, ok := v.(string); ok {
		return v_str

	} else if v_str, ok := v.([]byte); ok {
		return string(v_str)
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

	if v_i, ok := v.(int64); ok {
		return v_i

	} else if v_i, ok := v.(int); ok {
		return int64(v_i)
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

	if v_f, ok := v.(float64); ok {
		return v_f

	} else if v_f, ok := v.(float32); ok {
		return float64(v_f)
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

	if v_t, ok := v.(time.Time); ok {
		return v_t
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
	dbConn      *sql.DB
	encrkey     string
	maxLifeTime int64
	maxIdleTime int64
}

func (pder *Provider) NewSessionStore(sid string) *SessionStore {
	return &SessionStore{
		sid:          sid,
		timeAccessed: time.Now(),
		timeCreated:  time.Now(),
		value:        make(map[string]interface{}, 0),
	}
}

// SessionInit initializes session with given ID.
func (pder *Provider) SessionInit(sid string) (session.Session, error) {
	if pder.dbConn == nil {
		return nil, errors.New("Provider not initialized")
	}

	if len(sid) > SESS_ID_LEN {
		return nil, errors.New("Session key length exceeded max value")
	}

	if _, err := pder.dbConn.ExecContext(context.Background(),
		"INSERT OR IGNORE INTO session_vals(id) VALUES($1)",
		sid,
	); err != nil {
		return nil, err
	}
	return pder.NewSessionStore(sid), nil
}

// SessionRead reads session data from db to memory.
func (pder *Provider) SessionRead(sid string) (session.Session, error) {
	var val []byte

	store := pder.NewSessionStore(sid)

	if err := pder.dbConn.QueryRowContext(context.Background(),
		`UPDATE session_vals
		SET
			accessed_time = datetime()
		WHERE id = $1
		RETURNING
			accessed_time,
			create_time,
			val`,
		sid).Scan(&store.timeAccessed,
		&store.timeCreated,
		&val,
	); err != nil && err == sql.ErrNoRows {
		//no such session
		return pder.SessionInit(sid)

	} else if err != nil {
		return nil, err
	}

	if err := setFromDb(&store.value, val); err != nil {
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

	//inactive sessions
	if pder.maxIdleTime > 0 {
		if _, err := pder.dbConn.ExecContext(context.Background(),
			fmt.Sprintf(`DELETE FROM session_vals WHERE accessed_time + '%d seconds' <= datetime()`, pder.maxIdleTime),
		); err != nil {
			//log error
			if l != nil {
				session.WriteToLog(l, fmt.Sprintf(LOG_PREF+"Exec() failed on DELETE FROM session_vals WHERE accessed_time: %v", err), session.LOG_LEVEL_ERROR)
			}
		}
	}

	if pder.maxLifeTime > 0 {
		if _, err := pder.dbConn.ExecContext(context.Background(),
			fmt.Sprintf(`DELETE FROM session_vals WHERE create_time + '%d seconds' <= datetime()`, pder.maxLifeTime),
		); err != nil {
			//log error
			if l != nil {
				session.WriteToLog(l, fmt.Sprintf(LOG_PREF+"Exec() failed on DELETE FROM session_vals WHERE create_time: %v", err), session.LOG_LEVEL_ERROR)
			}
		}
	}
}

func (pder *Provider) DestroyAllSessions(l io.Writer, logLev session.LogLevel) {
	if _, err := pder.dbConn.ExecContext(context.Background(), `DELETE FROM session_vals`); err != nil {
		if l != nil {
			session.WriteToLog(l, fmt.Sprintf(LOG_PREF+"Exec() failed on DELETE FROM session_vals: %v", err), session.LOG_LEVEL_ERROR)
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
// Function expects one parameter: path to a database file.
// This function opens connection.
func (pder *Provider) InitProvider(provParams []interface{}) error {
	if len(provParams) < 1 {
		return errors.New("InitProvider missing parameters: path to a database file")
	}
	dbFileName, ok := provParams[0].(string)
	if !ok {
		return errors.New("InitProvider path to a database file must be a string")
	}

	conn, err := sql.Open(PROVIDER, dbFileName)
	if err != nil {
		return fmt.Errorf("sql.Open failed: %v", err)
	}
	pder.dbConn = conn

	return nil
}

// CloseProvider closes all database connections.
func (pder *Provider) CloseProvider() {
	pder.dbConn.Close()
}

func (pder *Provider) removeSessionFromDb(sid string) error {
	if _, err := pder.dbConn.ExecContext(context.Background(), `DELETE FROM session_vals WHERE id = $1`, sid); err != nil {
		return err
	}
	return nil
}

func (pder *Provider) GetSessionIDLen() int {
	return SESS_ID_LEN
}

// setFromDb is a helper function, called on retrieving value from data base.
// It decodes data base value for in-memory store.
func setFromDb(strucVal *storeValue, dbVal []byte) error {
	if len(dbVal) == 0 {
		return nil
	}
	dec := gob.NewDecoder(bytes.NewBuffer(dbVal))
	if err := dec.Decode(strucVal); err != nil {
		return err
	}
	return nil
}

// getForDb is a helper function called before putting value to database.
// It encodes in-memory session value for data base.
func getForDb(strucVal *storeValue) ([]byte, error) {
	var b bytes.Buffer
	enc := gob.NewEncoder(&b)
	err := enc.Encode(strucVal)
	if err != nil {
		return []byte{}, err
	}
	return b.Bytes(), nil
}

func init() {
	session.Register(PROVIDER, pder)
}
