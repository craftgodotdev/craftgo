// Package errcat holds the canonical catalogue of reserved HTTP error
// categories. It is a leaf below the parser (which rejects an unknown
// category), codegen (which emits each category's HTTP status + default
// message), and the LSP (which validates and offers them as completions), so a
// new category is added in exactly ONE place instead of four. Source: the
// project README §"Error categories".
package errcat

// Category is one reserved error category: its DSL name, HTTP status code, and
// the default human-readable message codegen uses for the runtime Message.
type Category struct {
	Name    string
	Status  int
	Message string
}

// Categories is the canonical catalogue in status order. The LSP renders
// `error <cursor>` completions in this order.
var Categories = []Category{
	{"BadRequest", 400, "Bad request"},
	{"Unauthorized", 401, "Unauthorized"},
	{"PaymentRequired", 402, "Payment required"},
	{"Forbidden", 403, "Forbidden"},
	{"NotFound", 404, "Not found"},
	{"MethodNotAllowed", 405, "Method not allowed"},
	{"NotAcceptable", 406, "Not acceptable"},
	{"Conflict", 409, "Conflict"},
	{"Gone", 410, "Resource gone"},
	{"LengthRequired", 411, "Length required"},
	{"PreconditionFailed", 412, "Precondition failed"},
	{"PayloadTooLarge", 413, "Payload too large"},
	{"UnsupportedMediaType", 415, "Unsupported media type"},
	{"UnprocessableEntity", 422, "Unprocessable entity"},
	{"Locked", 423, "Resource locked"},
	{"TooManyRequests", 429, "Too many requests"},
	{"Internal", 500, "Internal server error"},
	{"NotImplemented", 501, "Not implemented"},
	{"BadGateway", 502, "Bad gateway"},
	{"ServiceUnavailable", 503, "Service unavailable"},
	{"GatewayTimeout", 504, "Gateway timeout"},
}

var byName = func() map[string]Category {
	m := make(map[string]Category, len(Categories))
	for _, c := range Categories {
		m[c.Name] = c
	}
	return m
}()

// IsCategory reports whether name is a reserved error category.
func IsCategory(name string) bool {
	_, ok := byName[name]
	return ok
}

// Status returns the HTTP status code for a category, or 0 if unknown.
func Status(name string) int { return byName[name].Status }

// Message returns the default human-readable message for a category, or "" if
// unknown.
func Message(name string) string { return byName[name].Message }
