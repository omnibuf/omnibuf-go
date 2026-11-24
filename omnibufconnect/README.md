# omnibufconnect

OmniBuf `connect.Codec` and `connect.StableCodec` for `connectrpc.com/connect/v2`.

## Install

```bash
go get github.com/omnibuf/omnibuf-go/omnibufconnect
```

## Usage

```go
package main

import (
	"net/http"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/omnibuf/omnibuf-go/omnibufconnect"
)

func setup(server *connect.Server) *connect.Client {
	// Server: accept both "omnibuf" and "x-omnibuf".
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server, omnibufconnect.ServerOptions()...)

	// Client: send compact "omnibuf" (or use SelfDescribingClientOptions() for "x-omnibuf").
	transport := connecthttp.NewTransport(
		http.DefaultClient,
		"https://api.example.com",
		omnibufconnect.ClientOptions()...,
	)
	return connect.NewClient(transport)
}
```
