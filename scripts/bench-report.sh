#!/usr/bin/env bash
# bench-report.sh — turn `go test -bench` raw output into a Markdown
# report. Reads the raw file at $1 (default: bench/results.txt) and
# writes Markdown to $2 (default: bench/REPORT.md).
#
# The report contains:
#   1. Run metadata (date, Go version, OS/arch, CPU)
#   2. A summary table averaged across -count runs (mean ns/op,
#      B/op, allocs/op)
#   3. The raw output, fenced for verbatim record-keeping
#   4. Codegen-vs-reflect deltas (ns ratio + alloc ratio) on the
#      pairs the benchmark file exposes — full bind and no-body.
#
# Pure bash + awk; no Go-side dependency so it works in any CI.

set -euo pipefail

raw="${1:-bench/results.txt}"
out="${2:-bench/REPORT.md}"

if [ ! -f "$raw" ]; then
  echo "bench-report: raw file not found: $raw" >&2
  exit 1
fi

mkdir -p "$(dirname "$out")"

# Probe build environment from go itself so the report is self-describing.
go_version="$(go version 2>/dev/null | awk '{print $3}' || echo unknown)"
goos="$(go env GOOS 2>/dev/null || echo unknown)"
goarch="$(go env GOARCH 2>/dev/null || echo unknown)"
date_utc="$(date -u +'%Y-%m-%d %H:%M:%S UTC')"

# Hardware brand on darwin/linux. Falls back to GOARCH on unknown systems.
cpu_brand="$goarch"
if [ "$goos" = "darwin" ] && command -v sysctl >/dev/null 2>&1; then
  cpu_brand="$(sysctl -n machdep.cpu.brand_string 2>/dev/null || echo "$goarch")"
elif [ "$goos" = "linux" ] && [ -r /proc/cpuinfo ]; then
  cpu_brand="$(awk -F': ' '/model name/{print $2; exit}' /proc/cpuinfo || echo "$goarch")"
fi

# ---------- summary table (mean across -count runs) ----------
summary="$(awk '
  /^Benchmark/ {
    name = $1; sub(/-[0-9]+$/, "", name)   # strip GOMAXPROCS suffix
    ns_sum[name]    += $3
    bytes_sum[name] += $5
    allocs_sum[name]+= $7
    runs[name]++
    order[name]     = (order[name] == "" ? NR : order[name])
  }
  END {
    n = 0
    for (k in runs) { keys[++n] = k }
    # bubble-sort by first-seen order so the report mirrors the test file
    for (i = 1; i <= n; i++) for (j = i+1; j <= n; j++) {
      if (order[keys[i]] > order[keys[j]]) { t=keys[i]; keys[i]=keys[j]; keys[j]=t }
    }
    for (i = 1; i <= n; i++) {
      k = keys[i]; r = runs[k]
      printf "| %s | %.0f | %.0f | %.0f | %d |\n", k, ns_sum[k]/r, bytes_sum[k]/r, allocs_sum[k]/r, r
    }
  }
' "$raw")"

# ---------- delta helper: ratio of two named benchmarks ----------
ratio() {
  local a="$1" b="$2"
  awk -v a="$a" -v b="$b" '
    /^Benchmark/ {
      name = $1; sub(/-[0-9]+$/, "", name)
      ns[name] += $3; allocs[name] += $7; runs[name]++
    }
    END {
      if (runs[a] == 0 || runs[b] == 0) { print "n/a"; exit }
      ans = (ns[a]/runs[a]) / (ns[b]/runs[b])
      acs = (allocs[a]/runs[a]) / (allocs[b]/runs[b])
      printf "%.2fx ns, %.2fx allocs", ans, acs
    }
  ' "$raw"
}

# ---------- emit ----------
{
  echo "# Bind perf report"
  echo
  echo "| Field | Value |"
  echo "| --- | --- |"
  echo "| Generated | $date_utc |"
  echo "| Go | \`$go_version\` |"
  echo "| OS / Arch | $goos / $goarch |"
  echo "| CPU | $cpu_brand |"
  echo "| Source | \`internal/bench/bind_bench_test.go\` |"
  echo
  echo "## Summary"
  echo
  echo "Mean across all -count runs. Lower is better."
  echo
  echo "| Benchmark | ns/op | B/op | allocs/op | runs |"
  echo "| --- | ---: | ---: | ---: | ---: |"
  echo "$summary"
  echo
  echo "## Codegen vs Reflect"
  echo
  echo "Ratio < 1 means codegen wins. Ratio > 1 means reflect wins."
  echo "Two shape buckets so the report shows how the gap scales"
  echo "with field count."
  echo
  echo "| Shape | Fields | Ratio (codegen ÷ reflect) |"
  echo "| --- | ---: | --- |"
  echo "| simple  (path + 1 query)              | 2 | $(ratio BenchmarkParseSimpleCodegen BenchmarkParseSimpleReflect) |"
  echo "| complex (path + query×4 + hdr + cookie + body) | 9 | $(ratio BenchmarkParseComplexCodegen BenchmarkParseComplexReflect) |"
  echo
  echo "## Raw"
  echo
  echo '```'
  cat "$raw"
  echo '```'
} > "$out"

echo "wrote $out"
