package kafka

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	events "github.com/craftgodotdev/craftgo/pkg/events"
)

// tlsHandshakeRecord is the first byte of every TLS connection.
const tlsHandshakeRecord = 0x16

// firstByteListener accepts one connection and reports the first byte the client sends.
func firstByteListener(t *testing.T) (addr string, first <-chan byte) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	got := make(chan byte, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		b := make([]byte, 1)
		if _, err := io.ReadFull(conn, b); err == nil {
			got <- b[0]
		}
	}()
	return ln.Addr().String(), got
}

// WithTLS(nil) dials over TLS, so SASL/PLAIN never sends its password in clear.
func TestWithTLSNilDialsOverTLS(t *testing.T) {
	addr, first := firstByteListener(t)
	tr := New([]string{addr}, WithTLS(nil), WithSASLPlain("user", "secret"))
	defer func() { _ = tr.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _ = tr.Publish(ctx, &events.Message{Event: "orders.Placed", Payload: []byte(`{}`)}) }()

	select {
	case b := <-first:
		if b != tlsHandshakeRecord {
			t.Fatalf("first byte on the wire = %#x, want %#x (a TLS handshake) - the client dialed in plaintext", b, tlsHandshakeRecord)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the transport never dialed the broker")
	}
}
