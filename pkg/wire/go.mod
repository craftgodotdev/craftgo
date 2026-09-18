// The pass-through wire type is its own module so a generated contract
// package can name it without inheriting the craftgo toolchain: a design
// with a `bytes @format(raw)` field lowers to wire.Raw, and a consumer of
// that contract should pull in this and nothing else. It imports only the
// standard library, and the floor is the oldest release its code needs
// (the `any` alias, Go 1.18) - lower than pkg/events on purpose, because
// there is less here to need anything newer.
//
// The floor predates per-iteration loop variables and `testing.B.Loop`,
// so a closure capturing a `for` variable in this module sees the last
// value, and a benchmark counts with `b.N`.
module github.com/craftgodotdev/craftgo/pkg/wire

go 1.18
