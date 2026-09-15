package ast

import "testing"

// Every kind in the list must actually satisfy the interface and report
// its own name, which is what the dispatchers key on.
func TestAllDeclKindsAreUsable(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range AllDeclKinds() {
		name := d.DeclName()
		if name == "" {
			t.Errorf("%T reports no name", d)
		}
		key := typeName(d)
		if seen[key] {
			t.Errorf("%s listed twice", key)
		}
		seen[key] = true
	}
	if len(seen) < 7 {
		t.Errorf("expected at least 7 declaration kinds, got %d", len(seen))
	}
}

func typeName(d Decl) string {
	switch d.(type) {
	case *TypeDecl:
		return "TypeDecl"
	case *EnumDecl:
		return "EnumDecl"
	case *ErrorDecl:
		return "ErrorDecl"
	case *ScalarDecl:
		return "ScalarDecl"
	case *MiddlewareDecl:
		return "MiddlewareDecl"
	case *ServiceDecl:
		return "ServiceDecl"
	case *EventDecl:
		return "EventDecl"
	}
	return ""
}
