module github.com/omnibuf/omnibuf-go/omnibufconnect

go 1.26.0

replace (
	github.com/omnibuf/omnibuf-go => ..
	github.com/omnibuf/omnibuf-go/omnibufproto => ../omnibufproto
)

require (
	connectrpc.com/connect v1.21.1
	github.com/omnibuf/omnibuf-go v0.2.5
	github.com/omnibuf/omnibuf-go/omnibufproto v0.0.0-00010101000000-000000000000
)

require google.golang.org/protobuf v1.36.12 // indirect
