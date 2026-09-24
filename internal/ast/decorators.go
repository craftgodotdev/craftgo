package ast

// FindDecorator returns the first decorator in decs named name, or nil; nil
// entries are skipped.
func FindDecorator(decs []*Decorator, name string) *Decorator {
	for _, d := range decs {
		if d != nil && d.Name == name {
			return d
		}
	}
	return nil
}

// HasDecorator reports whether decs has a decorator named name.
func HasDecorator(decs []*Decorator, name string) bool {
	return FindDecorator(decs, name) != nil
}

// DecoratorArgValues returns a's value, or the elements of an array value, so
// `@x([a, b])` reads like `@x(a, b)`. It returns nil for a nil a.
func DecoratorArgValues(a *DecoratorArg) []Expr {
	if a == nil {
		return nil
	}
	if arr, ok := a.Value.(*ArrayLit); ok {
		return arr.Elements
	}
	return []Expr{a.Value}
}
