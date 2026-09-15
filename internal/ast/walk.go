package ast

// EachField calls fn for every Field directly declared in body, in
// source order. Mixin members are skipped - they're embedded type
// references, not fields with their own decorator chain or shape.
//
// The callback may return false to stop iteration early (useful for
// "find first matching field" lookups). Returning true continues.
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
