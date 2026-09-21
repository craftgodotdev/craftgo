package telemetry_test

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/craftgodotdev/craftgo/pkg/rpc"
	"github.com/craftgodotdev/craftgo/pkg/telemetry"
)

const pingMethod = "/test.Echo/Ping"

// pinger is the one-method service the tests register, hand-written the
// way protoc-gen-go-grpc would generate it.
type pinger interface {
	Ping(context.Context, *wrapperspb.StringValue) (*wrapperspb.StringValue, error)
}

type pingFunc func(context.Context, *wrapperspb.StringValue) (*wrapperspb.StringValue, error)

func (f pingFunc) Ping(ctx context.Context, in *wrapperspb.StringValue) (*wrapperspb.StringValue, error) {
	return f(ctx, in)
}

var echoDesc = grpc.ServiceDesc{
	ServiceName: "test.Echo",
	HandlerType: (*pinger)(nil),
	Methods: []grpc.MethodDesc{{
		MethodName: "Ping",
		Handler: func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
			in := new(wrapperspb.StringValue)
			if err := dec(in); err != nil {
				return nil, err
			}
			handler := func(ctx context.Context, req any) (any, error) {
				return srv.(pinger).Ping(ctx, req.(*wrapperspb.StringValue))
			}
			if interceptor == nil {
				return handler(ctx, in)
			}
			return interceptor(ctx, in, &grpc.UnaryServerInfo{Server: srv, FullMethod: pingMethod}, handler)
		},
	}},
	Metadata: "test.proto",
}

// serveGRPC serves impl behind tel's stats handler on an in-memory
// listener and returns a client connection.
func serveGRPC(t *testing.T, tel *telemetry.Telemetry, impl pinger) *grpc.ClientConn {
	t.Helper()
	srv := rpc.New(nil, rpc.WithStatsHandler(tel.GRPCServerHandler()))
	srv.RegisterService(&echoDesc, impl)
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Stop(ctx)
	})
	return conn
}

// The stats handler must put the caller's trace on the handler context
// and record the call against the stack's own meter.
func TestGRPCServerHandlerEmitsBothSignals(t *testing.T) {
	tel, err := telemetry.Init(context.Background(), telemetry.Config{
		ServiceName: "todo",
		OTel:        telemetry.OTelConfig{Enabled: true, Exporter: "none"},
		Metrics:     telemetry.MetricsConfig{Enabled: true, Exporter: "prometheus", AdminAddr: "127.0.0.1:0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tel.Shutdown(context.Background()) })
	var seen trace.SpanContext
	conn := serveGRPC(t, tel, pingFunc(func(ctx context.Context, in *wrapperspb.StringValue) (*wrapperspb.StringValue, error) {
		seen = trace.SpanContextFromContext(ctx)
		return in, nil
	}))
	const traceID = "0af7651916cd43dd8448eb211c80319c"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ctx = metadata.AppendToOutgoingContext(ctx, "traceparent", "00-"+traceID+"-b7ad6b7169203331-01")
	if err := conn.Invoke(ctx, pingMethod, wrapperspb.String("x"), new(wrapperspb.StringValue)); err != nil {
		t.Fatal(err)
	}
	if !seen.IsValid() {
		t.Fatal("no span on the handler context")
	}
	if seen.TraceID().String() != traceID {
		t.Errorf("trace id = %s, want the caller's", seen.TraceID())
	}
	// A probe is not a call worth a span or a series.
	if _, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatal(err)
	}
	body, _ := scrape(t, tel.ScrapeURL())
	if !strings.Contains(body, "rpc_server_call_duration_seconds_count") {
		t.Errorf("no gRPC instruments in the scrape:\n%s", body[:min(len(body), 400)])
	}
	if !strings.Contains(body, `rpc_method="test.Echo/Ping"`) || strings.Contains(body, "grpc.health.v1.Health") {
		t.Errorf("the scrape must hold the service and not the probe:\n%s", body)
	}
}

// With traces off the handler must not adopt a caller's trace, and the
// metrics must still flow.
func TestGRPCServerHandlerWithTracesOff(t *testing.T) {
	tel, err := telemetry.Init(context.Background(), telemetry.Config{
		ServiceName: "todo",
		Metrics:     telemetry.MetricsConfig{Enabled: true, Exporter: "prometheus", AdminAddr: "127.0.0.1:0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tel.Shutdown(context.Background()) })
	var seen trace.SpanContext
	conn := serveGRPC(t, tel, pingFunc(func(ctx context.Context, in *wrapperspb.StringValue) (*wrapperspb.StringValue, error) {
		seen = trace.SpanContextFromContext(ctx)
		return in, nil
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ctx = metadata.AppendToOutgoingContext(ctx, "traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	if err := conn.Invoke(ctx, pingMethod, wrapperspb.String("x"), new(wrapperspb.StringValue)); err != nil {
		t.Fatal(err)
	}
	if seen.IsValid() {
		t.Error("a stack without traces adopted the caller's span")
	}
	if body, _ := scrape(t, tel.ScrapeURL()); !strings.Contains(body, "rpc_server_call_duration_seconds_count") {
		t.Error("tracing off swallowed the gRPC metrics")
	}
}

// An unconfigured stack, and a nil one, must hand grpc a working handler.
func TestGRPCServerHandlerUnconfiguredIsPassThrough(t *testing.T) {
	tel, err := telemetry.Init(context.Background(), telemetry.Config{ServiceName: "todo"})
	if err != nil {
		t.Fatal(err)
	}
	var nilTel *telemetry.Telemetry
	if tel.GRPCServerHandler() == nil || nilTel.GRPCServerHandler() == nil {
		t.Fatal("the handler must never be nil")
	}
	conn := serveGRPC(t, tel, pingFunc(func(_ context.Context, in *wrapperspb.StringValue) (*wrapperspb.StringValue, error) {
		return in, nil
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out := new(wrapperspb.StringValue)
	if err := conn.Invoke(ctx, pingMethod, wrapperspb.String("x"), out); err != nil || out.GetValue() != "x" {
		t.Fatalf("call through the pass-through handler: %v %v", out, err)
	}
}
