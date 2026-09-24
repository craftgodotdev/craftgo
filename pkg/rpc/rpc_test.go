package rpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/craftgodotdev/craftgo/internal/errcat"
	"github.com/craftgodotdev/craftgo/pkg/log"
)

// entry is one captured log line.
type entry struct {
	msg    string
	fields map[string]any
	ctx    context.Context
}

// capture is a log.Logger that records every line for assertions.
type capture struct {
	mu      *sync.Mutex
	entries *[]entry
	ctx     context.Context
}

func newCapture() *capture {
	return &capture{mu: &sync.Mutex{}, entries: &[]entry{}, ctx: context.Background()}
}

func (c *capture) record(msg string, fields []log.Field) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := map[string]any{}
	for _, f := range fields {
		m[f.Key] = f.Value
	}
	*c.entries = append(*c.entries, entry{msg: msg, fields: m, ctx: c.ctx})
}

func (c *capture) Debug(msg string, fields ...log.Field) { c.record(msg, fields) }
func (c *capture) Info(msg string, fields ...log.Field)  { c.record(msg, fields) }
func (c *capture) Warn(msg string, fields ...log.Field)  { c.record(msg, fields) }
func (c *capture) Error(msg string, fields ...log.Field) { c.record(msg, fields) }
func (c *capture) With(...log.Field) log.Logger          { return c }
func (c *capture) WithContext(ctx context.Context) log.Logger {
	return &capture{mu: c.mu, entries: c.entries, ctx: ctx}
}
func (c *capture) Enabled(log.Level) bool { return true }

func (c *capture) lines(msg string) []entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []entry
	for _, e := range *c.entries {
		if e.msg == msg {
			out = append(out, e)
		}
	}
	return out
}

// echoServer is the service the tests register: a unary Ping and a
// server-streaming Count.
type echoServer interface {
	Ping(context.Context, *wrapperspb.StringValue) (*wrapperspb.StringValue, error)
	Count(*wrapperspb.StringValue, grpc.ServerStream) error
}

type echo struct {
	ping  func(context.Context, *wrapperspb.StringValue) (*wrapperspb.StringValue, error)
	count func(*wrapperspb.StringValue, grpc.ServerStream) error
}

func (e *echo) Ping(ctx context.Context, in *wrapperspb.StringValue) (*wrapperspb.StringValue, error) {
	return e.ping(ctx, in)
}

func (e *echo) Count(in *wrapperspb.StringValue, ss grpc.ServerStream) error { return e.count(in, ss) }

const pingMethod, countMethod = "/test.Echo/Ping", "/test.Echo/Count"

var echoDesc = grpc.ServiceDesc{
	ServiceName: "test.Echo",
	HandlerType: (*echoServer)(nil),
	Methods: []grpc.MethodDesc{{
		MethodName: "Ping",
		Handler: func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
			in := new(wrapperspb.StringValue)
			if err := dec(in); err != nil {
				return nil, err
			}
			handler := func(ctx context.Context, req any) (any, error) {
				return srv.(echoServer).Ping(ctx, req.(*wrapperspb.StringValue))
			}
			if interceptor == nil {
				return handler(ctx, in)
			}
			return interceptor(ctx, in, &grpc.UnaryServerInfo{Server: srv, FullMethod: pingMethod}, handler)
		},
	}},
	Streams: []grpc.StreamDesc{{
		StreamName:    "Count",
		ServerStreams: true,
		Handler: func(srv any, ss grpc.ServerStream) error {
			in := new(wrapperspb.StringValue)
			if err := ss.RecvMsg(in); err != nil {
				return err
			}
			return srv.(echoServer).Count(in, ss)
		},
	}},
	Metadata: "test.proto",
}

// serve registers impl on srv, serves it in memory and returns a client connection.
func serve(t *testing.T, srv *Server, impl echoServer) *grpc.ClientConn {
	t.Helper()
	srv.RegisterService(&echoDesc, impl)
	lis := bufconn.Listen(1 << 20)
	served := make(chan error, 1)
	go func() { served <- srv.Serve(lis) }()
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
		if err := srv.Stop(ctx); err != nil {
			t.Errorf("stop: %v", err)
		}
		if err := <-served; err != nil {
			t.Errorf("serve: %v", err)
		}
	})
	return conn
}

func ping(conn *grpc.ClientConn, text string) (*wrapperspb.StringValue, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out := new(wrapperspb.StringValue)
	if err := conn.Invoke(ctx, pingMethod, wrapperspb.String(text), out); err != nil {
		return nil, err
	}
	return out, nil
}

func pong(_ context.Context, in *wrapperspb.StringValue) (*wrapperspb.StringValue, error) {
	return wrapperspb.String(in.GetValue() + " pong"), nil
}

