// Package session provides support for sessions in web applications.
// It gives interface for specific session implementation.
// Session providers are implemented as specific packages.
// Max session life time, max session idle time, fixed time clearing are supported.
// See NewManager() function for specific session handling parameters.
package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"sync"
	"time"
)

const (
	TIME_SEC_LAYOUT = "15:04:05"
	TIME_MIN_LAYOUT = "15:04"
)

var log_levels = []string{"ERROR", "WARN", "DEBUG"}

type LogLevel int

func (lv LogLevel) String() string {
	return log_levels[lv]
}

const (
	LOG_LEVEL_ERROR LogLevel = iota
	LOG_LEVEL_WARN
	LOG_LEVEL_DEBUG
)

// Session interface for session functionality.
type Session interface {
	Set(key string, value interface{}) error //set session value
	Put(key string, value interface{}) error //set session value and flushes
	Get(key string, value interface{}) error //get session value
	GetBool(key string) bool                 //get bool session value, false if no key or assertion error
	GetString(key string) string             //get string session value, empty string if no key or assertion error
	GetInt(key string) int64                 //get int64 session value, 0 if no key or assertion error
	GetFloat(key string) float64             //get float64 session value, 0.0 if no key or assertion error
	Delete(key string) error                 //delete session value
	SessionID() string                       //returns current sessionID
	Flush() error				 //flushes data to persistent storage
	TimeCreated() time.Time
	TimeAccessed() time.Time
}

// Provider interface for session provider.
type Provider interface {
	InitProvider(provParams []interface{}) error
	SessionInit(sid string) (Session, error)
	SessionRead(sid string) (Session, error)
	SessionDestroy(sid string) error
	SessionClose(sid string) error
	SessionGC(io.Writer, LogLevel)
	GetSessionIDLen() int
	SetMaxLifeTime(int64)
	GetMaxLifeTime() int64
	SetMaxIdleTime(int64)
	GetMaxIdleTime() int64
	DestroyAllSessions(io.Writer, LogLevel)
}

var provides = make(map[string]Provider)

// Register makes a session provider available by the provided name.
// If Register is called twice with the same name or if driver is nil,
// it panics.
func Register(name string, provide Provider) {
	if provide == nil {
		panic("session: Register provide is nil")
	}
	if _, dup := provides[name]; dup {
		panic("session: Register called twice for provide " + name)
	}
	provides[name] = provide
}

// Manager structure for holding provider.
type Manager struct {
	lock             sync.Mutex
	provider         Provider
	SessionsKillTime time.Time //clears all sessions
	gcCancel         context.CancelFunc
}

// NewManager is a Manager create function.
// providerName is a name of the data storage provider.
// maxLifeTime is a maximum life of a session in seconds.
// maxIdleTime is a maximum idle time of a session in seconds. Idling is the time during which a session is not accessed.
// sessionsKillTime is a time in format 00:00 or 00:00:00 at which all sessions will be killed on dayly bases.
// See details how session destruction is handled in StartGC()
// provParams contains provider specific arguments.
func NewManager(providerName string, maxLifeTime int64, maxIdleTime int64, sessionsKillTime string, provParams ...interface{}) (*Manager, error) {
	provider, ok := provides[providerName]
	if !ok {
		return nil, fmt.Errorf("session: unknown provider %q (forgotten import?)", providerName)
	}

	provider.SetMaxLifeTime(maxLifeTime)
	provider.SetMaxIdleTime(maxIdleTime)

	manager := &Manager{provider: provider}
	if sessionsKillTime != "" {
		if err := manager.SetSessionsKillTime(sessionsKillTime); err != nil {
			return nil, err
		}
	}
	if err := manager.provider.InitProvider(provParams); err != nil {
		return nil, err
	}
	return manager, nil
}

// SetSessionsKillTime sets time from the given string value
func (manager *Manager) SetSessionsKillTime(sessionsKillTime string) error {
	var err error
	manager.SessionsKillTime, err = parseTime(sessionsKillTime)
	if err != nil {
		return err
	}
	return nil
}

// SetMaxLifeTime is an alias for provider SetMaxLifeTime
func (manager *Manager) SetMaxLifeTime(maxLifeTime int64) {
	manager.provider.SetMaxLifeTime(maxLifeTime)
}

// SetMaxIdleTime is an alias for provider SetMaxIdleTime
func (manager *Manager) SetMaxIdleTime(maxIdleTime int64) {
	manager.provider.SetMaxIdleTime(maxIdleTime)
}

// GetSessionIDLen returns session ID length specific for provider.
func (manager *Manager) GetSessionIDLen() int {
	return manager.provider.GetSessionIDLen()
}

// SessionStart opens session with the given ID.
func (manager *Manager) SessionStart(sid string) (Session, error) {
	//manager.lock.Lock()
	//defer manager.lock.Unlock()

	if sid == "" {
		sid := manager.genSessionID()
		return manager.provider.SessionInit(sid)
	}
	return manager.provider.SessionRead(sid)
}

// SessionClose closes session with the given ID.
func (manager *Manager) SessionClose(sid string) error {
	if sid != "" {
		return manager.provider.SessionClose(sid)
	}
	return nil
}

// InitProvider initializes provider with its specific parameters.
// Should consult specific provider package to know its parameters.
func (manager *Manager) InitProvider(provParams []interface{}) error {
	return manager.provider.InitProvider(provParams)
}

