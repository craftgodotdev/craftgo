package ast

// AllDeclKinds is every concrete [Decl] the parser can put in
// [File.Decls]. A dispatcher that switches on declaration kind without a
// default silently skips what it does not list, which is how a whole
// declaration - or its comments - disappears from generated output. Any
// package that walks declarations asserts against this list, so adding a
// kind fails the build instead of going unnoticed.
func AllDeclKinds() []Decl {
	return []Decl{
		&TypeDecl{Name: "T"},
		&EnumDecl{Name: "E"},
		&ErrorDecl{Name: "Err"},
		&ScalarDecl{Name: "S"},
		&MiddlewareDecl{Name: "M"},
		&ServiceDecl{Name: "Svc"},
		&EventDecl{Name: "Ev"},
	}
}