func TestUnaryChainLogsRecoversAndOrders(t *testing.T) {
	logs := newCapture()
	var order []string
	srv := New(nil).SetLogger(logs)
	srv.Use(Unary(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		order = append(order, "first")
		return handler(ctx, req)
	}))
	srv.Use(AccessLog(logs))
	srv.Use(Unary(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		order = append(order, "second")
		return handler(ctx, req)
	}))
	conn := serve(t, srv, &echo{ping: func(ctx context.Context, in *wrapperspb.StringValue) (*wrapperspb.StringValue, error) {
		if in.GetValue() == "boom" {
			panic("kaboom")
		}
		return pong(ctx, in)
	}})

	out, err := ping(conn, "ping")
	if err != nil || out.GetValue() != "ping pong" {
		t.Fatalf("out = %v, err = %v", out, err)
	}
	if fmt.Sprint(order) != "[first second]" {
		t.Errorf("order = %v", order)
	}
	access := logs.lines("grpc access")
	if len(access) != 1 || access[0].fields["method"] != pingMethod || access[0].fields["code"] != "OK" {
		t.Fatalf("access = %+v", access)
	}
	if _, ok := access[0].fields["latency"].(time.Duration); !ok {
		t.Errorf("latency = %T", access[0].fields["latency"])
	}
	if m, ok := grpc.Method(access[0].ctx); !ok || m != pingMethod {
		t.Errorf("the log line must carry the call context, got %q", m)
	}

	_, err = ping(conn, "boom")
	if status.Code(err) != codes.Internal || status.Convert(err).Message() != "internal server error" {
		t.Fatalf("panic answered %v", err)
	}
	if got := logs.lines("panic recovered"); len(got) != 1 || got[0].fields["panic"] != "kaboom" || got[0].fields["stack"] == "" {
		t.Errorf("recovery log = %+v", got)
	}
	// A panic leaves no access line; the recovery line is the record.
	if access := logs.lines("grpc access"); len(access) != 1 {
		t.Errorf("access after panic = %+v", access)
	}
}

// notFoundErr is the shape of a craftgo typed error.
type notFoundErr struct{ id string }

func (e *notFoundErr) Error() string   { return "todo " + e.id + " not found" }
func (e *notFoundErr) HTTPStatus() int { return 404 }
func (e *notFoundErr) ErrCode() string { return "TODO_NOT_FOUND" }

func TestErrorMapsServiceErrorsLikeWriteError(t *testing.T) {
	logs := newCapture()
	log.SetDefault(logs)
	t.Cleanup(func() { log.SetDefault(log.New()) })
	ctx := context.Background()
	if Error(ctx, nil) != nil {
		t.Error("nil stays nil")
	}
	pre := status.Error(codes.AlreadyExists, "dup")
	if Error(ctx, pre) != pre { //nolint:errorlint // identity is the contract
		t.Error("a status error passes through untouched")
	}
	wrapped := fmt.Errorf("lookup: %w", &notFoundErr{id: "7"})
	st := status.Convert(Error(ctx, wrapped))
	// The message is the typed error's own text.
	if st.Code() != codes.NotFound || st.Message() != "todo 7 not found" {
		t.Errorf("typed error → %v", st)
	}
	if len(st.Details()) != 1 {
		t.Fatalf("details = %v", st.Details())
	}
	info, ok := st.Details()[0].(*errdetails.ErrorInfo)
	if !ok || info.GetReason() != "TODO_NOT_FOUND" || info.GetDomain() != "" {
		t.Errorf("detail = %v", st.Details()[0])
	}
	if got := status.Code(Error(ctx, context.DeadlineExceeded)); got != codes.DeadlineExceeded {
		t.Errorf("deadline → %v", got)
	}
	if got := status.Code(Error(ctx, fmt.Errorf("op: %w", context.Canceled))); got != codes.Canceled {
		t.Errorf("canceled → %v", got)
	}
	unknown := Error(ctx, errors.New("dsn=postgres://secret"))
	if status.Code(unknown) != codes.Internal || status.Convert(unknown).Message() != "internal server error" {
		t.Errorf("unknown → %v", unknown)
	}
	if got := logs.lines("unhandled service error"); len(got) != 1 || fmt.Sprint(got[0].fields["error"]) != "dsn=postgres://secret" {
		t.Errorf("unknown error log = %+v", got)
	}
	SetHandleUnknownError(func(context.Context, error) error { return status.Error(codes.Aborted, "custom") })
	t.Cleanup(func() { SetHandleUnknownError(nil) })
	if got := status.Code(Error(ctx, errors.New("x"))); got != codes.Aborted {
		t.Errorf("hook → %v", got)
	}
}

