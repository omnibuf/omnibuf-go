module github.com/omnibuf/omnibuf-go/omnibufconnect

go 1.26.0

replace (
	github.com/omnibuf/omnibuf-go => ..
	github.com/omnibuf/omnibuf-go/omnibufproto => ../omnibufproto
)

require (
	connectrpc.com/connect/v2 v2.0.2
	github.com/omnibuf/omnibuf-go v0.2.12
	github.com/omnibuf/omnibuf-go/omnibufproto v0.2.11
)

require google.golang.org/protobuf v1.37.0-rc.1 // indirect
