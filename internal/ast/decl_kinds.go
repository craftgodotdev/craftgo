package ast

// AllDeclKinds returns one value of each concrete [Decl] type, for tests that
// check a declaration switch covers every kind.
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
