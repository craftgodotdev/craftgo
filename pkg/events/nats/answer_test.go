package nats

import (
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	events "github.com/craftgodotdev/craftgo/pkg/events"
)

// answerRecorder records how a delivery was answered.
type answerRecorder struct {
	jetstream.Msg
	got string
}

func (m *answerRecorder) Ack() error {
	m.got = "ack"
	return nil
}

func (m *answerRecorder) Nak() error {
	m.got = "nak"
	return nil
}

func (m *answerRecorder) NakWithDelay(d time.Duration) error {
	m.got = "nak after " + d.String()
	return nil
}

func (m *answerRecorder) Term() error {
	m.got = "term"
	return nil
}

// The answer follows the chain's disposition, and the cap turns a Redeliver into a termination.
func TestTheAnswerFollowsTheDispositionUpToTheCap(t *testing.T) {
	redeliver := (*events.Message).Redeliver
	for _, c := range []struct {
		name       string
		tr         *JetStream
		ask        func(*events.Message)
		deliveries int
		want       string
	}{
		{"unset settles", &JetStream{maxDeliveries: 3}, func(*events.Message) {}, 1, "ack"},
		{"settle", &JetStream{maxDeliveries: 3}, (*events.Message).Settle, 1, "ack"},
		{"a success after many deliveries", &JetStream{maxDeliveries: 3}, func(*events.Message) {}, 9, "ack"},
		{"redeliver under the cap", &JetStream{maxDeliveries: 3}, redeliver, 2, "nak"},
		{"redeliver at the cap", &JetStream{maxDeliveries: 3}, redeliver, 3, "term"},
		{"redeliver with no cap", &JetStream{}, redeliver, 1000, "nak"},
		{"redeliver with a backoff", &JetStream{backoff: func(n int) time.Duration { return time.Duration(n) * time.Second }}, redeliver, 2, "nak after 2s"},
		{"reject", &JetStream{maxDeliveries: 3}, (*events.Message).Reject, 1, "term"},
	} {
		msg := &events.Message{}
		msg.SetDeliveries(c.deliveries)
		c.ask(msg)
		m := &answerRecorder{}
		if err := c.tr.answer(m, msg); err != nil {
			t.Fatalf("%s: answer: %v", c.name, err)
		}
		if m.got != c.want {
			t.Errorf("%s: answered %q, want %q", c.name, m.got, c.want)
		}
	}
}
