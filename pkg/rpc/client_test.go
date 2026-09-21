package rpc

import (
	"context"
	"crypto/tls"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// listen serves impl on an in-memory listener and returns the dial
// option that reaches it, so the tests exercise the real [Dial].
func listen(t *testing.T, impl echoServer) ClientOption {
	t.Helper()
	srv := New(nil).SetLogger(newCapture())
	srv.RegisterService(&echoDesc, impl)
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Stop(ctx)
	})
	return WithDialOptions(grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}))
}

// invoke calls Ping over conn under ctx.
func invoke(ctx context.Context, conn *grpc.ClientConn, text string) (string, error) {
	out := new(wrapperspb.StringValue)
	if err := conn.Invoke(ctx, pingMethod, wrapperspb.String(text), out); err != nil {
		return "", err
	}
	return out.GetValue(), nil
}

// A dialed connection talks to the server on the framework defaults
// alone, and a nil stats handler is dropped rather than handed to grpc.
func TestDialDefaults(t *testing.T) {
	conn, err := Dial("passthrough:///bufconn", listen(t, &echo{ping: pong}), WithClientStatsHandler(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if got, err := invoke(ctx, conn, "ping"); err != nil || got != "ping pong" {
		t.Fatalf("got %q, err %v", got, err)
	}
}

// The default deadline bounds a call that carries none, and a caller
// that sets its own keeps it - longer or shorter.
func TestDialTimeoutOnlyFillsAGap(t *testing.T) {
	dialer := listen(t, &echo{ping: func(ctx context.Context, in *wrapperspb.StringValue) (*wrapperspb.StringValue, error) {
		if in.GetValue() == "slow" {
			time.Sleep(120 * time.Millisecond)
		}
		return pong(ctx, in)
	}})
	conn, err := Dial("passthrough:///bufconn", dialer, WithClientTimeout(40*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if _, err := invoke(context.Background(), conn, "slow"); status.Code(err) != codes.DeadlineExceeded {
		t.Errorf("the default deadline did not bound the call: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if got, err := invoke(ctx, conn, "slow"); err != nil || got != "slow pong" {
		t.Errorf("the caller's own deadline must win: %q %v", got, err)
	}
}

func TestDialAccessLog(t *testing.T) {
	logs := newCapture()
	conn, err := Dial("passthrough:///bufconn", listen(t, &echo{ping: func(ctx context.Context, in *wrapperspb.StringValue) (*wrapperspb.StringValue, error) {
		if in.GetValue() == "fail" {
			return nil, status.Error(codes.NotFound, "nope")
		}
		return pong(ctx, in)
	}}), WithClientAccessLog(logs))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := invoke(ctx, conn, "ping"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(ctx, conn, "fail"); status.Code(err) != codes.NotFound {
		t.Fatal(err)
	}
	lines := logs.lines("grpc client")
	if len(lines) != 2 {
		t.Fatalf("lines = %+v", lines)
	}
	if lines[0].fields["method"] != pingMethod || lines[0].fields["code"] != "OK" {
		t.Errorf("ok line = %+v", lines[0].fields)
	}
	if lines[1].fields["code"] != "NotFound" {
		t.Errorf("failed line = %+v", lines[1].fields)
	}
	if _, ok := lines[0].fields["latency"].(time.Duration); !ok {
		t.Errorf("latency = %T", lines[0].fields["latency"])
	}
}

// The escape hatches reach grpc: transport security replaces the
// insecure default, and a dial option lands on the connection.
func TestDialEscapeHatches(t *testing.T) {
	dialer := listen(t, &echo{ping: pong})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// TLS against a plaintext server cannot handshake; the insecure
	// default can, so the contrast is the option taking effect.
	tlsConn, err := Dial("passthrough:///bufconn", dialer,
		WithClientTransportCredentials(credentials.NewTLS(&tls.Config{InsecureSkipVerify: true})))
	if err != nil {
		t.Fatal(err)
	}
	defer tlsConn.Close()
	if _, err := invoke(ctx, tlsConn, "ping"); err == nil {
		t.Error("a TLS client reached a plaintext server, so the credentials were dropped")
	}

	// A dial option the caller passes is honoured: one byte is not
	// enough for the reply.
	capped, err := Dial("passthrough:///bufconn", dialer, WithDialOptions(grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(1))))
	if err != nil {
		t.Fatal(err)
	}
	defer capped.Close()
	if _, err := invoke(ctx, capped, "ping"); status.Code(err) != codes.ResourceExhausted {
		t.Errorf("the dial option did not reach the connection: %v", err)
	}
}

// Dial connects lazily, so an unreachable target is a failed call and
// not a failed startup - a service may be dialed before the one it
// calls is up.
func TestDialDoesNotConnectEagerly(t *testing.T) {
	conn, err := Dial("127.0.0.1:1")
	if err != nil {
		t.Fatalf("dial = %v", err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if _, err := invoke(ctx, conn, "ping"); status.Code(err) != codes.Unavailable && status.Code(err) != codes.DeadlineExceeded {
		t.Errorf("call to a closed port = %v", err)
	}
}