// SessionDestroy destroys session by its ID.
func (manager *Manager) SessionDestroy(sid string) error {
	if sid == "" {
		return nil
	} else {
		return manager.provider.SessionDestroy(sid)
	}
}

func (manager *Manager) SessionGC(l io.Writer, logLev LogLevel) {
	manager.provider.SessionGC(l, logLev)
}

func (manager *Manager) DestroyAllSessions(l io.Writer, logLev LogLevel) {
	manager.provider.DestroyAllSessions(l, logLev)
}

// StartGC starts garbage collection (GC) server for managing sessions destruction.
// Session can be destroyed:
//   - if SessionsKillTime is set, then all sessions will be cleared at that time.
//   - if idle time is set, then sessions idling (session access time is controled) more then that time will be cleared.
//   - if max life time is set, then sessions will live no more then that specified time, no matter idling or not.
//
// SessionsKillTime runs its own independant gorouting.
// MaxLifeTime and MaxIdleTime are combined in one gorouting.
// All thee parameters can be used together.
// Goroutings are controled by a context an can be cancelled.
// So it is possible to modify SessionsKillTime/MaxLifeTime/MaxIdleTime and to restart the GC server
// Server does not generate any output. Instead all errors/comments are sent to io.Writer passed as argument to StartGC() function.
func (manager *Manager) StartGC(l io.Writer, logLev LogLevel) {
	var ctx context.Context
	ctx, manager.gcCancel = context.WithCancel(context.Background())

	empty_t := time.Time{}
	if manager.SessionsKillTime != empty_t {
		//destroy all sessions at certain time
		go (func() {
		gc_loop:
			for {
				//calculate new sleep time
				now := time.Now().Truncate(time.Second)
				now_sec := now.Hour()*60*60 + now.Minute()*60 + now.Second()
				kill_sec := manager.SessionsKillTime.Hour()*60*60 + manager.SessionsKillTime.Minute()*60 + manager.SessionsKillTime.Second()
				sleep_sec := kill_sec - now_sec
				if sleep_sec < 0 {
					sleep_sec = 24*60*60 + sleep_sec
				}

				if l != nil && logLev >= LOG_LEVEL_WARN {
					WriteToLog(l, fmt.Sprintf("waiting session killer in %d seconds", sleep_sec), LOG_LEVEL_WARN)
				}

				select {
				case <-ctx.Done(): //context cancelled
					break gc_loop

				case <-time.After(time.Duration(sleep_sec) * time.Second): //timeout
					if l != nil && logLev >= LOG_LEVEL_DEBUG {
						WriteToLog(l, "calling manager.DestroyAllSessions()", LOG_LEVEL_DEBUG)
					}
					manager.DestroyAllSessions(l, logLev)
					time.Sleep(time.Duration(1) * time.Second)
				}
			}
		})()
	}

	var sleep_sec int64 //seconds to sleep before the next GC
	life_time := manager.provider.GetMaxLifeTime()
	idle_time := manager.provider.GetMaxIdleTime()
	if life_time == 0 && idle_time == 0 {
		return //do not start
	}
	if idle_time > 0 && life_time == 0 {
		sleep_sec = idle_time

	} else if life_time > 0 && idle_time == 0 {
		sleep_sec = life_time

	} else if idle_time < life_time {
		sleep_sec = idle_time

	} else {
		sleep_sec = life_time
	}

	if l != nil && logLev >= LOG_LEVEL_WARN {
		WriteToLog(l, fmt.Sprintf("running garbage collector every %d seconds", sleep_sec), LOG_LEVEL_DEBUG)
	}

	go (func() {
	gc_loop:
		for {
			select {
			case <-ctx.Done(): //context cancelled
				break gc_loop

			case <-time.After(time.Duration(sleep_sec) * time.Second): //timeout
				if l != nil && logLev >= LOG_LEVEL_DEBUG {
					WriteToLog(l, "calling manager.SessionGC()", LOG_LEVEL_DEBUG)
				}

				manager.SessionGC(l, logLev)
			}
		}
	})()
}

// StopGC stops garbage collection server
func (manager *Manager) StopGC() {
	if manager.gcCancel != nil {
		manager.gcCancel()
	}
}

// genSessionID generates unique ID for a session.
func (manager *Manager) genSessionID() string {
	source := rand.NewSource(time.Now().UnixNano())
	r := rand.New(source)
	b := make([]byte, 16)
	_, err := r.Read(b)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func WriteToLog(w io.Writer, s string, logLevel LogLevel) {
	io.WriteString(w, "SessionManager	"+time.Now().Format(time.RFC3339)+"	"+logLevel.String()+"	"+s+"\n")
}

func parseTime(timeStr string) (time.Time, error) {
	lay_out := ""
	if len(timeStr) == len(TIME_SEC_LAYOUT) {
		lay_out = TIME_SEC_LAYOUT
	} else if len(timeStr) == len(TIME_MIN_LAYOUT) {
		lay_out = TIME_MIN_LAYOUT
	} else {
		return time.Time{}, errors.New("parseTime: unknown layout")
	}

	t, err := time.Parse(lay_out, timeStr)
	if err != nil {
		return time.Time{}, err
	}
	return t, nil

}
