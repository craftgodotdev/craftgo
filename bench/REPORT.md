# Bind perf report

| Field     | Value                               |
| --------- | ----------------------------------- |
| Generated | 2026-04-29 02:22:07 UTC             |
| Go        | `go1.24.2`                          |
| OS / Arch | darwin / arm64                      |
| CPU       | Apple M1                            |
| Source    | `internal/bench/bind_bench_test.go` |

## Summary

Mean across all -count runs. Lower is better.

| Benchmark                     | ns/op | B/op | allocs/op | runs |
| ----------------------------- | ----: | ---: | --------: | ---: |
| BenchmarkParseSimpleCodegen   |    39 |   24 |         1 |    3 |
| BenchmarkParseSimpleReflect   |   237 |   48 |         2 |    3 |
| BenchmarkParseComplexCodegen  |  1452 |  744 |        16 |    3 |
| BenchmarkParseComplexReflect  |  2756 |  960 |        26 |    3 |
| BenchmarkParseComplexBodyOnly |  1419 |  720 |        14 |    3 |

## Codegen vs Reflect

Ratio < 1 means codegen wins. Ratio > 1 means reflect wins.
Two shape buckets so the report shows how the gap scales
with field count.

| Shape                                          | Fields | Ratio (codegen ÷ reflect) |
| ---------------------------------------------- | -----: | ------------------------- |
| simple (path + 1 query)                        |      2 | 0.17x ns, 0.50x allocs    |
| complex (path + query×4 + hdr + cookie + body) |      9 | 0.53x ns, 0.62x allocs    |

## Raw

```
goos: darwin
goarch: arm64
pkg: github.com/craftgodotdev/craftgo/internal/bench
cpu: Apple M1
BenchmarkParseSimpleCodegen-8     	64523796	        36.96 ns/op	      24 B/op	       1 allocs/op
BenchmarkParseSimpleCodegen-8     	64628838	        37.44 ns/op	      24 B/op	       1 allocs/op
BenchmarkParseSimpleCodegen-8     	64189000	        43.36 ns/op	      24 B/op	       1 allocs/op
BenchmarkParseSimpleReflect-8     	10674759	       229.5 ns/op	      48 B/op	       2 allocs/op
BenchmarkParseSimpleReflect-8     	10533372	       235.9 ns/op	      48 B/op	       2 allocs/op
BenchmarkParseSimpleReflect-8     	10089550	       245.6 ns/op	      48 B/op	       2 allocs/op
BenchmarkParseComplexCodegen-8    	 1627156	      1442 ns/op	     744 B/op	      16 allocs/op
BenchmarkParseComplexCodegen-8    	 1675935	      1434 ns/op	     744 B/op	      16 allocs/op
BenchmarkParseComplexCodegen-8    	 1672578	      1479 ns/op	     744 B/op	      16 allocs/op
BenchmarkParseComplexReflect-8    	  912229	      2704 ns/op	     960 B/op	      26 allocs/op
BenchmarkParseComplexReflect-8    	  860006	      2731 ns/op	     960 B/op	      26 allocs/op
BenchmarkParseComplexReflect-8    	  784372	      2832 ns/op	     960 B/op	      26 allocs/op
BenchmarkParseComplexBodyOnly-8   	 1809002	      1581 ns/op	     720 B/op	      14 allocs/op
BenchmarkParseComplexBodyOnly-8   	 1591808	      1333 ns/op	     720 B/op	      14 allocs/op
BenchmarkParseComplexBodyOnly-8   	 1852411	      1343 ns/op	     720 B/op	      14 allocs/op
PASS
ok  	github.com/craftgodotdev/craftgo/internal/bench	49.389s
```
