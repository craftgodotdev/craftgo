package ast

// EachField calls fn on each [Field] of body in order until fn returns false;
// mixins are skipped.
func EachField(body []TypeMember, fn func(*Field) bool) {
	for _, m := range body {
		f, ok := m.(*Field)
		if !ok {
			continue
		}
		if !fn(f) {
			return
		}
	}
}
