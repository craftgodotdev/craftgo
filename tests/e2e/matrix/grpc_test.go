package matrix

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/craftgodotdev/craftgo/pkg/log"
	"github.com/craftgodotdev/craftgo/pkg/rpc"

	pb "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/pb/grpc"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/wiring"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/svccontext"
)

// bootGRPC serves the generated gRPC wiring over bufconn, behind access-log
// and timeout interceptors, and returns a client.
func bootGRPC(t *testing.T) pb.GreeterClient {
	t.Helper()
	svc := svccontext.NewServiceContext()
	srv := rpc.New(svc, rpc.WithReflection(true))
	srv.SetLogger(log.Discard())
	srv.Use(rpc.AccessLog(srv.Logger()))
	srv.Use(rpc.Timeout(200 * time.Millisecond))
	shutdown, err := wiring.RegisterGRPC(context.Background(), srv, svc)
	if err != nil {
		t.Fatal(err)
	}
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
		_ = shutdown(ctx)
	})
	return pb.NewGreeterClient(conn)
}

func callCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestGRPC_UnaryThroughTheGeneratedLayer(t *testing.T) {
	client := bootGRPC(t)
	reply, err := client.SayHello(callCtx(t), &pb.HelloRequest{Name: "matrix"})
	if err != nil || reply.GetMessage() != "hello matrix" {
		t.Fatalf("reply = %v, err = %v", reply, err)
	}
	if _, err := client.Ping(callCtx(t), &emptypb.Empty{}); err != nil {
		t.Errorf("ping: %v", err)
	}
}

// A typed error from logic reaches the client as the gRPC code for its HTTP
// status, with an ErrorInfo carrying its code and service.
func TestGRPC_TypedErrorsMapToStatusCodes(t *testing.T) {
	client := bootGRPC(t)
	_, err := client.SayHello(callCtx(t), &pb.HelloRequest{Name: "missing"})
	st := status.Convert(err)
	if st.Code() != codes.NotFound {
		t.Fatalf("code = %v (%v)", st.Code(), err)
	}
	if len(st.Details()) != 1 {
		t.Fatalf("details = %v", st.Details())
	}
	info, ok := st.Details()[0].(*errdetails.ErrorInfo)
	if !ok || info.GetReason() != "ACCT_USER_NOT_FOUND" || info.GetDomain() != "grpcmatrix.Greeter" {
		t.Errorf("detail = %v", st.Details()[0])
	}
}

func TestGRPC_PanicsAndDeadlinesAreGuarded(t *testing.T) {
	client := bootGRPC(t)
	_, err := client.SayHello(callCtx(t), &pb.HelloRequest{Name: "panic"})
	if status.Code(err) != codes.Internal || status.Convert(err).Message() != "internal server error" {
		t.Errorf("panic → %v", err)
	}
	_, err = client.SayHello(callCtx(t), &pb.HelloRequest{Name: "slow"})
	if status.Code(err) != codes.DeadlineExceeded {
		t.Errorf("slow → %v", err)
	}
	// The server keeps serving after both.
	if _, err := client.SayHello(callCtx(t), &pb.HelloRequest{Name: "again"}); err != nil {
		t.Errorf("after the guards: %v", err)
	}
}

func TestGRPC_ServerStreaming(t *testing.T) {
	client := bootGRPC(t)
	stream, err := client.ListHellos(callCtx(t), &pb.HelloRequest{Name: "m", Count: 3})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for {
		reply, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, reply.GetMessage())
	}
	if fmt.Sprint(got) != "[hello m #1 hello m #2 hello m #3]" {
		t.Errorf("got %v", got)
	}
}

func TestGRPC_ClientStreaming(t *testing.T) {
	client := bootGRPC(t)
	stream, err := client.RecordHellos(callCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if err := stream.Send(&pb.HelloRequest{Name: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	reply, err := stream.CloseAndRecv()
	if err != nil || reply.GetMessage() != "recorded 4" {
		t.Fatalf("reply = %v, err = %v", reply, err)
	}
}

func TestGRPC_BidiStreaming(t *testing.T) {
	client := bootGRPC(t)
	stream, err := client.Chat(callCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b"} {
		if err := stream.Send(&pb.HelloRequest{Name: name}); err != nil {
			t.Fatal(err)
		}
		reply, err := stream.Recv()
		if err != nil || reply.GetMessage() != "echo "+name {
			t.Fatalf("reply = %v, err = %v", reply, err)
		}
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Errorf("after CloseSend: %v", err)
	}
}

// The runtime's health service reports the registered Greeter as SERVING.
func TestGRPC_HealthReportsTheRegisteredService(t *testing.T) {
	svc := svccontext.NewServiceContext()
	srv := rpc.New(svc)
	srv.SetLogger(log.Discard())
	if _, err := wiring.RegisterGRPC(context.Background(), srv, svc); err != nil {
		t.Fatal(err)
	}
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	resp, err := healthpb.NewHealthClient(conn).Check(callCtx(t), &healthpb.HealthCheckRequest{Service: "grpcmatrix.Greeter"})
	if err != nil || resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("health = %v, %v", resp, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Stop(ctx); err != nil {
		t.Errorf("stop: %v", err)
	}
}
