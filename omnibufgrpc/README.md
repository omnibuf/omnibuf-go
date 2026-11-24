# omnibufgrpc

OmniBuf `encoding.CodecV2` for `google.golang.org/grpc`.

## Install

```bash
go get github.com/omnibuf/omnibuf-go/omnibufgrpc
```

## Usage

```go
package main

import (
	"context"

	"github.com/omnibuf/omnibuf-go/omnibufgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	// Register both "omnibuf" and "x-omnibuf" codecs on server and client.
	omnibufgrpc.Register()

	conn, err := grpc.NewClient(
		"127.0.0.1:50051",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(omnibufgrpc.CallOption()),
	)
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	_ = context.Background()
}
```

Use `omnibufgrpc.SelfDescribingCallOption()` to emit Field #0 schema metadata inline (`application/grpc+x-omnibuf`).
