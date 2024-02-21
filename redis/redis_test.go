// testing functions for session/redis.
// Testing asumes the following:
//
//	redis is running on localhost.
//	Database test_proj already exists with PGCRYPTO extension.
//	User postgres has no password access to the database.
package redis

import (
	"encoding/gob"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/dronm/session" //session manager
)

const (
	//redis connection in the following format - redis://<user>:<pass>@localhost:6379/<db>
	ENV_REDIS_CONN      = "REDIS_CONN"
	ENV_REDIS_NAMESPACE = "REDIS_NAMESPACE"
	ENV_REDIS_SESS_ID   = "REDIS_SESS_ID"
)

func getTestVar(t *testing.T, n string) string {
	v := os.Getenv(n)
	if v == "" {
		t.Fatalf("getTestVar() failed: %s environment variable is not set", n)
	}
	return v
}

// TestStruct custom struct for use in session.
type TestStruct struct {
	IntVal   int
	FloatVal float32
	StrVal   string
}

func NewTestStruct() TestStruct {
	return TestStruct{IntVal: 375, FloatVal: 3.14, StrVal: "Some string value in struct"}
}

func NewTestValues() map[string]interface{} {
	return map[string]interface{}{
		"stringVal":  "some string value",
		"int32Val":   int32(2147483647),
		"int64Val":   2147483647 * 2,
		"float32Val": float32(3.14),
		"float64Val": float64(3.14),
		"dateVal":    time.Now().Truncate(time.Second),
		"structVal":  NewTestStruct(),
	}	
}

func putValues(t *testing.T, currentSession session.Session, tests map[string]interface{}) {
	//test writing
	for key, val := range tests {
		t.Logf("Setting key: %s to %v", key, val)
		if err := currentSession.Set(key, val); err != nil {
			t.Fatalf("Set() for string value failed: %v", err)
		}
	}
	if err := currentSession.Flush(); err != nil {
		t.Fatalf("Flush() failed: %v", err)
	}
}

func compareValues(t *testing.T, currentSession session.Session, tests map[string]interface{}) {
	for key, wanted := range tests {
		t.Logf("Getting key: %s", key)
		
		ptr := reflect.New(reflect.TypeOf(wanted))
		err := currentSession.Get(key, ptr.Interface())
		if err != nil {
			t.Fatalf("Get() failed: %v", err)
		}
		got := ptr.Elem().Interface()
		if !reflect.DeepEqual(got, wanted) {
			t.Fatalf("Wanted: %v, got %v", wanted, got)
		}
	}
}

func assertNoValues(t *testing.T, currentSession session.Session, tests map[string]interface{}) {
	for key, wanted := range tests {
		ptr := reflect.New(reflect.TypeOf(wanted))
		err := currentSession.Get(key, ptr.Interface())	
		if err == nil {
			t.Fatalf("Session: %s is not destroyed", currentSession.SessionID())
		}
	}
}

func NewManager(t *testing.T, idleTime int64, lifeTime int64, killTime string) (*session.Manager, error) {
	return session.NewManager(PROVIDER, idleTime, lifeTime, killTime, getTestVar(t, ENV_REDIS_CONN), getTestVar(t, ENV_REDIS_NAMESPACE))
}

func TestSession(t *testing.T) {
	//Register custom struct for marshaling.
	gob.Register(TestStruct{})

	SessManager, err := NewManager(t, 0, 0, "")
	if err != nil {
		t.Fatalf("NewManager() failed: %v", err)
	}

	//start new session
	currentSession, err := SessManager.SessionStart("")
	if err != nil {
		t.Fatalf("SessionStart() failed: %v", err)
	}

	sid := currentSession.SessionID()
	t.Logf("SessionID: %s", sid)

	tests := NewTestValues()
	putValues(t, currentSession, tests)

	//test reading
	compareValues(t, currentSession, tests)

	t.Logf("Closing session: %s", sid)
	if err := SessManager.SessionClose(sid); err != nil {
		t.Fatalf("SessionClose() failed: %v", err)
	}

	t.Logf("Reopening session: %s", sid)
	//reopen
	currentSession, err = SessManager.SessionStart(sid)
	if err != nil {
		t.Errorf("SessionStart() failed: %v", err)
	}
	//test reading
	compareValues(t, currentSession, tests)

	if err := SessManager.SessionClose(currentSession.SessionID()); err != nil {
		t.Errorf("SessionClose() failed: %v", err)
	}
	
	//destroying session
	t.Logf("Destroying session: %s", sid)
	if err := SessManager.SessionDestroy(sid); err != nil {
		t.Errorf("SessManager.SessionDestroy() failed: %v", err)
	}
	
	currentSession, err = SessManager.SessionStart(sid)
	if err != nil {
		t.Errorf("SessionStart() failed: %v", err)
	}
	t.Logf("Trying to read from session")
	assertNoValues(t, currentSession, tests)
	t.Logf("Session destroyed to read from session")
}

