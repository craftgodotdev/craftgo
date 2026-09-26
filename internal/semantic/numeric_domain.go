package semantic

import (
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/prims"
)

// scale is the set of quantities a bound meets as the generated check
// compares them: the whole numbers of an integer primitive's range or of a
// length or item count, the finite floats of a float primitive's width, or,
// for another type, the rationals.
type scale struct {
	name   string // how a message names a quantity on the scale: `uint8`, `length`
	whole  bool
	bits   int      // a float's width
	lo, hi *big.Rat // the range; nil for the rationals
}

// scaleOf returns the scale of what a bound on limits measures on a value of
// primitive prim; a length or item count is an int, at most MaxInt64.
func scaleOf(prim, limits string) scale {
	if limits != "value" {
		return scale{name: limits, whole: true, lo: new(big.Rat), hi: new(big.Rat).SetInt64(math.MaxInt64)}
	}
	if lo, hi, ok := prims.Capacity(prim); ok {
		return scale{name: prim, whole: true, lo: new(big.Rat).SetInt(lo), hi: new(big.Rat).SetInt(hi)}
	}
	if sp, ok := prims.Lookup(prim); ok && sp.Kind == prims.Float {
		hi := new(big.Rat).SetFloat64(math.MaxFloat64)
		if sp.Bits == 32 {
			hi.SetFloat64(math.MaxFloat32)
		}
		return scale{name: prim, bits: sp.Bits, lo: new(big.Rat).Neg(hi), hi: hi}
	}
	return scale{name: limits}
}

// atWidth returns l at a float scale's width, as the generated check
// converts its literal; ok is false when l overflows the width.
func (sc scale) atWidth(l NumericLit) (float64, bool) {
	f, err := strconv.ParseFloat(l.Text(), sc.bits)
	return f, err == nil
}

// cmp compares quantities q and v as the generated check does: at a float
// scale's width, exactly on another scale.
func (sc scale) cmp(q, v NumericLit) int {
	if sc.bits != 0 {
		qf, qok := sc.atWidth(q)
		vf, vok := sc.atWidth(v)
		if qok && vok {
			return new(big.Rat).SetFloat64(qf).Cmp(new(big.Rat).SetFloat64(vf))
		}
	}
	return q.Cmp(v)
}

// limit is a bound as the generated check applies it on its scale.
type limit struct {
	bound
	of string // "" for a bound of the site checked, else ` of scalar <Name>`
	sc scale
	// at is the least (lower) or greatest (upper) quantity on sc the bound
	// admits; nil when none is, or when its value is not on sc, which the
	// capacity, whole-number and count rules report.
	at *big.Rat
	// none marks a bound that admits no quantity on sc.
	none bool
}

// limitOn returns b as a limit on sc: on whole numbers a strict bound moves
// to the next whole number, on a float scale to the next float of its width.
func limitOn(sc scale, b bound, of string) limit {
	l := limit{bound: b, of: of, sc: sc}
	var at *big.Rat
	switch {
	case sc.whole:
		v := b.value.Rat()
		if v == nil || !v.IsInt() || v.Cmp(sc.lo) < 0 || v.Cmp(sc.hi) > 0 {
			return l
		}
		n := new(big.Int).Set(v.Num())
		if b.Strict && b.Lower {
			n.Add(n, big.NewInt(1))
		} else if b.Strict {
			n.Sub(n, big.NewInt(1))
		}
		at = new(big.Rat).SetInt(n)
	case sc.bits != 0:
		f, ok := sc.atWidth(b.value)
		if !ok {
			return l
		}
		if b.Strict {
			f = nextFloat(f, sc.bits, b.Lower)
		}
		if math.IsInf(f, 0) {
			l.none = true
			return l
		}
		at = new(big.Rat).SetFloat64(f)
	default:
		return l
	}
	if b.Lower && at.Cmp(sc.hi) > 0 || !b.Lower && at.Cmp(sc.lo) < 0 {
		l.none = true
		return l
	}
	l.at = at
	return l
}

