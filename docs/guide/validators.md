# Validators

Validators are decorators that constrain field values. They live in the DSL and run at request time as plain Go code - no struct tags, no runtime cost beyond the comparisons themselves, and reflection only for a generic type's type-parameter fields.

## At a glance

```craftgo
type CreateUserReq {
    name  string @length(1, 80)
    email string @format(email)
    age   int?   @gte(0) @lte(150)
}
```

15+ built-in validators cover strings (length, pattern, format), numbers (gte, lte, gt, lt, range, positive, negative, multipleOf), arrays (minItems, maxItems, uniqueItems), and cross-field rules (`@requiresOneOf`, `@mutuallyExclusive`).

The handler calls `req.Validate()` after JSON decode and before your business logic. If validation fails, the handler answers 400 `{"message":"<field>: <reason>"}`. Your service code never runs with bad input.

The rest of this page covers each validator family with examples.

## How it works

You write:

```craftgo
type CreateUserReq {
    name  string @length(1, 80)
    email string @format(email)
    age   int?   @gte(0) @lte(150)
}
```

craftgo generates:

```go
// Validate returns the first constraint v violates, or nil.
func (v *CreateUserReq) Validate() error {
	if l := utf8.RuneCountInString(v.Name); l < 1 || l > 80 {
		return fmt.Errorf("name: length out of range [1, 80]")
	}
	if _, _err := mail.ParseAddress(v.Email); _err != nil {
		return fmt.Errorf("email: not a valid email")
	}
	if v.Age != nil && *v.Age < 0 {
		return fmt.Errorf("age: below minimum 0")
	}
	if v.Age != nil && *v.Age > 150 {
		return fmt.Errorf("age: above maximum 150")
	}
	return nil
}
```

Plain Go, with no struct tag parsing. The handler calls `req.Validate()` after JSON decode and before your business logic.

::: tip Required-by-default
A non-optional field (no `?`) is required in the OpenAPI schema, but craftgo only emits an explicit presence check when the field has a meaningful empty value the JSON decoder accepts (e.g. `any`, an enum). For a plain `string`, a missing key and a literal `null` both leave `""`, a legal value unless you add `@length` / `@minLength`. That's why `name` above shows only its `@length` check, not a separate "required" line. String lengths count **characters** (`utf8.RuneCountInString`), matching `minLength`/`maxLength` in the OpenAPI spec. `@format(email)` delegates to `net/mail.ParseAddress`; the regex-backed formats (uuid, phone, …) compile their pattern once into a package-level var, not per call.
:::

## Built-in validators

> **Required-by-default**: a field without `?` is required in the OpenAPI schema. At runtime a missing key decodes to its zero value, so only an enum (whose zero value is not a member), a `file`, an `any` and a `bytes @format(raw)` field get a presence check (`<field>: required`). No `@required` decorator - use `?` to opt out, `@nullable` to allow JSON `null` (the schema keeps it required; nothing checks the key at runtime), `@default(...)` to pre-fill when absent (auto-marks optional on save).

The tables below cover validators with the examples that matter for *validation*. For the one-grid lookup of every decorator (including non-validator ones) and its legal levels, see the [Decorator Registry](/reference/decorator-registry).

### Strings

| Decorator                   | Effect                                                |
| --------------------------- | ----------------------------------------------------- |
| `@length(min, max)`         | Character count in `[min, max]`                       |
| `@length(n)`                | Exactly `n` characters                                |
| `@minLength(n)`             | At least `n` characters                               |
| `@maxLength(n)`             | At most `n` characters                                |
| `@pattern("regex")`         | Must match `regexp`                                   |
| `@format(name)`             | Built-in format check (see below)                     |

`@length`, `@minLength` and `@maxLength` also apply to `bytes`, counted in bytes.

