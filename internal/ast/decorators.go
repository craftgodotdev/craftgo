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

// Arg returns the first argument of type T among the decorators in decs named
// name, array elements included, and whether there is one.
func Arg[T Expr](decs []*Decorator, name string) (T, bool) {
	for _, d := range decs {
		if d == nil || d.Name != name {
			continue
		}
		for _, a := range d.Args {
			for _, v := range DecoratorArgValues(a) {
				if t, ok := v.(T); ok {
					return t, true
				}
			}
		}
	}
	var zero T
	return zero, false
}

// StringArg is [Arg] for a string literal, returning its value; an empty
// string counts as present.
func StringArg(decs []*Decorator, name string) (string, bool) {
	s, ok := Arg[*StringLit](decs, name)
	if !ok {
		return "", false
	}
	return s.Value, true
}

// ArgName is a string or a name a decorator argument gives, and where it is
// written.
type ArgName struct {
	Value string
	Pos   Pos
}

// ArgNames returns the strings and names among d's positional arguments,
// reading `@x([a, b])` like `@x(a, b)`; other values are skipped.
func ArgNames(d *Decorator) []ArgName {
	var out []ArgName
	for _, a := range d.Args {
		if a == nil || a.Named {
			continue
		}
		for _, v := range DecoratorArgValues(a) {
			if s, ok := TextValue(v); ok {
				out = append(out, ArgName{Value: s, Pos: v.ExprPos()})
			}
		}
	}
	return out
}

// TextValue returns the text of a string literal or the dotted form of a
// name, and false for any other expression.
func TextValue(e Expr) (string, bool) {
	switch v := e.(type) {
	case *StringLit:
		return v.Value, true
	case *IdentExpr:
		if v.Name != nil {
			return v.Name.String(), true
		}
	}
	return "", false
}
