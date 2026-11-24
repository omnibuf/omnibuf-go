# omnibufproto

OmniBuf binary codec for standard `google.golang.org/protobuf` (`proto.Message`) types.

## Install

```bash
go get github.com/omnibuf/omnibuf-go/omnibufproto
```

## Usage

```go
package main

import (
	"fmt"

	"github.com/omnibuf/omnibuf-go/omnibufproto"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func main() {
	in := wrapperspb.String("hello omnibuf")

	wire, err := omnibufproto.Marshal(in)
	if err != nil {
		panic(err)
	}

	var out wrapperspb.StringValue
	if err := omnibufproto.Unmarshal(wire, &out); err != nil {
		panic(err)
	}

	fmt.Println(out.GetValue())
}
```

Use `omnibufproto.MarshalOptions{SelfDescribing: true}` to emit Field #0 schema metadata inline, or `omnibufproto.MarshalDeterministic` for deterministic map key ordering.
