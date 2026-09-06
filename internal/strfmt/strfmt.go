// Package strfmt is the catalogue of named string formats `@format`
// accepts: for each, the OpenAPI `format` keyword, the Go imports the
// generated check needs and the check itself. It is a leaf below the
// analyser (which accepts exactly these names), codegen (which emits the
// check and the keyword) and the LSP (which offers the names), so a
// format is defined in exactly one place.
//
// (Named `strfmt`, not `formats`, to stay clearly distinct from the
// `internal/format` printer package that renders DSL source.)
package strfmt

// Spec is one named string format.
type Spec struct {
	Name  string // the `@format` argument
	Label string // the human label of the validation message: "not a valid <Label>"
	OAS   string // the OpenAPI `format` keyword when it differs from Name
	// Imports are the Go packages the generated check needs.
	Imports []string
	// Cond is a Go condition template that is true when the value is
	// INVALID, with one %s for the value expression. It may open with an
	// init statement (`_, _err := f(%s); _err != nil`), which slots into
	// Go's `if init; cond` form. Empty for a regex-backed format.
	Cond string
	// Pattern is the regular expression the value must match; the
	// generated validator compiles it once per file. Empty for a
	// stdlib-backed format.
	Pattern string
}

// All lists every format in documentation order (README §"Decorators by
// level").
var All = []Spec{
	// RFC 5322 email: net/mail.ParseAddress accepts the full address-spec
	// grammar, so common forms ("a@b.com", "a+tag@b.co.uk") pass while
	// malformed ones are rejected.
	{Name: "email", Label: "email", Imports: []string{"net/mail"},
		Cond: `_, _err := mail.ParseAddress(%s); _err != nil`},
	// HTTP/HTTPS URL: net/url.Parse is permissive (it accepts `mailto:`,
	// `data:`, ...), so the scheme is checked as well.
	{Name: "url", Label: "URL", Imports: []string{"net/url"},
		Cond: `_u, _err := url.Parse(%s); _err != nil || (_u.Scheme != "http" && _u.Scheme != "https")`},
	// RFC 3986 generic URI: any non-empty scheme.
	{Name: "uri", Label: "URI", Imports: []string{"net/url"},
		Cond: `_u, _err := url.Parse(%s); _err != nil || _u.Scheme == ""`},
	// RFC 4122 UUID, format only: the version digit is not enforced, a
	// @pattern on top makes it strict.
	{Name: "uuid", Label: "UUID",
		Pattern: `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`},
	// RFC 3339 date-time, spelled `datetime` in the DSL and `date-time` in
	// OpenAPI. time.Parse handles fractional seconds and offsets and
	// rejects impossible dates (Feb 30), which a regex cannot.
	{Name: "datetime", Label: "RFC 3339 datetime", OAS: "date-time", Imports: []string{"time"},
		Cond: `_, _err := time.Parse(time.RFC3339, %s); _err != nil`},
	// RFC 3339 full-date.
	{Name: "date", Label: "date", Imports: []string{"time"},
		Cond: `_, _err := time.Parse(time.DateOnly, %s); _err != nil`},
	// RFC 3339 partial-time: `15:04:05`, no offset.
	{Name: "time", Label: "time", Imports: []string{"time"},
		Cond: `_, _err := time.Parse(time.TimeOnly, %s); _err != nil`},
	// E.164-ish phone with human-friendly separators; `@pattern("^\\+\\d{1,15}$")`
	// for the strict form.
	{Name: "phone", Label: "phone", Pattern: `^\+?[0-9 ()-]{6,20}$`},
	// RFC 791 IPv4: net.ParseIP + To4 tells it apart from the IPv6 form
	// ParseIP also accepts.
	{Name: "ipv4", Label: "IPv4", Imports: []string{"net"},
		Cond: `_ip := net.ParseIP(%s); _ip == nil || _ip.To4() == nil`},
	// RFC 4291 IPv6: parses and is not a v4 address. Covers `::`, zone ids,
	// IPv4-mapped and shortened forms - every shape a regex would miss.
	{Name: "ipv6", Label: "IPv6", Imports: []string{"net"},
		Cond: `_ip := net.ParseIP(%s); _ip == nil || _ip.To4() != nil`},
	// RFC 4632 / RFC 4291 CIDR: v4 and v6 with mask-range and octet-bound
	// validation.
	{Name: "cidr", Label: "CIDR", Imports: []string{"net"},
		Cond: `_, _, _err := net.ParseCIDR(%s); _err != nil`},
	// MAC-48 / EUI-64 / 20-octet InfiniBand: `:`, `-` and dot separated
	// forms across all three lengths.
	{Name: "mac", Label: "MAC address", Imports: []string{"net"},
		Cond: `_, _err := net.ParseMAC(%s); _err != nil`},
	// Length-only credit card number sanity; a Luhn checksum needs a loop,
	// so it stays in hand-written logic.
	{Name: "creditcard", Label: "credit card number", Pattern: `^[0-9]{12,19}$`},
	// RFC 4648 §4 standard base64 (`+/=`).
	{Name: "base64", Label: "base64", Imports: []string{"encoding/base64"},
		Cond: `_, _err := base64.StdEncoding.DecodeString(%s); _err != nil`},
	// RFC 4648 §5 URL-safe base64 (`-_=`).
	{Name: "base64url", Label: "base64url", Imports: []string{"encoding/base64"},
		Cond: `_, _err := base64.URLEncoding.DecodeString(%s); _err != nil`},
	// CSS hex color: 3 or 6 hex digits, optional `#`.
	{Name: "hexcolor", Label: "hex color", Pattern: `^#?[0-9a-fA-F]{3}([0-9a-fA-F]{3})?$`},
	// RFC 8259 JSON: json.Valid does a full structural parse, so bad
	// escapes and unbalanced brackets are caught.
	{Name: "json", Label: "JSON", Imports: []string{"encoding/json"},
		Cond: `!json.Valid([]byte(%s))`},
}

// Names returns every format name in documentation order.
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

// OpenAPIFormat returns the OpenAPI `format` keyword for name: the
// catalogue's keyword where it differs from the DSL spelling, the name
// itself otherwise.
func OpenAPIFormat(name string) string {
	if s, ok := Lookup(name); ok && s.OAS != "" {
		return s.OAS
	}
	return name
}
