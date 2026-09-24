package parser

import (
	"strings"
	"testing"
	"time"
)

// TestUnfinishedTypeArgsDoNotHang pins that an unfinished type-argument list
// is reported without hanging the parser.
func TestUnfinishedTypeArgsDoNotHang(t *testing.T) {
	for _, src := range []string{
		"package p\ntype A { x < }\n",
		"package p\ntype A { x Box< }\n",
		"package p\ntype A { x Box<int, }\n",
		"package p\ntype A { x Box<int } }\n",
		"package p\ntype A { Page<User }\n",
	} {
		done := make(chan []string, 1)
		go func() { _, msgs := parseWithErrors(t, src); done <- msgs }()
		select {
		case msgs := <-done:
			if len(msgs) == 0 {
				t.Errorf("%q: want a diagnostic", src)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%q: parser hangs", src)
		}
	}
}

func TestEmptyTypeArgsRejected(t *testing.T) {
	_, msgs := parseWithErrors(t, "package p\ntype A { x Box<> }\n")
	if len(msgs) != 1 || !strings.Contains(msgs[0], "type argument list cannot be empty") {
		t.Fatalf("diagnostics = %v", msgs)
	}
}
