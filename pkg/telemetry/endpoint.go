package telemetry

import "strings"

// otlpEndpoint returns the endpoint options for addr in the exporter's own
// option type - the one rule both signals apply to an OTLP endpoint. A
// full URL lets its scheme pick transport security (`https://host:4317`
// is TLS); a bare `host:port` is dialled insecure, the convention for a
// collector on a trusted local network.
func otlpEndpoint[T any](addr string, endpointURL func(string) T, endpoint func(string) T, insecure func() T) []T {
	if strings.Contains(addr, "://") {
		return []T{endpointURL(addr)}
	}
	return []T{endpoint(addr), insecure()}
}
