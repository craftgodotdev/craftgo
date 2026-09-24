package semantic

import (
	"testing"
	"time"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// A bare integer counts seconds; a duration literal reads as written.
func TestDurationArg(t *testing.T) {
	cases := []struct {
		name string
		arg  *ast.DecoratorArg
		want time.Duration
		ok   bool
	}{
		{"nil", nil, 0, false},
		{"bare seconds", &ast.DecoratorArg{Value: &ast.IntLit{Value: 60}}, time.Minute, true},
		{"literal", &ast.DecoratorArg{Value: &ast.DurationLit{Text: "1.5h"}}, 90 * time.Minute, true},
		{"microseconds", &ast.DecoratorArg{Value: &ast.DurationLit{Text: "250µs"}}, 250 * time.Microsecond, true},
		{"unreadable literal", &ast.DecoratorArg{Value: &ast.DurationLit{Text: "5x"}}, 0, false},
		{"seconds past a duration", &ast.DecoratorArg{Value: &ast.IntLit{Value: maxDurationSeconds + 1}}, 0, false},
		{"other kind", &ast.DecoratorArg{Value: &ast.StringLit{Value: "5s"}}, 0, false},
	}
	for _, c := range cases {
		got, ok := DurationArg(c.arg)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: DurationArg = (%v, %v), want (%v, %v)", c.name, got, ok, c.want, c.ok)
		}
	}
}
