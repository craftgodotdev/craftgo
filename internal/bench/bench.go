// Package bench houses bind-path microbenchmarks. Inputs are
// pre-extracted strings so the numbers measure only the per-field
// parse + write-back cost; both parsers share the same
// encoding/json.Unmarshal call in the hot loop.
package bench
