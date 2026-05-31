package ast

import "slices"

// FindDecorator returns the FIRST decorator in decs whose Name matches.
// Returns nil when no decorator matches or decs is empty. Nil entries
// in decs are skipped, so callers iterate without their own nil guard.
func FindDecorator(decs []*Decorator, name string) *Decorator {
	for _, d := range decs {
		if d != nil && d.Name == name {
			return d
		}
	}
	return nil
}

// HasDecorator reports whether decs contains a decorator with the
// given Name. Equivalent to `FindDecorator(decs, name) != nil` but
// reads better at call sites that only need the boolean.
func HasDecorator(decs []*Decorator, name string) bool {
	return FindDecorator(decs, name) != nil
}

// FindDecoratorAny returns the first decorator whose Name matches ANY
// of the supplied names. Useful for binding-kind classifiers (`path`
// vs `query` vs `header` vs `cookie` vs `body` vs `form`) that want a
// single pass instead of N FindDecorator calls.
func FindDecoratorAny(decs []*Decorator, names ...string) *Decorator {
	for _, d := range decs {
		if d == nil {
			continue
		}
		if slices.Contains(names, d.Name) {
			return d
		}
	}
	return nil
}