// nextFloat returns the float of width bits next to f, above it when up.
func nextFloat(f float64, bits int, up bool) float64 {
	to := math.Inf(-1)
	if up {
		to = math.Inf(1)
	}
	if bits == 32 {
		return float64(math.Nextafter32(float32(f), float32(to)))
	}
	return math.Nextafter(f, to)
}

// BoundImpliedByType reports whether bound i of those d puts - `@range`'s
// low end is 0, its high end 1 - holds for every value of primitive prim, or
// every length or item count, the Go type carries, so its check never
// fails. A float's bound is never implied: a float parameter can carry NaN
// or an infinity.
func BoundImpliedByType(prim string, d *ast.Decorator, i int) bool {
	bs := declaredBounds([]*ast.Decorator{d})
	if i < 0 || i >= len(bs) {
		return false
	}
	sc := scaleOf(prim, bs[i].limits)
	l := limitOn(sc, bs[i], "")
	switch {
	case !sc.whole || l.at == nil:
		return false
	case l.Lower:
		return l.at.Cmp(sc.lo) <= 0
	}
	return l.at.Cmp(sc.hi) >= 0
}

// siteLimits returns the limits the bounds of sites put on a value of
// primitive prim.
func siteLimits(prim string, sites []constraintSite) []limit {
	var out []limit
	for _, s := range sites {
		for _, b := range declaredBounds(s.decs) {
			out = append(out, limitOn(scaleOf(prim, b.limits), b, s.of))
		}
	}
	return out
}

// checkValueDomain reports the bounds of sites[0] that, with the bounds of
// every site, leave no value, length or item count of primitive prim: a pair
// no quantity meets, compared exactly and on the scale the generated check
// compares on; else a bound past what the type holds; else a @multipleOf no
// quantity within the bounds meets. A later site is the scalar a field's type
// names, whose own bounds its own check reports.
func (a *analyzer) checkValueDomain(prim string, sites []constraintSite) {
	ls := siteLimits(prim, sites)
	if a.reportContradictingPairs(ls) || a.reportLimitsPastScale(prim, ls) {
		return
	}
	a.reportNoMultiple(prim, sites, ls)
}

// reportContradictingPairs reports each lower and upper limit on the same
// quantity, one of them the checked site's, that no quantity meets both; the
// diagnostic sits at the site's own bound, the upper one when both are.
func (a *analyzer) reportContradictingPairs(ls []limit) bool {
	reported := false
	for _, lo := range ls {
		for _, hi := range ls {
			if !lo.Lower || hi.Lower || lo.dec == hi.dec || lo.limits != hi.limits || lo.of != "" && hi.of != "" {
				continue
			}
			code, noun, ok := contradiction(lo, hi)
			if !ok {
				continue
			}
			at, other := hi, lo
			if hi.of != "" {
				at, other = lo, hi
			}
			d := a.diag(at.pos, at.pos, lexer.SeverityError, code,
				"%s contradicts %s%s: no %s is both %s and %s",
				decoratorCall(at.dec), decoratorCall(other.dec), other.of, noun, lo.relation(), hi.relation())
			d.Related = related(other.pos, decoratorCall(other.dec)+other.of+" declared here")
			reported = true
		}
	}
	return reported
}

// contradiction reports whether no quantity meets both lo and hi: with code
// CodeDecoratorRange when lo lies above hi as written, else
// CodeBoundEmptyRange; noun names the quantity, by its scale when only the
// scale's grid or width leaves none.
func contradiction(lo, hi limit) (code, noun string, ok bool) {
	switch c := lo.value.Cmp(hi.value); {
	case c > 0:
		return CodeDecoratorRange, lo.limits, true
	case c == 0 && (lo.Strict || hi.Strict):
		return CodeBoundEmptyRange, lo.limits, true
	}
	if lo.at != nil && hi.at != nil && lo.at.Cmp(hi.at) > 0 {
		return CodeBoundEmptyRange, lo.sc.name, true
	}
	return "", "", false
}