func TestErrorDomainIsTheCalledService(t *testing.T) {
	srv := New(nil).SetLogger(newCapture())
	conn := serve(t, srv, &echo{ping: func(ctx context.Context, _ *wrapperspb.StringValue) (*wrapperspb.StringValue, error) {
		return nil, Error(ctx, &notFoundErr{id: "1"})
	}})
	_, err := ping(conn, "x")
	st := status.Convert(err)
	if st.Code() != codes.NotFound {
		t.Fatalf("code = %v", st.Code())
	}
	info, ok := st.Details()[0].(*errdetails.ErrorInfo)
	if !ok || info.GetDomain() != "test.Echo" {
		t.Errorf("detail = %v", st.Details())
	}
}

func TestCodeTableCoversTheErrorCatalogue(t *testing.T) {
	for _, c := range errcat.Categories {
		if codeFor(c.Status) == codes.Unknown {
			t.Errorf("%s (%d) has no gRPC code", c.Name, c.Status)
		}
	}
	for status, want := range map[int]codes.Code{400: codes.InvalidArgument, 401: codes.Unauthenticated, 403: codes.PermissionDenied, 404: codes.NotFound, 409: codes.AlreadyExists, 429: codes.ResourceExhausted, 500: codes.Internal, 501: codes.Unimplemented, 503: codes.Unavailable, 504: codes.DeadlineExceeded, 418: codes.Unknown} {
		if got := codeFor(status); got != want {
			t.Errorf("%d → %v, want %v", status, got, want)
		}
	}
}

func TestTimeoutBoundsUnaryCalls(t *testing.T) {
	if got := Timeout(0); got.Unary != nil || got.Stream != nil {
		t.Error("a zero timeout installs nothing")
	}
	if got := Timeout(time.Second); got.Unary == nil || got.Stream != nil {
		t.Error("a timeout bounds unary calls only")
	}
	srv := New(nil).SetLogger(newCapture())
	srv.Use(Timeout(30 * time.Millisecond))
	conn := serve(t, srv, &echo{ping: func(ctx context.Context, in *wrapperspb.StringValue) (*wrapperspb.StringValue, error) {
		switch in.GetValue() {
		case "honour":
			<-ctx.Done()
			return nil, Error(ctx, ctx.Err())
		case "ignore":
			time.Sleep(80 * time.Millisecond)
			return wrapperspb.String("late"), nil
		}
		return pong(ctx, in)
	}})
	if out, err := ping(conn, "fast"); err != nil || out.GetValue() != "fast pong" {
		t.Errorf("fast call: %v %v", out, err)
	}
	for _, text := range []string{"honour", "ignore"} {
		if _, err := ping(conn, text); status.Code(err) != codes.DeadlineExceeded {
			t.Errorf("%s: %v", text, err)
		}
	}
}

func TestStreamRecoveryAndAccessLog(t *testing.T) {
	logs := newCapture()
	srv := New(nil).SetLogger(logs)
	srv.Use(AccessLog(logs))
	conn := serve(t, srv, &echo{count: func(in *wrapperspb.StringValue, ss grpc.ServerStream) error {
		for i := 0; i < 2; i++ {
			if err := ss.SendMsg(wrapperspb.String(fmt.Sprintf("%s-%d", in.GetValue(), i))); err != nil {
				return err
			}
		}
		if in.GetValue() == "panic" {
			panic("mid-stream")
		}
		return status.Error(codes.Aborted, "enough")
	}})
	// count sends text to Count and returns the messages and the final status.
	count := func(text string) ([]string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		stream, err := conn.NewStream(ctx, &grpc.StreamDesc{StreamName: "Count", ServerStreams: true}, countMethod)
		if err != nil {
			t.Fatal(err)
		}
		if err := stream.SendMsg(wrapperspb.String(text)); err != nil {
			t.Fatal(err)
		}
		if err := stream.CloseSend(); err != nil {
			t.Fatal(err)
		}
		var got []string
		for {
			m := new(wrapperspb.StringValue)
			err := stream.RecvMsg(m)
			if err == nil {
				got = append(got, m.GetValue())
				continue
			}
			if errors.Is(err, io.EOF) {
				return got, nil
			}
			return got, err
		}
	}
	got, err := count("panic")
	if status.Code(err) != codes.Internal || fmt.Sprint(got) != "[panic-0 panic-1]" {
		t.Errorf("panicking stream: %v %v", got, err)
	}
	if len(logs.lines("panic recovered")) != 1 {
		t.Error("stream panic not logged")
	}
	got, err = count("n")
	if status.Code(err) != codes.Aborted || fmt.Sprint(got) != "[n-0 n-1]" {
		t.Errorf("aborted stream: %v %v", got, err)
	}
	if access := logs.lines("grpc access"); len(access) != 1 || access[0].fields["method"] != countMethod || access[0].fields["code"] != "Aborted" {
		t.Errorf("access = %+v", access)
	}
}

