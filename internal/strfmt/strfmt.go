// Package strfmt is the catalogue of named string formats `@format` accepts:
// each one's OpenAPI `format` keyword and the Go check generated for it.
package strfmt

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"net/mail"
	"net/url"
	"regexp"
	"time"
)

// Spec is one named string format.
type Spec struct {
	Name  string // the `@format` argument
	Label string // fills the validation message "not a valid <Label>"
	OAS   string // the OpenAPI `format` keyword when it differs from Name
	// Imports are the Go packages the generated check needs.
	Imports []string
	// Cond is a Go condition, true when the value is invalid, with one %s for
	// the value; it may open with an `if` init statement.
	Cond string
	// Pattern is the regular expression the value must match when Cond is empty.
	Pattern string
	// valid is Cond as a function: it accepts what Cond does not reject.
	valid func(string) bool
}

// Valid reports whether v passes the check generated for s: its Pattern, or
// its Cond.
func (s Spec) Valid(v string) bool {
	if s.Pattern != "" {
		return regexp.MustCompile(s.Pattern).MatchString(v)
	}
	return s.valid(v)
}

// All lists every format.
var All = []Spec{
	// RFC 5322 address, parsed by net/mail.
	{Name: "email", Label: "email", Imports: []string{"net/mail"},
		Cond:  `_, _err := mail.ParseAddress(%s); _err != nil`,
		valid: func(v string) bool { _, err := mail.ParseAddress(v); return err == nil }},
	// HTTP or HTTPS URL: url.Parse also accepts other schemes, so the scheme is checked.
	{Name: "url", Label: "URL", Imports: []string{"net/url"},
		Cond: `_u, _err := url.Parse(%s); _err != nil || (_u.Scheme != "http" && _u.Scheme != "https")`,
		valid: func(v string) bool {
			u, err := url.Parse(v)
			return err == nil && (u.Scheme == "http" || u.Scheme == "https")
		}},
	// RFC 3986 URI with any non-empty scheme.
	{Name: "uri", Label: "URI", Imports: []string{"net/url"},
		Cond:  `_u, _err := url.Parse(%s); _err != nil || _u.Scheme == ""`,
		valid: func(v string) bool { u, err := url.Parse(v); return err == nil && u.Scheme != "" }},
	// RFC 4122 UUID layout; the version digit is not checked.
	{Name: "uuid", Label: "UUID",
		Pattern: `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`},
	// RFC 3339 date-time, spelled `date-time` in OpenAPI.
	{Name: "datetime", Label: "RFC 3339 datetime", OAS: "date-time", Imports: []string{"time"},
		Cond:  `_, _err := time.Parse(time.RFC3339, %s); _err != nil`,
		valid: func(v string) bool { _, err := time.Parse(time.RFC3339, v); return err == nil }},
	// RFC 3339 full-date.
	{Name: "date", Label: "date", Imports: []string{"time"},
		Cond:  `_, _err := time.Parse(time.DateOnly, %s); _err != nil`,
		valid: func(v string) bool { _, err := time.Parse(time.DateOnly, v); return err == nil }},
	// RFC 3339 partial-time: `15:04:05`, no offset.
	{Name: "time", Label: "time", Imports: []string{"time"},
		Cond:  `_, _err := time.Parse(time.TimeOnly, %s); _err != nil`,
		valid: func(v string) bool { _, err := time.Parse(time.TimeOnly, v); return err == nil }},
	// E.164-like phone number with separators allowed.
	{Name: "phone", Label: "phone", Pattern: `^\+?[0-9 ()-]{6,20}$`},
	// RFC 791 IPv4; To4 rejects the IPv6 forms ParseIP also accepts.
	{Name: "ipv4", Label: "IPv4", Imports: []string{"net"},
		Cond:  `_ip := net.ParseIP(%s); _ip == nil || _ip.To4() == nil`,
		valid: func(v string) bool { ip := net.ParseIP(v); return ip != nil && ip.To4() != nil }},
	// RFC 4291 IPv6: any address ParseIP accepts that is not IPv4.
	{Name: "ipv6", Label: "IPv6", Imports: []string{"net"},
		Cond:  `_ip := net.ParseIP(%s); _ip == nil || _ip.To4() != nil`,
		valid: func(v string) bool { ip := net.ParseIP(v); return ip != nil && ip.To4() == nil }},
	// RFC 4632 / RFC 4291 CIDR, IPv4 or IPv6.
	{Name: "cidr", Label: "CIDR", Imports: []string{"net"},
		Cond:  `_, _, _err := net.ParseCIDR(%s); _err != nil`,
		valid: func(v string) bool { _, _, err := net.ParseCIDR(v); return err == nil }},
	// MAC-48, EUI-64 or 20-octet InfiniBand address.
	{Name: "mac", Label: "MAC address", Imports: []string{"net"},
		Cond:  `_, _err := net.ParseMAC(%s); _err != nil`,
		valid: func(v string) bool { _, err := net.ParseMAC(v); return err == nil }},
	// Card number length only; no Luhn checksum.
	{Name: "creditcard", Label: "credit card number", Pattern: `^[0-9]{12,19}$`},
	// RFC 4648 §4 standard base64 (`+/=`).
	{Name: "base64", Label: "base64", Imports: []string{"encoding/base64"},
		Cond:  `_, _err := base64.StdEncoding.DecodeString(%s); _err != nil`,
		valid: func(v string) bool { _, err := base64.StdEncoding.DecodeString(v); return err == nil }},
	// RFC 4648 §5 URL-safe base64 (`-_=`).
	{Name: "base64url", Label: "base64url", Imports: []string{"encoding/base64"},
		Cond:  `_, _err := base64.URLEncoding.DecodeString(%s); _err != nil`,
		valid: func(v string) bool { _, err := base64.URLEncoding.DecodeString(v); return err == nil }},
	// CSS hex color: 3 or 6 hex digits, optional `#`.
	{Name: "hexcolor", Label: "hex color", Pattern: `^#?[0-9a-fA-F]{3}([0-9a-fA-F]{3})?$`},
	// RFC 8259 JSON text.
	{Name: "json", Label: "JSON", Imports: []string{"encoding/json"},
		Cond:  `!json.Valid([]byte(%s))`,
		valid: func(v string) bool { return json.Valid([]byte(v)) }},
}

// Names returns every format name, in [All] order.
func Names() []string {
	out := make([]string, len(All))
	for i, s := range All {
		out[i] = s.Name
	}
	return out
}

// Lookup returns the spec for name.
func Lookup(name string) (Spec, bool) {
	for _, s := range All {
		if s.Name == name {
			return s, true
		}
	}
	return Spec{}, false
}

// OpenAPIFormat returns the OpenAPI `format` keyword for name: the spec's OAS
// when set, else name itself.
func OpenAPIFormat(name string) string {
	if s, ok := Lookup(name); ok && s.OAS != "" {
		return s.OAS
	}
	return name
}
