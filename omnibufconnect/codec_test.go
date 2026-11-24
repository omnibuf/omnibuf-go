package omnibufconnect

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"connectrpc.com/connect"
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

func TestConnectUnaryRoundTrip_HTTP(t *testing.T) {
	const procedure = "/omnibuf.test.v1.PingService/Ping"

	var (
		mu              sync.Mutex
		seenContentType string
		seenBody        []byte
	)

	handler := connect.NewUnaryHandler(
		procedure,
		func(_ context.Context, req *connect.Request[pingRequest]) (*connect.Response[pingResponse], error) {
			return connect.NewResponse(&pingResponse{
				OK:      true,
				Message: "pong:" + req.Msg.RouterID,
				ASN:     req.Msg.ASN,
			}), nil
		},
		HandlerOptions()...,
	)

	mux := http.NewServeMux()
	mux.HandleFunc(procedure, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		mu.Lock()
		seenContentType = r.Header.Get("Content-Type")
		seenBody = append([]byte(nil), body...)
		mu.Unlock()
		r.Body = io.NopCloser(bytesReader(body))
		handler.ServeHTTP(w, r)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cases := []struct {
		name            string
		opt             connect.ClientOption
		wantContentType string
		wantField0      bool
	}{
		{
			name:            "compact",
			opt:             ClientOption(),
			wantContentType: "application/omnibuf",
			wantField0:      false,
		},
		{
			name:            "self-describing",
			opt:             SelfDescribingClientOption(),
			wantContentType: "application/x-omnibuf",
			wantField0:      true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := connect.NewClient[pingRequest, pingResponse](
				srv.Client(),
				srv.URL+procedure,
				tc.opt,
			)

			req := connect.NewRequest(&pingRequest{
				ASN:      64512,
				RouterID: "edge-rtr-fra-01",
				Tags:     map[string]string{"env": "prod"},
			})
			res, err := client.CallUnary(context.Background(), req)
			if err != nil {
				t.Fatalf("CallUnary (%s): %v", tc.name, err)
			}

			want := &pingResponse{
				OK:      true,
				Message: "pong:edge-rtr-fra-01",
				ASN:     64512,
			}
			if !reflect.DeepEqual(res.Msg, want) {
				t.Fatalf("res.Msg = %+v, want %+v", res.Msg, want)
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

type byteSliceReader struct {
	b []byte
	i int
}

func bytesReader(b []byte) io.Reader {
	return &byteSliceReader{b: b}
}

func (r *byteSliceReader) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}
