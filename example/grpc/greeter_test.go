package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/craftgodotdev/craftgo/pkg/log"
	"github.com/craftgodotdev/craftgo/pkg/rpc"

	"github.com/craftgodotdev/craftgo/example/grpc/config"
	pb "github.com/craftgodotdev/craftgo/example/grpc/internal/pb/greet"
	"github.com/craftgodotdev/craftgo/example/grpc/internal/wiring"
	"github.com/craftgodotdev/craftgo/example/grpc/svccontext"
)

// boot serves the generated wiring on an in-memory listener, the way
// main.go serves it on cfg.GRPC.Addr, and returns a client for it.
func boot(t *testing.T) pb.GreeterClient {
	t.Helper()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	svc := svccontext.NewServiceContext(cfg)
	srv := rpc.New(svc, rpc.WithReflection(cfg.GRPC.Reflection))
	srv.SetLogger(log.Discard())
	srv.Use(rpc.AccessLog(log.Follow()))
	srv.Use(rpc.Timeout(cfg.GRPC.HandlerTimeout))
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

func TestGreeter(t *testing.T) {
	client := boot(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	reply, err := client.SayHello(ctx, &pb.HelloRequest{Name: "craftgo"})
	if err != nil || reply.GetMessage() != "hello craftgo" {
		t.Fatalf("SayHello = %v, %v", reply, err)
	}
	if _, err := client.SayHello(ctx, &pb.HelloRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("empty name = %v", err)
	}

	list, err := client.ListHellos(ctx, &pb.HelloRequest{Name: "x", Count: 2})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for {
		r, err := list.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, r.GetMessage())
	}
	if fmt.Sprint(got) != "[hello x #1 hello x #2]" {
		t.Errorf("ListHellos = %v", got)
	}

	record, err := client.RecordHellos(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := record.Send(&pb.HelloRequest{Name: "y"}); err != nil {
			t.Fatal(err)
		}
	}
	if r, err := record.CloseAndRecv(); err != nil || r.GetMessage() != "recorded 3 greetings" {
		t.Errorf("RecordHellos = %v, %v", r, err)
	}

	chat, err := client.Chat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(&pb.HelloRequest{Name: "z"}); err != nil {
		t.Fatal(err)
	}
	if r, err := chat.Recv(); err != nil || r.GetMessage() != "hello z" {
		t.Errorf("Chat = %v, %v", r, err)
	}
	if err := chat.CloseSend(); err != nil {
		t.Fatal(err)
	}
	if _, err := chat.Recv(); !errors.Is(err, io.EOF) {
		t.Errorf("Chat end = %v", err)
	}
}
