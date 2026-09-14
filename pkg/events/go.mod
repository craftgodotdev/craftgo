// The event runtime is its own module so a generated contract package can
// depend on it alone. It imports nothing outside the standard library, and
// the floor is the oldest release its code needs (atomic.Uint64, Go 1.19),
// rounded to a version still in wide use - a team importing a contract
// should not inherit craftgo's toolchain.
//
// The floor predates per-iteration loop variables, so a closure capturing a
// `for` variable in this module sees the last value. Pass it as a parameter.
module github.com/craftgodotdev/craftgo/pkg/events

go 1.21
