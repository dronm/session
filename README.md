# GoLang sessions for web apps.
Supported providers:
- Postgresql (with pgx driver)
- Redis (with go-redis)
See test file for details.

## Usage for pg:
```golang
import (
	"fmt"
	"context"
	"encoding/gob"				//used for custom data types encoding
	
	"github.com/dronM/session"      	//session manager
	_ "github.com/dronM/session/pg" 	//postgresql session provider
	
	"github.com/jackc/pgx/v4/pgxpool"	//pg driver		
)

// ENC_KEY holds session encrypt key
const ENC_KEY = "4gWv64T54583v8t410-45vkUiopgjw4gwmjRcGkck,ld"

// SomeStruct holds custom data type
type SomeStruct struct {
	IntVal   int
	FloatVal float32
	StrVal   string
}

func main(){
	conn_s := "{USER_NAME}@{HOST}:{PORT}/{DATABASE}"
	//creating session manager with 0 TTL, 0 max idle time,
	//requesting to clear all sessions at 03:00
	SessManager, er := session.NewManager("pg", 0, 0, "03:00", conn_s, ENC_KEY)
	if er != nil {
		panic(fmt.Sprintf("NewManager() fail: %v\n", err))
	}

	// Starting new session with unique random ID
	currentSession, er := SessManager.SessionStart("")
	if er != nil {
		panic(fmt.Sprintf("SessionStart() fail: %v\n", err))
	}

	//Register custom struct for marshaling.
	gob.Register(SomeStruct{})

	// Setting data
	currentSession.Set("strVal", "Some string")
	currentSession.Set("intVal", 125)
	currentSession.Set("floatVal", 3.14)	
	currentSession.Set("customVal", SomeStruct{IntVal: 375, FloatVal: 3.14, StrVal: "Some string value in struct"})
	
	// Flushing to database
	if err := currentSession.Flush(); err != nil {
		panic(fmt.Sprintf("Flush() fail: %v\n", err))
	}

	if err := SessManager.SessionClose(currentSession.SessionID()); err != nil {
		panic(fmt.Sprintf("SessionClose() fail: %v\n", err))
	}	
}
```
