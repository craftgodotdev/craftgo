package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// notesDesign is one route whose JSON body the handler reads before the logic runs.
const notesDesign = `package api

type Note {
	text string
}

service Notes {
	post Add /notes {
		request  Note
		response Note
	}
}
`

// The main.go craftgo writes for routes and RPCs, built and run: each listener logs the address it
// bound, a signal stops the gRPC listener while an HTTP request still drains, and a listener that
// cannot bind shuts the process down with exit status 1.
func TestGeneratedMainListenersAndShutdown(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the process is stopped with SIGTERM")
	}
	dir := grpcProject(t)
	mustWrite(t, dir, "design/craftgo.design.yaml", protoOnlyManifest)
	mustWrite(t, dir, "design/api/notes.craftgo", notesDesign)
	mustWrite(t, dir, "design/greet/greet.proto", greetProto)
	genProject(t, dir)
	app := filepath.Join(dir, "app")
	if out, err := goIn(dir, "build", "-o", app, "."); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	config := func(httpAddr, grpcAddr string) {
		mustWrite(t, dir, "config/config.yaml", "server:\n  addr: \""+httpAddr+"\"\ngrpc:\n  addr: \""+grpcAddr+
			"\"\nmetrics:\n  enabled: false\notel:\n  enabled: false\n")
	}

	t.Run("signal", func(t *testing.T) {
		config("127.0.0.1:0", "127.0.0.1:0")
		proc := startApp(t, dir, app)
		httpAddr, grpcAddr := proc.bound(t, "listening"), proc.bound(t, "grpc listening")
		conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		health := healthpb.NewHealthClient(conn)
		serving := func() bool {
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			resp, err := health.Check(ctx, &healthpb.HealthCheckRequest{})
			return err == nil && resp.GetStatus() == healthpb.HealthCheckResponse_SERVING
		}
		if !serving() {
			t.Fatal("the gRPC listener is not serving")
		}

		// A request whose body is still arriving keeps the HTTP listener draining.
		body, send := io.Pipe()
		answered := make(chan int, 1)
		go func() {
			resp, err := http.Post("http://"+httpAddr+"/notes", "application/json", body)
			if err != nil {
				answered <- 0
				return
			}
			resp.Body.Close()
			answered <- resp.StatusCode
		}()
		if _, err := send.Write([]byte(`{"text":`)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(200 * time.Millisecond)
		if err := proc.cmd.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(2 * time.Second)
		for serving() {
			if time.Now().After(deadline) {
				t.Fatal("gRPC still serves 2s after the signal while HTTP drains")
			}
			time.Sleep(50 * time.Millisecond)
		}
		select {
		case code := <-answered:
			t.Fatalf("the draining request was answered %d before its body arrived", code)
		default:
		}
		if _, err := send.Write([]byte(`"x"}`)); err != nil {
			t.Fatal(err)
		}
		send.Close()
		if code := <-answered; code != http.StatusNotImplemented {
			t.Errorf("the drained request answered %d, want the stub's 501", code)
		}
		if code := proc.exit(t); code != 0 {
			t.Errorf("exit status %d after a signal, want 0\n%s", code, proc.out())
		}
	})

	t.Run("bind failure", func(t *testing.T) {
		busy, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer busy.Close()
		config("127.0.0.1:0", busy.Addr().String())
		proc := startApp(t, dir, app)
		if code := proc.exit(t); code != 1 {
			t.Errorf("exit status %d, want 1\n%s", code, proc.out())
		}
		if _, ok := proc.field("grpc server start", "error"); !ok {
			t.Errorf("the bind failure is not logged\n%s", proc.out())
		}
		if addr, ok := proc.field("grpc listening", "addr"); ok {
			t.Errorf("logged listening on %s, which it never bound", addr)
		}
	})
}

// appProcess is a running build of a generated project and the log lines it writes.
type appProcess struct {
	cmd  *exec.Cmd
	mu   sync.Mutex
	logs bytes.Buffer
	done chan error
}

// startApp runs app in dir, killing it when t ends.
func startApp(t *testing.T, dir, app string) *appProcess {
	t.Helper()
	p := &appProcess{cmd: exec.Command(app), done: make(chan error, 1)}
	p.cmd.Dir = dir
	p.cmd.Stdout, p.cmd.Stderr = p, p
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { p.done <- p.cmd.Wait() }()
	t.Cleanup(func() { _ = p.cmd.Process.Kill() })
	return p
}

func (p *appProcess) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.logs.Write(b)
}

func (p *appProcess) out() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.logs.String()
}

// field returns the key of the first JSON log line whose message is msg.
func (p *appProcess) field(msg, key string) (string, bool) {
	lines := bufio.NewScanner(bytes.NewBufferString(p.out()))
	for lines.Scan() {
		var line map[string]any
		if json.Unmarshal(lines.Bytes(), &line) != nil || line["msg"] != msg {
			continue
		}
		v, ok := line[key].(string)
		return v, ok
	}
	return "", false
}

// bound waits for the msg line and returns its address, failing t unless it names a real port.
func (p *appProcess) bound(t *testing.T, msg string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if addr, ok := p.field(msg, "addr"); ok {
			if _, port, err := net.SplitHostPort(addr); err != nil || port == "0" {
				t.Fatalf("%q names %q, not the port it bound", msg, addr)
			}
			return addr
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %q line\n%s", msg, p.out())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// exit waits for the process to end and returns its exit status.
func (p *appProcess) exit(t *testing.T) int {
	t.Helper()
	select {
	case err := <-p.done:
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		if err != nil {
			t.Fatal(err)
		}
		return 0
	case <-time.After(15 * time.Second):
		t.Fatalf("the process did not exit\n%s", p.out())
		return -1
	}
}
