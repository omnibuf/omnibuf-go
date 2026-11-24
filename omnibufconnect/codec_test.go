package omnibufconnect

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/omnibuf/omnibuf-go"
)

type pingRequest struct {
	ASN      uint32            `omnibuf:"1,name=asn"`
	RouterID string            `omnibuf:"2,name=router_id"`
	Tags     map[string]string `omnibuf:"3,name=tags"`
}

type pingResponse struct {
	OK      bool   `omnibuf:"1,name=ok"`
	Message string `omnibuf:"2,name=message"`
	ASN     uint32 `omnibuf:"3,name=asn"`
}

func TestConnectV2UnaryRoundTrip_HTTP(t *testing.T) {
	const procedure = "/omnibuf.test.v1.PingService/Ping"

	var (
		mu              sync.Mutex
		seenContentType string
		seenBody        []byte
	)

	spec := connect.Spec{
		StreamType: connect.StreamTypeUnary,
		Procedure:  procedure,
	}

	server := connect.NewServer()
	server.Register(connect.Method{
		Spec: spec,
		Handler: func(_ context.Context, _ connect.Spec, stream connect.ServerStream) error {
			var req pingRequest
			if err := stream.Receive(&req); err != nil {
				return err
			}
			return stream.Send(&pingResponse{
				OK:      true,
				Message: "pong:" + req.RouterID,
				ASN:     req.ASN,
			})
		},
	})

	connectMux := http.NewServeMux()
	connecthttp.Mount(connectMux, server, ServerOptions()...)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		mu.Lock()
		seenContentType = r.Header.Get("Content-Type")
		seenBody = append([]byte(nil), body...)
		mu.Unlock()
		r.Body = io.NopCloser(bytes.NewReader(body))
		connectMux.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	cases := []struct {
		name            string
		opts            []connecthttp.Option
		wantContentType string
		wantField0      bool
	}{
		{
			name:            "compact",
			opts:            ClientOptions(),
			wantContentType: "application/omnibuf",
			wantField0:      false,
		},
		{
			name:            "self-describing",
			opts:            SelfDescribingClientOptions(),
			wantContentType: "application/x-omnibuf",
			wantField0:      true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transport := connecthttp.NewTransport(srv.Client(), srv.URL, tc.opts...)
			client := connect.NewClient(transport)

			req := &pingRequest{
				ASN:      64512,
				RouterID: "edge-rtr-fra-01",
				Tags:     map[string]string{"env": "prod"},
			}
			var res pingResponse
			if err := client.CallUnary(context.Background(), spec, req, &res); err != nil {
				t.Fatalf("CallUnary (%s): %v", tc.name, err)
			}

			want := pingResponse{
				OK:      true,
				Message: "pong:edge-rtr-fra-01",
				ASN:     64512,
			}
			if !reflect.DeepEqual(res, want) {
				t.Fatalf("res = %+v, want %+v", res, want)
			}

			mu.Lock()
			gotCT := seenContentType
			gotBody := seenBody
			mu.Unlock()

			if gotCT != tc.wantContentType {
				t.Fatalf("Content-Type = %q, want %q", gotCT, tc.wantContentType)
			}
			if tc.wantField0 {
				blind, err := omnibuf.DecodeBlind(gotBody)
				if err != nil {
					t.Fatalf("DecodeBlind request body: %v", err)
				}
				if got := blind["router_id"]; got != "edge-rtr-fra-01" {
					t.Fatalf("blind[router_id] = %v, want %q", got, "edge-rtr-fra-01")
				}
			}
		})
	}
}

func TestMarshalWriteStableDeterministic(t *testing.T) {
	c := New()
	msg := &pingRequest{
		ASN:      64512,
		RouterID: "edge-rtr-fra-01",
		Tags:     map[string]string{"z": "last", "a": "first", "m": "mid"},
	}

	var first bytes.Buffer
	if err := c.MarshalWriteStable(context.Background(), &first, msg); err != nil {
		t.Fatalf("MarshalWriteStable: %v", err)
	}
	for i := range 20 {
		var next bytes.Buffer
		if err := c.MarshalWriteStable(context.Background(), &next, msg); err != nil {
			t.Fatalf("MarshalWriteStable iter %d: %v", i, err)
		}
		if !bytes.Equal(first.Bytes(), next.Bytes()) {
			t.Fatalf("non-deterministic output on iter %d", i)
		}
	}
}
