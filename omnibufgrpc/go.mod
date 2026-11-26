module github.com/omnibuf/omnibuf-go/omnibufgrpc

go 1.26.0

replace (
	github.com/omnibuf/omnibuf-go => ..
	github.com/omnibuf/omnibuf-go/omnibufproto => ../omnibufproto
)

require (
	github.com/omnibuf/omnibuf-go v0.2.11
	github.com/omnibuf/omnibuf-go/omnibufproto v0.0.0-00010101000000-000000000000
	google.golang.org/grpc v1.85.0
)

require (
	golang.org/x/net v0.60.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260817212433-ac3dfec99bb1 // indirect
	google.golang.org/protobuf v1.37.0-rc.1 // indirect
)
