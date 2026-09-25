package telemetry

import (
	"fmt"
	"net/url"
	"strings"
)

// otlpGRPCEndpoint returns the OTLP/gRPC options for addr: none when it is empty, leaving the
// exporter's own default; a URL's scheme picks transport security, a bare host:port is insecure.
func otlpGRPCEndpoint[T any](addr string, endpointURL func(string) T, endpoint func(string) T, insecure func() T) ([]T, error) {
	switch {
	case addr == "":
		return nil, nil
	case !strings.Contains(addr, "://"):
		return []T{endpoint(addr), insecure()}, nil
	}
	if u, err := url.Parse(addr); err == nil && u.Host != "" {
		return []T{endpointURL(addr)}, nil
	}
	return nil, fmt.Errorf("otlp_grpc endpoint %q is neither a host:port nor a URL with a host", addr)
}

// otlpHTTPEndpoint returns the OTLP/HTTP options for addr: none when it is empty, leaving the
// exporter's own default; a path of "/" is dropped, so each signal is sent to its own path.
func otlpHTTPEndpoint[T any](addr string, endpointURL func(string) T) ([]T, error) {
	if addr == "" {
		return nil, nil
	}
	u, err := url.Parse(addr)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("otlp_http endpoint %q is not an http:// or https:// URL", addr)
	}
	if u.Path == "/" {
		u.Path = ""
		addr = u.String()
	}
	return []T{endpointURL(addr)}, nil
}
