package nats

import (
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// stuckProgress is a delivery whose InProgress blocks until release is closed.
type stuckProgress struct {
	jetstream.Msg
	entered chan struct{}
	release chan struct{}
}

func (m *stuckProgress) InProgress() error {
	select {
	case m.entered <- struct{}{}:
	default:
	}
	<-m.release
	return nil
}

// Stopping the ticker waits for an InProgress already being sent, so none follows the answer.
func TestStoppingTheProgressTickerWaitsForItsLastSend(t *testing.T) {
	m := &stuckProgress{entered: make(chan struct{}, 1), release: make(chan struct{})}
	stop := holdOpen(m, 2*time.Millisecond)
	select {
	case <-m.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the ticker never sent InProgress")
	}

	stopped := make(chan struct{})
	go func() {
		stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("stop returned while an InProgress was still being sent")
	case <-time.After(100 * time.Millisecond):
	}
	close(m.release)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("stop never returned")
	}
}
