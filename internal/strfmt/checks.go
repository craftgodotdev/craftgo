package strfmt

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"net/mail"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// The functions below are the format checks. Each is copied, doc comment and name included,
// into the generated validate.go of a package whose checks call it, so they use the standard
// library and one another alone.

// _isEmail reports whether v is a bare RFC 5322 addr-spec, as JSON Schema `email` is: no
// display name, comment, angle brackets or surrounding space.
func _isEmail(v string) bool {
	a, err := mail.ParseAddress(v)
	return err == nil && a.Name == "" && a.String() == "<"+v+">"
}

// _isURL reports whether v is an http or https URI with a host.
func _isURL(v string) bool {
	u, err := url.Parse(v)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && _isURIText(v)
}

// _isURI reports whether v is an RFC 3986 URI, which has a scheme.
func _isURI(v string) bool {
	u, err := url.Parse(v)
	return err == nil && u.Scheme != "" && _isURIText(v)
}

// _isURIText reports whether v holds only the characters RFC 3986 allows in a URI, a `%` only
// before two hex digits, and one `#` at most.
func _isURIText(v string) bool {
	for i := 0; i < len(v); i++ {
		switch c := v[i]; {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		case strings.IndexByte("-._~:/?#[]@!$&'()*+,;=", c) >= 0:
		case c == '%' && i+2 < len(v) && strings.IndexByte("0123456789abcdefABCDEF", v[i+1]) >= 0 &&
			strings.IndexByte("0123456789abcdefABCDEF", v[i+2]) >= 0:
			i += 2
		default:
			return false
		}
	}
	return strings.Count(v, "#") <= 1
}

// _isDateTime reports whether v is an RFC 3339 date-time, its `T` and `Z` in either case.
func _isDateTime(v string) bool {
	return len(v) > 10 && (v[10] == 'T' || v[10] == 't') && _isDate(v[:10]) && _isTime(v[11:])
}

// _isDate reports whether v is an RFC 3339 full-date.
func _isDate(v string) bool {
	_, err := time.Parse(time.DateOnly, v)
	return err == nil
}

// _isTime reports whether v is an RFC 3339 full-time: hh:mm:ss, a `.` and fraction digits, then
// `Z` or an offset ±hh:mm, with second 60, a leap second, only at 23:59 UTC.
func _isTime(v string) bool {
	if len(v) < 9 || v[2] != ':' || v[5] != ':' {
		return false
	}
	h, m, s := _twoDigits(v[0:2]), _twoDigits(v[3:5]), _twoDigits(v[6:8])
	if h > 23 || m > 59 || s > 60 {
		return false
	}
	rest := v[8:]
	if rest[0] == '.' {
		n := 1
		for n < len(rest) && '0' <= rest[n] && rest[n] <= '9' {
			n++
		}
		if n == 1 {
			return false
		}
		rest = rest[n:]
	}
	offset := 0
	switch {
	case rest == "Z" || rest == "z":
	case len(rest) == 6 && (rest[0] == '+' || rest[0] == '-') && rest[3] == ':':
		oh, om := _twoDigits(rest[1:3]), _twoDigits(rest[4:6])
		if oh > 23 || om > 59 {
			return false
		}
		if offset = oh*60 + om; rest[0] == '-' {
			offset = -offset
		}
	default:
		return false
	}
	return s < 60 || (h*60+m-offset+24*60)%(24*60) == 23*60+59
}

// _twoDigits returns the number two decimal digits spell, or 100 for anything else.
func _twoDigits(v string) int {
	if len(v) != 2 || v[0] < '0' || v[0] > '9' || v[1] < '0' || v[1] > '9' {
		return 100
	}
	return int(v[0]-'0')*10 + int(v[1]-'0')
}

// _isPhone reports whether v, after an optional `+`, is 6 to 20 digits, spaces, parentheses
// and hyphens, at least one of them a digit.
func _isPhone(v string) bool {
	v = strings.TrimPrefix(v, "+")
	digit := false
	for i := 0; i < len(v); i++ {
		switch c := v[i]; {
		case '0' <= c && c <= '9':
			digit = true
		case c != ' ' && c != '(' && c != ')' && c != '-':
			return false
		}
	}
	return digit && len(v) >= 6 && len(v) <= 20
}

// _isIPv4 reports whether v is an IPv4 address in dotted-quad form, an IPv4-mapped IPv6
// address not included.
func _isIPv4(v string) bool {
	a, err := netip.ParseAddr(v)
	return err == nil && a.Is4()
}

// _isIPv6 reports whether v is an RFC 4291 IPv6 address without a zone, an IPv4-mapped one
// included.
func _isIPv6(v string) bool {
	a, err := netip.ParseAddr(v)
	return err == nil && a.Is6() && a.Zone() == ""
}

// _isCIDR reports whether v is an IPv4 or IPv6 address with a prefix length, as RFC 4632 and
// RFC 4291 write them.
func _isCIDR(v string) bool {
	_, _, err := net.ParseCIDR(v)
	return err == nil
}

// _isMAC reports whether v is a MAC-48, EUI-64 or 20-octet InfiniBand address.
func _isMAC(v string) bool {
	_, err := net.ParseMAC(v)
	return err == nil
}

// _isBase64 reports whether v is RFC 4648 §4 standard base64 (`+/`), padded.
func _isBase64(v string) bool {
	_, err := base64.StdEncoding.DecodeString(v)
	return err == nil
}

// _isBase64URL reports whether v is RFC 4648 §5 URL-safe base64 (`-_`), padded or, as §3.2
// allows, not.
func _isBase64URL(v string) bool {
	enc := base64.RawURLEncoding
	if len(v)%4 == 0 {
		enc = base64.URLEncoding
	}
	_, err := enc.DecodeString(v)
	return err == nil
}

// _isJSON reports whether v is an RFC 8259 JSON text.
func _isJSON(v string) bool {
	return json.Valid([]byte(v))
}
