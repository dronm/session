module github.com/dronm/session/redis

go 1.21.3

replace github.com/dronm/session => /home/andrey/go/session

require (
	github.com/dronm/session v0.0.0
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/redis/go-redis/v9 v9.5.1 // indirect
)