// TestDestroyAllSessions creates a session, puts some data, destroys this session,
// then tries to reopen and read from the session. If at leas one key is found, test fails.
func TestDestroyAllSessions(t *testing.T) {	
	SessManager, err := NewManager(t, 0, 0, "")
	if err != nil {
		t.Fatalf("NewManager() failed: %v", err)
	}
	
	//start new session
	currentSession, err := SessManager.SessionStart("")
	if err != nil {
		t.Fatalf("SessionStart() failed: %v", err)
	}

	sid := currentSession.SessionID()
	t.Logf("SessionID: %s", sid)

	tests := NewTestValues()
	putValues(t, currentSession, tests)
	
	SessManager.DestroyAllSessions(os.Stderr, session.LOG_LEVEL_DEBUG)

	currentSession, err = SessManager.SessionStart(sid)
	if err != nil {
		t.Errorf("SessionStart() failed: %v", err)
	}
	t.Logf("Trying to read from session")
	assertNoValues(t, currentSession, tests)
}

// TestLifeTime creates a session with a limited life time.
// Then waiting for the time more than our life time.
// After that SessionGC() is called. 
// Then data is retrieved. The session should have been deleted by then.
// The test fails if any key persists.
func TestLifeTime(t *testing.T) {
	var life_time int64 = 2
	SessManager, err := NewManager(t, life_time, 0, "")
	if err != nil {
		t.Fatalf("NewManager() failed: %v", err)
	}
	
	currentSession, err := SessManager.SessionStart("")
	sid := currentSession.SessionID()
	t.Logf("SessionID: %s", sid)

	tests := NewTestValues()
	putValues(t, currentSession, tests)
	if err := SessManager.SessionClose(currentSession.SessionID()); err != nil {
		t.Errorf("SessionClose() failed: %v", err)
	}
	
	t.Logf("waiting %d seconds for session to be killed", life_time + 2)
	time.Sleep(time.Duration(life_time) * time.Second)	
	
	SessManager.SessionGC(os.Stderr, session.LOG_LEVEL_DEBUG)
		
	currentSession, err = SessManager.SessionStart(sid)
	if err != nil {
		t.Errorf("SessionStart() failed: %v", err)
	}
	t.Logf("Trying to read from session")
	assertNoValues(t, currentSession, tests)	
	t.Logf("The session %s is destroyed", sid)
}

// TestIdleTime creates a session with a limited idle time.
// Some values are put to session store, then retrieved, asserted they exist.
// Then session data is not touched more then idle time.
// After that SessionGC() is called. 
// Then data is retrieved. The session should have been deleted by then.
// The test fails if any key persists.
func TestIdleTime(t *testing.T) {
	var idle_time int64 = 3
	SessManager, err := NewManager(t, 0, idle_time, "")
	if err != nil {
		t.Fatalf("NewManager() failed: %v", err)
	}
	
	currentSession, err := SessManager.SessionStart("")
	sid := currentSession.SessionID()
	t.Logf("SessionID: %s", sid)

	tests := NewTestValues()
	putValues(t, currentSession, tests)
	if err := SessManager.SessionClose(currentSession.SessionID()); err != nil {
		t.Errorf("SessionClose() failed: %v", err)
	}
	
	t.Logf("waiting %d seconds", idle_time/2)
	time.Sleep(time.Duration(idle_time/2) * time.Second)	
	SessManager.SessionGC(os.Stderr, session.LOG_LEVEL_DEBUG)	
	//test reading
	compareValues(t, currentSession, tests)	
	
	t.Logf("waiting %d seconds for session to be killed", idle_time + 2)
	time.Sleep(time.Duration(idle_time) * time.Second)	
	
	SessManager.SessionGC(os.Stderr, session.LOG_LEVEL_DEBUG)
		
	currentSession, err = SessManager.SessionStart(sid)
	if err != nil {
		t.Errorf("SessionStart() failed: %v", err)
	}
	t.Logf("Trying to read from session")
	assertNoValues(t, currentSession, tests)	
	t.Logf("The session %s is destroyed", sid)
}

