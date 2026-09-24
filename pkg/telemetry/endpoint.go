package telemetry

import "strings"

// otlpEndpoint returns the OTLP/gRPC endpoint options for addr: a URL's scheme
// picks transport security, and a bare host:port is dialled insecure.
func otlpEndpoint[T any](addr string, endpointURL func(string) T, endpoint func(string) T, insecure func() T) []T {
	if strings.Contains(addr, "://") {
		return []T{endpointURL(addr)}
	}
	return []T{endpoint(addr), insecure()}
}
