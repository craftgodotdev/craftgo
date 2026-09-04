// Package otlpaddr holds the single rule craftgo applies to an OTLP
// endpoint string, shared by the trace and metric exporters.
package otlpaddr

import "strings"

// Base returns the endpoint options for addr in the exporter's own option
// type. A full URL lets its scheme pick transport security
// (`https://host:4317` = TLS); a bare `host:port` is dialled insecure.
//
// The rule decides TLS, so traces and metrics must not drift apart on it -
// hence one implementation, two callers.
func Base[T any](addr string, endpointURL func(string) T, endpoint func(string) T, insecure func() T) []T {
	if strings.Contains(addr, "://") {
		return []T{endpointURL(addr)}
	}
	return []T{endpoint(addr), insecure()}
}