// TestKillByTime creates a session with a fixed kill time set to Now() + X seconds and starts GC.
// Some values are put to session store, then we wait some tome less then X, retrieve values, assert they exist.
// Then we wait some more time to pass the fixed time.
// After that SessionGC() is called. 
// Then data is retrieved. The session should have been deleted by then.
// The test fails if any key persists.
func TestKillByTime(t *testing.T) {
	var in_sec int64 = 3
	tm := time.Now()
	tm2 := tm.Add(time.Duration(in_sec) * time.Second)
	m := tm2.Format("15:04:05")
	t.Logf("Creating session manager and start GC at %s.", tm.Format("15:04:05"))
	t.Logf("Expecting all sessions to be cleared in %d seconds at %s", in_sec, m)
	
	SessManager, err := NewManager(t, 0, 0, m)
	if err != nil {
		t.Fatalf("NewManager() failed: %v", err)
	
	
	}
	SessManager.StartGC(os.Stderr, session.LOG_LEVEL_DEBUG)
	
	currentSession, err := SessManager.SessionStart("")
	sid := currentSession.SessionID()
	t.Logf("SessionID: %s", sid)

	tests := NewTestValues()
	putValues(t, currentSession, tests)
	if err := SessManager.SessionClose(currentSession.SessionID()); err != nil {
		t.Errorf("SessionClose() failed: %v", err)
	}
	
	t.Logf("waiting %d seconds", 1)
	time.Sleep(time.Duration(1) * time.Second)	
	//test reading
	compareValues(t, currentSession, tests)	
	
	t.Logf("waiting %d seconds for session to be killed", in_sec + 2)
	time.Sleep(time.Duration( in_sec + 2) * time.Second)	
	
	currentSession, err = SessManager.SessionStart(sid)
	if err != nil {
		t.Errorf("SessionStart() failed: %v", err)
	}
	t.Logf("Trying to read from session")
	assertNoValues(t, currentSession, tests)	
	t.Logf("The session %s is destroyed", sid)
}

// TestRestartGC
func TestRestartGC(t *testing.T) {
	var lt_sec int64 = 3 //idle time
	t.Logf("Creating session manager with idle time: %d seconds", lt_sec)
		
	SessManager, err := NewManager(t, 0, lt_sec, "")
	if err != nil {
		t.Fatalf("NewManager() failed: %v", err)
	}
	SessManager.StartGC(os.Stderr, session.LOG_LEVEL_DEBUG)
	
	currentSession, err := SessManager.SessionStart("")
	tests := NewTestValues()
	putValues(t, currentSession, tests)
	if err := SessManager.SessionClose(currentSession.SessionID()); err != nil {
		t.Errorf("SessionClose() failed: %v", err)
	}
	time.Sleep(time.Duration(1) * time.Second)
	compareValues(t, currentSession, tests)		
	
	//reset the GC time
	lt_sec = lt_sec * 2
	t.Logf("Resetting the idle time to %d seconds", lt_sec)
	SessManager.StopGC()
	SessManager.SetMaxIdleTime(lt_sec)
	SessManager.StartGC(os.Stderr, session.LOG_LEVEL_DEBUG)

	t.Logf("Waiting %d seconds", lt_sec + 2)
	time.Sleep(time.Duration(lt_sec + 2) * time.Second)
	t.Logf("Trying to read from session")
	assertNoValues(t, currentSession, tests)	
	t.Logf("The session %s is destroyed", currentSession.SessionID())
}	

