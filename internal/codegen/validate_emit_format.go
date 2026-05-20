// Format-validator catalogue: per-@format(name) check builders + RFC references.
package codegen

import (
	"fmt"
)

// regex once at package-init via [regexRegistry] instead of compiling
// per call; stdlib-backed formats leave it empty and rely on their
// own `emit` closure.
type formatValidator struct {
	label   string
	imports []string
	pattern string
	emit    func(val, msg string) string
}

// exprFormat builds a [formatValidator] from a single Go boolean
// expression that's true-when-invalid. `condFmt` must contain exactly
// one `%s` placeholder for the value access.
func exprFormat(label string, imports []string, condFmt string) formatValidator {
	return formatValidator{
		label:   label,
		imports: imports,
		emit: func(val, msg string) string {
			cond := fmt.Sprintf(condFmt, val)
			return ifReturnf(cond, msg)
		},
	}
}

// stmtFormat builds a [formatValidator] from a Go init-statement +
// condition pair. `condFmt` must contain `%s` for the value access
// and produce text like `_, _err := f(%s); _err != nil` - the whole
// thing slots into Go's `if init; cond` form.
func stmtFormat(label string, imports []string, condFmt string) formatValidator {
	return formatValidator{
		label:   label,
		imports: imports,
		emit: func(val, msg string) string {
			cond := fmt.Sprintf(condFmt, val)
			return ifReturnf(cond, msg)
		},
	}
}

// regexFormat builds a [formatValidator] that the caller routes
// through the package-level regex registry: the pattern is interned
// once at file emit, and the check references the resulting var by
// name (e.g. `_pattern0.MatchString(v.Foo)`). The validator carries
// `regexp` in its imports so the var block stays well-formed even
// when no other emit pulls it in.
func regexFormat(label, pattern string) formatValidator {
	return formatValidator{
		label:   label,
		imports: []string{"regexp"},
		pattern: pattern,
	}
}

// formatValidators is the canonical catalogue. For RFC compliance the
// network/time/email checks delegate to the Go standard library; the
// rest stay regex for shapes where stdlib has no direct equivalent
// (UUID, hex color, hostname, phone, credit card length).
var formatValidators = map[string]formatValidator{
	// RFC 5322 email - net/mail.ParseAddress accepts the full
	// address-spec grammar (display name + addr-spec); we feed it
	// the raw string so common forms ("a@b.com", "a+tag@b.co.uk")
	// pass while obviously-malformed ones are rejected.
	"email": stmtFormat("email", []string{"net/mail"},
		`_, _err := mail.ParseAddress(%s); _err != nil`),

	// HTTP/HTTPS URLs - net/url.Parse + scheme guard. The bare
	// `url.Parse` is permissive (it accepts `mailto:`, `data:`, ...);
	// we additionally require http/https since the format name
	// implies a web URL.
	"url": stmtFormat("URL", []string{"net/url"},
		`_u, _err := url.Parse(%s); _err != nil || (_u.Scheme != "http" && _u.Scheme != "https")`),

	// RFC 3986 generic URI - any non-empty scheme.
	"uri": stmtFormat("URI", []string{"net/url"},
		`_u, _err := url.Parse(%s); _err != nil || _u.Scheme == ""`),

	// RFC 4122 UUID - format-only check (we don't enforce a
	// specific version digit; consumers can layer @pattern on top
	// when they want strict v4 etc.).
	"uuid": regexFormat("UUID",
		`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`),

	// RFC 1123 hostname - alphanumeric labels with optional hyphens
	// in the middle, separated by dots.
	"hostname": regexFormat("hostname",
		`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)*$`),

	// RFC 791 IPv4 - net.ParseIP + To4 disambiguates from the
	// IPv6 form (which net.ParseIP also accepts).
	"ipv4": stmtFormat("IPv4", []string{"net"},
		`_ip := net.ParseIP(%s); _ip == nil || _ip.To4() == nil`),

	// RFC 4291 IPv6 - parse succeeds AND not a v4 address. Handles
	// `::`, zone IDs, IPv4-mapped (`::ffff:1.2.3.4`), and shortened
	// forms — every shape a regex would miss.
	"ipv6": stmtFormat("IPv6", []string{"net"},
		`_ip := net.ParseIP(%s); _ip == nil || _ip.To4() != nil`),

	// E.164-ish phone with human-friendly separators. Stricter
	// users should add `@pattern("^\\+\\d{1,15}$")`.
	"phone": regexFormat("phone",
		`^\+?[0-9 ()-]{6,20}$`),

	// RFC 3339 date-time. time.Parse handles fractional seconds,
	// optional offset, and rejects malformed dates (Feb 30 etc.) —
	// a regex couldn't catch the latter.
	"datetime": stmtFormat("RFC 3339 datetime", []string{"time"},
		`_, _err := time.Parse(time.RFC3339, %s); _err != nil`),

	// RFC 3339 full-date.
	"date": stmtFormat("date", []string{"time"},
		`_, _err := time.Parse(time.DateOnly, %s); _err != nil`),

	// RFC 3339 partial-time. time.TimeOnly is `15:04:05`; offset
	// is not part of partial-time, so we use the dedicated layout.
	"time": stmtFormat("time", []string{"time"},
		`_, _err := time.Parse(time.TimeOnly, %s); _err != nil`),

	// RFC 4632 / RFC 4291 CIDR - net.ParseCIDR handles both v4 and
	// v6 with mask-range and octet-bound validation.
	"cidr": stmtFormat("CIDR", []string{"net"},
		`_, _, _err := net.ParseCIDR(%s); _err != nil`),

	// MAC-48 / EUI-64 / 20-octet InfiniBand - net.ParseMAC accepts
	// `:`-separated, `-`-separated, and dot-separated forms across
	// all three lengths.
	"mac": stmtFormat("MAC address", []string{"net"},
		`_, _err := net.ParseMAC(%s); _err != nil`),

	// Length-only credit card number sanity. Luhn checksum needs
	// loop logic (not a single expression); pair with custom logic
	// when stricter validation matters.
	"creditcard": regexFormat("credit card number",
		`^[0-9]{12,19}$`),

	// RFC 4648 §4 standard base64 (with `+/=`). Use `base64url`
	// for the URL-safe alphabet.
	"base64": stmtFormat("base64", []string{"encoding/base64"},
		`_, _err := base64.StdEncoding.DecodeString(%s); _err != nil`),

	// RFC 4648 §5 URL-safe base64 (with `-_=`).
	"base64url": stmtFormat("base64url", []string{"encoding/base64"},
		`_, _err := base64.URLEncoding.DecodeString(%s); _err != nil`),

	// CSS hex color (3 or 6 hex digits, optional `#` prefix).
	"hexcolor": regexFormat("hex color",
		`^#?[0-9a-fA-F]{3}([0-9a-fA-F]{3})?$`),

	// RFC 8259 JSON - json.Valid does a full structural parse so
	// bad escapes, unbalanced brackets, etc. all get caught. A
	// "non-empty" regex would let them through.
	"json": exprFormat("JSON", []string{"encoding/json"},
		`!json.Valid([]byte(%s))`),
}
