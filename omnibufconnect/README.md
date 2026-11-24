# omnibufconnect

OmniBuf `connect.Codec` for `connectrpc.com/connect`.

## Install

```bash
go get github.com/omnibuf/omnibuf-go/omnibufconnect
```

## Usage

```go
package main

import (
	"net/http"

	"connectrpc.com/connect"
	"github.com/omnibuf/omnibuf-go/omnibufconnect"
)

func setup(newHandler func(...connect.HandlerOption) (string, http.Handler)) {
	// Server: accept both "omnibuf" and "x-omnibuf".
	path, handler := newHandler(omnibufconnect.HandlerOptions()...)
	http.Handle(path, handler)

	// Client: pass omnibufconnect.ClientOption() or omnibufconnect.SelfDescribingClientOption().
	_ = omnibufconnect.ClientOption()
}
```
