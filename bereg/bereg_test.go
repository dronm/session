package bereg

import (
	"context"
	"os"
	"reflect"
	"testing"

	"github.com/dronm/session" //session manager

	"github.com/joho/godotenv"

	"github.com/jackc/pgx/v5/pgxpool"
)

const(
	sessProvider = "bereg"
)

func getTestVarStr(t *testing.T, n string) string {
	v := os.Getenv(n)
	if v == "" {
		t.Fatalf("getTestVar() failed: %s environment variable is not set", n)
	}
	return v
}

func putValues(t *testing.T, currentSession session.Session, tests map[string]any) {
	//test writing
	for key, val := range tests {
		t.Logf("Setting key: %s to %v", key, val)
		if err := currentSession.Set(key, val); err != nil {
			t.Fatalf("Set() for string value: %v", err)
		}
	}
	if err := currentSession.Flush(); err != nil {
		t.Fatalf("Flush(): %v", err)
	}
}

func compareValues(t *testing.T, currentSession session.Session, tests map[string]any) {
	for key, wanted := range tests {
		t.Logf("Getting key: %s", key)

		ptr := reflect.New(reflect.TypeOf(wanted))
		err := currentSession.Get(key, ptr.Interface())
		if err != nil {
			t.Fatalf("Get(): %v", err)
		}
		got := ptr.Elem().Interface()
		if !reflect.DeepEqual(got, wanted) {
			t.Fatalf("Wanted: %v, got %v", wanted, got)
		}
	}
}

func NewManager(t *testing.T, idleTime int64, lifeTime int64, killTime string) (*session.Manager, error) {
	dbpool, err := pgxpool.New(context.Background(), getTestVarStr(t, "DB_CONN"))
	if err != nil {
		t.Fatalf("NewManager() failed(): %v", err)
	}
	return session.NewManager(sessProvider, idleTime, lifeTime, killTime, dbpool, getTestVarStr(t, "SESS_KEY"))
}

func TestExistingSession(t *testing.T) {
	err := godotenv.Load()
	if err != nil {
		t.Fatal("Error loading .env file")
	}

	sessManager, err := NewManager(t, 0, 0, "")
	if err != nil {
		t.Fatalf("NewManager(): %v", err)
	}

	existingSessID := getTestVarStr(t, "SESS_ID")

	//start existing session
	currentSession, err := sessManager.SessionStart(existingSessID)
	if err != nil {
		t.Fatalf("SessionStart(): %v", err)
	}

	sid := currentSession.SessionID()
	if sid != existingSessID {
		t.Fatalf("wanted session ID to be %s, got %s", existingSessID, sid)
	}
	t.Logf("SessionID: %s", sid)
}

func TestExistingSessionVal(t *testing.T) {
	err := godotenv.Load()
	if err != nil {
		t.Fatal("Error loading .env file")
	}

	sessManager, err := NewManager(t, 0, 0, "")
	if err != nil {
		t.Fatalf("NewManager(): %v", err)
	}

	existingSessID := getTestVarStr(t, "SESS_ID")

	//start existing session
	currentSession, err := sessManager.SessionStart(existingSessID)
	if err != nil {
		t.Fatalf("SessionStart(): %v", err)
	}

	sessValues := map[string]any{ 
		"LOGGED": true,
		"user_id": getTestVarStr(t, "SESS_USER_ID"),
		"role_id": getTestVarStr(t, "SESS_USER_ROLE"),
		"user_name": getTestVarStr(t, "SESS_USER_NAME"),
	}

	compareValues(t, currentSession, sessValues)

}
