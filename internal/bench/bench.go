// Package bench houses bind-path microbenchmarks. The benchmarks
// here deliberately strip every layer of HTTP machinery —
// url.ParseQuery, http.Request.Cookie(), header maps — so the
// numbers measure ONLY the per-field parse + write-back cost. The
// inputs are pre-extracted strings; the only system call exercised
// in the hot loop is `encoding/json.Unmarshal`, which both parsers
// share. That makes the codegen-vs-reflect delta interpretable
// without arguing about how many times r.URL.Query() got called.
package bench
