package kafka

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl"

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

// clientOf opens a client the way tr opens every client; nothing is dialed until it is used.
func clientOf(t *testing.T, tr *Transport) *kgo.Client {
	t.Helper()
	cl, err := tr.newClient("")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(cl.Close)
	return cl
}

// WithTLS hands every client the config it was given.
func TestWithTLSHandsTheClientItsConfig(t *testing.T) {
	cfg := &tls.Config{ServerName: "kafka.internal"}
	cl := clientOf(t, New([]string{"127.0.0.1:1"}, WithTLS(cfg)))
	if got, _ := cl.OptValue(kgo.DialTLSConfig).(*tls.Config); got != cfg {
		t.Errorf("dial TLS config = %p, want the one given (%p)", got, cfg)
	}
}

// Each SASL option hands every client its mechanism, carrying the credentials given.
func TestTheSASLOptionsHandTheClientTheirMechanism(t *testing.T) {
	for _, c := range []struct {
		opt   Option
		mech  string
		first string
	}{
		{WithSASLPlain("user", "secret"), "PLAIN", "\x00user\x00secret"},
		{WithSASLSCRAMSHA256("user", "secret"), "SCRAM-SHA-256", "n=user,"},
		{WithSASLSCRAMSHA512("user", "secret"), "SCRAM-SHA-512", "n=user,"},
	} {
		t.Run(c.mech, func(t *testing.T) {
			cl := clientOf(t, New([]string{"127.0.0.1:1"}, c.opt))
			mechs, _ := cl.OptValue(kgo.SASL).([]sasl.Mechanism)
			if len(mechs) != 1 || mechs[0].Name() != c.mech {
				t.Fatalf("mechanisms = %v, want one %s", mechs, c.mech)
			}
			_, first, err := mechs[0].Authenticate(context.Background(), "127.0.0.1:1")
			if err != nil {
				t.Fatalf("authenticate: %v", err)
			}
			if !strings.Contains(string(first), c.first) {
				t.Errorf("first message %q does not carry %q", first, c.first)
			}
		})
	}
}

// WithAutoCreateTopics decides whether a client asks the broker to create a missing topic.
func TestWithAutoCreateTopicsReachesTheClient(t *testing.T) {
	for _, c := range []struct {
		name string
		tr   *Transport
		want bool
	}{
		{"default", New([]string{"127.0.0.1:1"}), false},
		{"on", New([]string{"127.0.0.1:1"}, WithAutoCreateTopics(true)), true},
	} {
		if got, _ := clientOf(t, c.tr).OptValue(kgo.AllowAutoTopicCreation).(bool); got != c.want {
			t.Errorf("%s: auto topic creation = %v, want %v", c.name, got, c.want)
		}
	}
}
