package telemetry

import (
	"fmt"
	"net/url"
	"strings"
)

// otlpEndpoint returns the OTLP/gRPC endpoint options for addr: a URL's scheme
// picks transport security, and a bare host:port is dialled insecure.
func otlpEndpoint[T any](addr string, endpointURL func(string) T, endpoint func(string) T, insecure func() T) []T {
	if strings.Contains(addr, "://") {
		return []T{endpointURL(addr)}
	}
	return []T{endpoint(addr), insecure()}
}

// checkOTLPHTTPEndpoint fails unless addr, the endpoint of an otlp_http exporter, is an
// http or https URL with a host.
func checkOTLPHTTPEndpoint(addr string) error {
	u, err := url.Parse(addr)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("otlp_http endpoint %q is not an http:// or https:// URL", addr)
	}
	return nil
}