Built-in formats: `email`, `url`, `uri`, `uuid`, `datetime` (RFC 3339), `date`, `time`, `phone`, `ipv4`, `ipv6`, `cidr`, `mac`, `creditcard`, `base64`, `base64url`, `hexcolor`, `json`. Most delegate to the Go standard library - `email` (`net/mail`), `url`/`uri` (`net/url`), `ipv4`/`ipv6`/`cidr`/`mac` (`net`), `datetime`/`date`/`time` (`time`), `base64`/`base64url` (`encoding/base64`), `json` (`encoding/json`); the remainder (`uuid`, `phone`, `creditcard`, `hexcolor`) use a compiled regex.

```craftgo
type Profile {
    email   string @format(email)
    website string @format(uri)
    avatar  string @pattern("^https://.*\\.(png|jpg)$")
}
```

### Numbers

| Decorator                   | Effect                                |
| --------------------------- | ------------------------------------- |
| `@gte(n)`                   | Value `>= n` (inclusive)              |
| `@lte(n)`                   | Value `<= n` (inclusive)              |
| `@gt(n)`                    | Value `> n` (strict)                  |
| `@lt(n)`                    | Value `< n` (strict)                  |
| `@range(min, max)`          | Both bounds, inclusive                |
| `@positive`                 | `> 0` (alias for `@gt(0)`)            |
| `@negative`                 | `< 0` (alias for `@lt(0)`)            |
| `@multipleOf(n)`            | Divisible by `n` (integers only)      |

```craftgo
type Order {
    quantity int     @positive @lte(1000)
    price    int     @gte(0) @multipleOf(2)
    rating   float64 @range(0.0, 5.0)
}
```

### Arrays

| Decorator                   | Effect                                |
| --------------------------- | ------------------------------------- |
| `@minItems(n)`              | At least `n` elements (arrays and maps) |
| `@maxItems(n)`              | At most `n` elements (arrays and maps)  |
| `@uniqueItems`              | All elements distinct                 |

```craftgo
type Post {
    tags string[] @minItems(1) @maxItems(10) @uniqueItems
}
```

`@uniqueItems` compares elements by value, so each element must be a primitive, an enum, a scalar or a type whose members all are. An element holding an optional or `@nullable` member, a `file`, `bytes`, `any`, a `datetime`, an array or a map is rejected with `decorator/typemismatch`; a `datetime` carries its time zone, so two equal instants would count as distinct.

### Cross-field

| Decorator                          | Effect                                              |
| ---------------------------------- | --------------------------------------------------- |
| `@requiresOneOf(a, b, c)`          | At least one of named fields must be set            |
| `@mutuallyExclusive(a, b)`         | At most one of named fields can be set              |

```craftgo
@requiresOneOf(email, phone)
@mutuallyExclusive(personal, business)
type Contact {
    email     string?
    phone     string?
    personal  bool?
    business  bool?
}
```

These attach to the type, not a field. The validator surfaces a single message.
Every referenced field must be optional (`?`) or `@nullable` - a plain field, a
wire parameter (`@query` / `@header` / …), a `@default` or `@sensitive` field,
and a collection are rejected, since their runtime presence can't match the
spec's present-and-non-null check.

## Optional fields

A `T?` field becomes `*T` in Go for a primitive, scalar, enum, struct or `datetime`; an optional array, map, `bytes`, `any` or raw field keeps its Go type, nil when absent, and gains `omitempty`. Validators only fire when the value is present:

```craftgo
type UpdateUser {
    name string? @length(1, 80)
}
```

Sending `{"name": null}` or omitting `name` skips the length check. Sending `{"name": "alice"}` runs it.

## Scalars carry validators

Scalars let you bake validators into a named primitive:

```craftgo
scalar Email string @format(email) @maxLength(254)

type User { email Email }
```

`User.Validate()` runs the format and length checks on `email` because the scalar's validators inherit. No need to repeat them on every field.

## File uploads

Multipart fields use `@maxSize` and `@mimeTypes`:

```craftgo
type AvatarReq {
    userId string @path
    file   file   @form @maxSize(2MB) @mimeTypes(["image/png", "image/jpeg"])
}
```

## Default values

`@default` provides a fallback when the client omits a field. The handler pre-fills the request struct before JSON decode, so omitted fields keep the default; explicit values overwrite.

