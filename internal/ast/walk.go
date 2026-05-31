package ast

// EachField calls fn for every Field directly declared in body, in
// source order. Mixin members are skipped — they're embedded type
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

// EachMember calls fn for every TypeMember in body. Used by walkers
// that need to see both Fields AND Mixins (e.g. validate emission,
// where the host's Validate() must call the embedded mixin's
// Validate()). Fn returns false to stop.
func EachMember(body []TypeMember, fn func(TypeMember) bool) {
	for _, m := range body {
		if !fn(m) {
			return
		}
	}
}

// FindField returns the first Field in body whose Name matches, or
// nil. Mixin members are not considered (a mixin's fields surface
// via Go's struct embedding at runtime, not under a single name in
// the host's body).
func FindField(body []TypeMember, name string) *Field {
	var out *Field
	EachField(body, func(f *Field) bool {
		if f.Name == name {
			out = f
			return false
		}
		return true
	})
	return out
}
