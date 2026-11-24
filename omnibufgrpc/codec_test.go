package omnibufgrpc

import (
	"context"
	"net"
	"reflect"
	"testing"

	"github.com/omnibuf/omnibuf-go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
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

type pingServer interface {
	Ping(context.Context, *pingRequest) (*pingResponse, error)
}

type pingServerImpl struct{}

func (pingServerImpl) Ping(_ context.Context, req *pingRequest) (*pingResponse, error) {
	return &pingResponse{
		OK:      true,
		Message: "pong:" + req.RouterID,
		ASN:     req.ASN,
	}, nil
}

func pingUnaryHandler(
	srv any,
	ctx context.Context,
	dec func(any) error,
	interceptor grpc.UnaryServerInterceptor,
) (any, error) {
	in := new(pingRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(pingServer).Ping(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/omnibuf.test.v1.PingService/Ping",
	}
	handler := func(ctx context.Context, req any) (any, error) {
		return srv.(pingServer).Ping(ctx, req.(*pingRequest))
	}
	return interceptor(ctx, in, info, handler)
}

var pingServiceDesc = grpc.ServiceDesc{
	ServiceName: "omnibuf.test.v1.PingService",
	HandlerType: (*pingServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Ping",
			Handler:    pingUnaryHandler,
		},
	},
}

func TestGRPCUnaryRoundTrip_Bufconn(t *testing.T) {
	Register()

	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	srv.RegisterService(&pingServiceDesc, pingServerImpl{})

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(lis)
	}()
	t.Cleanup(func() {
		srv.Stop()
		_ = lis.Close()
		<-errCh
	})

	dialer := func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}
	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	cases := []struct {
		name string
		opt  grpc.CallOption
	}{
		{name: "compact", opt: CallOption()},
		{name: "self-describing", opt: SelfDescribingCallOption()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &pingRequest{
				ASN:      64512,
				RouterID: "edge-rtr-fra-01",
				Tags:     map[string]string{"env": "prod", "region": "eu-central"},
			}
			var resp pingResponse
			err := conn.Invoke(
				context.Background(),
				"/omnibuf.test.v1.PingService/Ping",
				req,
				&resp,
				tc.opt,
			)
			if err != nil {
				t.Fatalf("conn.Invoke (%s): %v", tc.name, err)
			}
			want := pingResponse{
				OK:      true,
				Message: "pong:edge-rtr-fra-01",
				ASN:     64512,
			}
			if !reflect.DeepEqual(resp, want) {
				t.Fatalf("resp = %+v, want %+v", resp, want)
			}
		})
	}
}

func TestSelfDescribingCodecEmitsField0(t *testing.T) {
	c := NewSelfDescribing()
	if c.Name() != NameSelfDescribing {
		t.Fatalf("Name() = %q, want %q", c.Name(), NameSelfDescribing)
	}
	bufs, err := c.Marshal(&pingRequest{ASN: 65001, RouterID: "core-nyc-02"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	defer bufs.Free()

	blind, err := omnibuf.DecodeBlind(bufs.Materialize())
	if err != nil {
		t.Fatalf("DecodeBlind: %v", err)
	}
	if got := blind["router_id"]; got != "core-nyc-02" {
		t.Fatalf("blind[router_id] = %v, want %q", got, "core-nyc-02")
	}
}
