package lsp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"
)

const (
	initializeMsg = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	shutdownMsg   = `{"jsonrpc":"2.0","id":2,"method":"shutdown"}`
	exitMsg       = `{"jsonrpc":"2.0","method":"exit"}`
)

// serveMessages runs Serve against an in-memory pipe carrying the given
// JSON-RPC bodies and returns its error. The pipe writer is never closed
// before Serve returns, so a server that only unblocks on EOF times out.
func serveMessages(t *testing.T, bodies ...string) error {
	t.Helper()
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })

	done := make(chan error, 1)
	go func() { done <- Serve(context.Background(), pr, io.Discard) }()
	go func() {
		for _, body := range bodies {
			if _, err := fmt.Fprintf(pw, "Content-Length: %d\r\n\r\n%s", len(body), body); err != nil {
				return
			}
		}
	}()

	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after the exit notification")
		return nil
	}
}

func TestServeExitAfterShutdown(t *testing.T) {
	if err := serveMessages(t, initializeMsg, shutdownMsg, exitMsg); err != nil {
		t.Fatalf("Serve() = %v, want nil", err)
	}
}

func TestServeExitWithoutShutdown(t *testing.T) {
	err := serveMessages(t, initializeMsg, exitMsg)
	if !errors.Is(err, errExitWithoutShutdown) {
		t.Fatalf("Serve() = %v, want %v", err, errExitWithoutShutdown)
	}
}