func TestInfrastructureMethodsBypassTheChain(t *testing.T) {
	logs := newCapture()
	srv := New(nil, WithReflection(true)).SetLogger(logs)
	srv.Use(AccessLog(logs))
	conn := serve(t, srv, &echo{ping: pong})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, name := range []string{"", "test.Echo"} {
		resp, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{Service: name})
		if err != nil || resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
			t.Errorf("health %q = %v, %v", name, resp, err)
		}
	}
	if lines := logs.lines("grpc access"); len(lines) != 0 {
		t.Errorf("probes must not be logged: %+v", lines)
	}
	if _, ok := srv.GRPCServer().GetServiceInfo()["grpc.reflection.v1.ServerReflection"]; !ok {
		t.Error("reflection not registered")
	}
	for method, want := range map[string]bool{"/grpc.health.v1.Health/Check": true, "/grpc.reflection.v1.ServerReflection/ServerReflectionInfo": true, pingMethod: false} {
		if got := IsInfrastructureMethod(method); got != want {
			t.Errorf("IsInfrastructureMethod(%q) = %v", method, got)
		}
	}
}

// An option the caller passes reaches grpc.NewServer.
func TestWithServerOptionsReachGRPC(t *testing.T) {
	srv := New(nil, WithServerOptions(grpc.MaxRecvMsgSize(1))).SetLogger(newCapture())
	conn := serve(t, srv, &echo{ping: pong})
	if _, err := ping(conn, "a request larger than one byte"); status.Code(err) != codes.ResourceExhausted {
		t.Errorf("the server option did not reach grpc: %v", err)
	}
}

func TestWithoutDefaultHealth(t *testing.T) {
	srv := New(nil, WithoutDefaultHealth())
	srv.RegisterService(&echoDesc, &echo{})
	if _, ok := srv.GRPCServer().GetServiceInfo()["grpc.health.v1.Health"]; ok {
		t.Error("health registered despite the option")
	}
}

type validated struct {
	*wrapperspb.StringValue
	err error
}

func (v validated) Validate() error { return v.err }

func TestValidate(t *testing.T) {
	if err := Validate(wrapperspb.String("plain")); err != nil {
		t.Errorf("no Validate method: %v", err)
	}
	if err := Validate(validated{}); err != nil {
		t.Errorf("valid: %v", err)
	}
	err := Validate(validated{err: errors.New("name: must not be empty")})
	if status.Code(err) != codes.InvalidArgument || status.Convert(err).Message() != "name: must not be empty" {
		t.Errorf("invalid → %v", err)
	}
}

func TestStopCutsOffAfterTheDeadline(t *testing.T) {
	if err := New(nil).Stop(context.Background()); err != nil {
		t.Errorf("stop before serve: %v", err)
	}
	srv := New(nil).SetLogger(newCapture())
	srv.RegisterService(&echoDesc, &echo{ping: func(ctx context.Context, _ *wrapperspb.StringValue) (*wrapperspb.StringValue, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}})
	lis := bufconn.Listen(1 << 20)
	served := make(chan error, 1)
	go func() { served <- srv.Serve(lis) }()
	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	inflight := make(chan error, 1)
	go func() {
		_, err := ping(conn, "hang")
		inflight <- err
	}()
	time.Sleep(50 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := srv.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("stop = %v, want the deadline", err)
	}
	if err := <-served; err != nil {
		t.Errorf("serve = %v", err)
	}
	if err := <-inflight; err == nil {
		t.Error("the hung call must fail once the server is cut off")
	}
	// Stop set the health service NOT_SERVING.
	resp, err := srv.health.Check(context.Background(), &healthpb.HealthCheckRequest{Service: "test.Echo"})
	if err != nil || resp.GetStatus() != healthpb.HealthCheckResponse_NOT_SERVING {
		t.Errorf("health after Stop = %v, %v", resp, err)
	}
}

func TestRegisterServiceAfterBuild(t *testing.T) {
	srv := New(nil)
	inner := srv.GRPCServer()
	srv.RegisterService(&echoDesc, &echo{})
	if _, ok := inner.GetServiceInfo()["test.Echo"]; !ok {
		t.Error("late registration must reach the built server")
	}
	if srv.GRPCServer() != inner {
		t.Error("GRPCServer must build once")
	}
	resp, err := srv.health.Check(context.Background(), &healthpb.HealthCheckRequest{Service: "test.Echo"})
	if err != nil || resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Errorf("a late registration must be SERVING: %v, %v", resp, err)
	}
}

// Serve after Stop returns nil.
func TestServeAfterStopReturnsNil(t *testing.T) {
	srv := New(nil).SetLogger(newCapture())
	if err := srv.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv.GRPCServer().Stop()
	if err := srv.Serve(bufconn.Listen(1 << 20)); err != nil {
		t.Errorf("serve after stop = %v", err)
	}
}
