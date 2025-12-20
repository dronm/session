// testing functions for session/sqlite.
package sqlite

import (
	"database/sql"
	"encoding/gob"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/dronm/session" //session manager
)

const (
	SQLITE_FILENAME = "test.db"
)

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
	//Register custom struct for marshaling.
	gob.Register(TestStruct{})
	gob.Register(time.Time{})

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
	return session.NewManager(PROVIDER, idleTime, lifeTime, killTime, SQLITE_FILENAME)
}

func ClearManager(manager *session.Manager) {
	manager.CloseProvider()
	os.Remove(SQLITE_FILENAME)
}

// InitTestDb opens new connection to test database file and initializes database objects.
func InitTestDb() error {
	conn, err := sql.Open("sqlite3", SQLITE_FILENAME)
	if err != nil {
		return err
	}
	sql := `CREATE TABLE IF NOT EXISTS session_vals
	(id varchar(35) NOT NULL PRIMARY KEY,
	accessed_time datetime DEFAULT CURRENT_DATETIME,
	create_time datetime DEFAULT CURRENT_DATETIME,
	val bytea
	);`
	if _, err := conn.Exec(sql); err != nil {
		return err
	}
	return nil
}

// TestTemp opens a new session, puts temporary data to it, reads and compares.
func TestTemp(t *testing.T) {
	if err := InitTestDb(); err != nil {
		t.Fatalf("InitTestDb() failed: %v", err)
	}

	gob.Register(time.Time{})
	SessManager, err := NewManager(t, 0, 0, "")
	if err != nil {
		t.Fatalf("NewManager() failed: %v", err)
	}
	defer ClearManager(SessManager)

	//start new session
	currentSession, err := SessManager.SessionStart("")
	if err != nil {
		t.Fatalf("SessionStart() failed: %v", err)
	}

	sid := currentSession.SessionID()
	t.Logf("SessionID: %s", sid)
	var wanted int64 = 177
	//wanted := time.Now().Truncate(time.Second)
	//wanted := "time.Now().Truncate(time.Second)"
	if err := currentSession.Put("dVal", wanted); err != nil {
		t.Fatalf("Put() for string value failed: %v", err)
	}
	//int_v := currentSession.GetInt("dVal")
	//var got time.Time
	//var got string
	var got int64 = 0
	if err := currentSession.Get("dVal", &got); err != nil {
		t.Fatalf("Put() for string value failed: %v", err)
	}
	if got != wanted {
		t.Fatalf("Wanted %v, got: %v", wanted, got)
	}
}

func TestSession(t *testing.T) {
	if err := InitTestDb(); err != nil {
		t.Fatalf("InitTestDb() failed: %v", err)
	}

	SessManager, err := NewManager(t, 0, 0, "")
	if err != nil {
		t.Fatalf("NewManager() failed: %v", err)
	}
	defer ClearManager(SessManager)

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