```craftgo
type ListUsersReq {
    page     int?    @default(1)
    pageSize int?    @default(20) @gte(1) @lte(100)
    sort     string? @default("created_at")
}
```

`@default` works on primitives (not `bytes`, `datetime`, `file` or `any`), scalars over them, enums, and single-level arrays of those. The handler pre-fills the field whether or not it carries `?`, but a field with a default is optional in meaning, so the analyzer warns (`decorator/default-needs-optional`) until it carries `?` - the formatter adds it on save - so types.go, validate.go and the OpenAPI agree it is optional. The default must pass the field's validators, its scalar's included - the handler validates the pre-filled value - so a default that breaks one is `decorator/conflict`.

## Error messages

The handler answers a failed check with 400 `{"message":"<message>"}` (`application/json; charset=utf-8`). Generated messages follow the shape:

```
<field>: <reason>
```

`<field>` is the name on the wire: the `@json` key of a body field, the name a binding decorator gives (`@header("X-Count")` → `X-Count: below minimum 1`), and the field's own name for a field read from the path or the query string without a binding decorator. A failure inside a nested type, an array or map element, or a type-parameter value is prefixed by each field on the way, with no index or key:

```
home: rooms: furniture: name: length less than 1
```

A mixin's fields keep their bare names. A cross-field group has no subject and lists its members by the names their own messages carry:

```
requiresOneOf [email phone] - at least one must be set
mutuallyExclusive [personal business] - at most one may be set
```

`Validate()` is **fail-fast** - it returns the **first** violation it hits and stops, so a request with several problems surfaces one message at a time:

```
name: length out of range [1, 80]
```

Reason strings are fixed per validator: `length out of range [lo, hi]`, `length must be N`, `length less than N`, `length greater than N`, `does not match pattern`, `not a valid <format>` (`not a valid email`), `below minimum N`, `above maximum N`, `must be greater than N`, `must be less than N`, `out of range [lo, hi]` (for `@range`), `must be positive`, `must be negative`, `must be a multiple of N`, `minItems N`, `maxItems N`, `items must be unique`, `file size exceeds N bytes`, `disallowed content type`, `required`, and `must be one of [v1 v2 ...]` (an enum value outside its set: the wire values, integers in decimal).

A body value of the wrong JSON type fails before `Validate()` runs, at its JSON path (wire names joined by `.`, `body` for the root): `age: expected integer, got string`, `age: expected integer, got number 1.5`, `home.rooms.furniture.name: expected string, got number`, `body: expected object, got array`. A number too large for its field reads `age: 99999999999999999999 is out of range`, and a map key that is not an integer reads `counts: "x" is not an integer`. Malformed JSON answers the decoder's own text (`unexpected EOF`). A codec installed with `server.SetGlobalJSONCodec` reports its own errors.

Customize the response by overriding `server.SetDefaultValidationFailed`; it also receives the JSON decode and parameter-binding errors, while a body read past its cap answers 413 without calling it:

```go
server.SetDefaultValidationFailed(func(w http.ResponseWriter, r *http.Request, err error) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusBadRequest)
    json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
})
```

## Validators on response types

`Validate()` exists on every type, including responses. craftgo does not call it on response types automatically - that would charge runtime cost for trusted server output. If you want belt-and-braces, call it yourself before encoding.

## Adding a custom validator

The DSL ships a closed set of validators. To add a project-specific check, validate inside your business logic - the handler has already run `req.Validate()` by then:

```go
func (l *CreateUserService) CreateUser(req *types.CreateUserReq) (*types.User, error) {
	if !l.svcCtx.AllowList.Contains(req.Email) {
		return nil, types.NewBadRequestErr(types.BadRequestBody{
			Message: "email domain not allowed",
		})
	}
	// ...
}
```

with `error BadRequest BadRequest { message string }` in the design and an `AllowList` field of your own on `ServiceContext`.

For checks that should live closer to the schema (uniqueness, foreign keys, business rules), the typed error pattern keeps the response shape clean.