// reportLimitsPastScale reports each bound of the checked site that admits
// no quantity its type holds: `@gt(255)` on a uint8.
func (a *analyzer) reportLimitsPastScale(prim string, ls []limit) bool {
	reported := false
	for _, l := range ls {
		if l.of != "" || !l.none {
			continue
		}
		reported = true
		if !l.Lower && prims.IsUnsigned(prim) {
			fix := "a positive bound"
			if l.dec.Name == "negative" {
				fix = "drop @negative"
			}
			a.diag(l.dec.Pos, decoratorEnd(l.dec), lexer.SeverityError, CodeDecoratorTypeMismatch,
				"%s cannot apply to an unsigned type (%s is always >= 0) - every value would be rejected; use a signed integer or %s",
				decoratorCall(l.dec), prim, fix)
			continue
		}
		a.diag(l.pos, l.pos, lexer.SeverityError, CodeBoundEmptyRange,
			"%s leaves no value: no %s is %s", decoratorCall(l.dec), l.sc.name, l.relation())
	}
	return reported
}

// reportNoMultiple reports a @multipleOf of an integer value that no value
// within the tightest bounds of sites meets, at the checked site's
// @multipleOf, else at its bound among the tightest; the site takes no part
// when neither is its.
func (a *analyzer) reportNoMultiple(prim string, sites []constraintSite, ls []limit) {
	sc := scaleOf(prim, "value")
	if !sc.whole {
		return
	}
	// A part is a decorator that decides the emptiness: a divisor, then the
	// tightest bounds.
	type part struct {
		dec *ast.Decorator
		pos lexer.Position
		of  string
	}
	var parts []part
	var divisors []string
	lcm := big.NewInt(1)
	for _, s := range sites {
		d := ast.FindDecorator(s.decs, "multipleOf")
		if d == nil {
			continue
		}
		arg := firstPositional(d)
		v, ok := ParseNumericArg(arg)
		r := v.Rat()
		if !ok || r == nil || !r.IsInt() || r.Sign() <= 0 || r.Cmp(sc.hi) > 0 {
			continue
		}
		parts = append(parts, part{dec: d, pos: arg.Pos, of: s.of})
		divisors = append(divisors, literalText(arg.Value))
		lcm.Mul(lcm, new(big.Int).Quo(r.Num(), new(big.Int).GCD(nil, nil, lcm, r.Num())))
	}
	if len(parts) == 0 {
		return
	}
	var lo, hi *limit
	for i := range ls {
		switch l := &ls[i]; {
		case l.limits != "value" || l.at == nil:
		case l.Lower && (lo == nil || l.at.Cmp(lo.at) > 0):
			lo = l
		case !l.Lower && (hi == nil || l.at.Cmp(hi.at) < 0):
			hi = l
		}
	}
	from, to := sc.lo, sc.hi
	var within []string
	if lo != nil {
		from = lo.at
		within = append(within, lo.relation())
		parts = append(parts, part{dec: lo.dec, pos: lo.pos, of: lo.of})
	}
	if hi != nil {
		to = hi.at
		within = append(within, hi.relation())
		parts = append(parts, part{dec: hi.dec, pos: hi.pos, of: hi.of})
	}
	// The least multiple of lcm at or above from, whole since from is.
	first := new(big.Int).Neg(new(big.Int).Div(new(big.Int).Neg(from.Num()), lcm))
	if new(big.Rat).SetInt(first.Mul(first, lcm)).Cmp(to) <= 0 {
		return
	}
	var at, other *part
	for i := range parts {
		switch p := &parts[i]; {
		case p.of == "" && at == nil:
			at = p
		case p.of != "" && other == nil:
			other = p
		}
	}
	if at == nil {
		return
	}
	d := a.diag(at.pos, at.pos, lexer.SeverityError, CodeBoundEmptyRange,
		"%s leaves no value: no %s %s is a multiple of %s",
		decoratorCall(at.dec), sc.name, strings.Join(within, " and "), strings.Join(divisors, " and "))
	if other != nil {
		d.Related = related(other.pos, decoratorCall(other.dec)+other.of+" declared here")
	}
}
