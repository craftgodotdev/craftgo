# Validators

Validators are decorators that constrain field values. They live in the DSL and run at request time as plain Go code - no reflection, no struct tags, no runtime cost beyond the comparisons themselves.

## At a glance

```craftgo
type CreateUserReq {
    name  string @length(1, 80)
    email string @format(email)
    age   int?   @gte(0) @lte(150)
}
```

15+ built-in validators cover strings (length, pattern, format), numbers (min, max, range), arrays (minItems, maxItems, uniqueItems), and cross-field rules (`@requiresOneOf`, `@mutuallyExclusive`).

The handler calls `req.Validate()` after JSON decode and before your business logic. If validation fails, the handler returns a 400 with a structured message. Your service code never runs with bad input.

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
func (v *CreateUserReq) Validate() error {
    if l := utf8.RuneCountInString(v.Name); l < 1 || l > 80 {
        return fmt.Errorf("name: length out of range [1, 80]")
    }
    if _, err := mail.ParseAddress(v.Email); err != nil {
        return fmt.Errorf("email: not a valid email")
    }
    if v.Age != nil {
        if *v.Age < 0 {
            return fmt.Errorf("age: below minimum 0")
        }
        if *v.Age > 150 {
            return fmt.Errorf("age: above maximum 150")
        }
    }
    return nil
}
```

Plain Go. No reflection. No struct tag parsing. The handler calls `req.Validate()` after JSON decode and before your business logic.

::: tip Required-by-default
A non-optional field (no `?`) is required, but craftgo only emits an explicit presence check when the field has a meaningful empty value the JSON decoder accepts (e.g. `any`, an enum). For a plain `string`, the decoder already rejects a literal `null`, and an empty `""` is a legal value unless you add `@length` / `@minLength`. That's why `name` above shows only its `@length` check, not a separate "required" line. String lengths count **characters** (`utf8.RuneCountInString`), matching `minLength`/`maxLength` in the OpenAPI spec. `@format(email)` delegates to `net/mail.ParseAddress`; the regex-backed formats (uuid, phone, …) compile their pattern once into a package-level var, not per call.
:::

## Built-in validators

> **Required-by-default**: every field gets an automatic presence check unless the type carries `?`. No `@required` decorator - use `?` to opt-out, `@nullable` to keep the field mandatory while allowing JSON `null`, `@default(...)` to pre-fill when absent (auto-marks optional on save).

The tables below cover validators with the examples that matter for *validation*. For the one-grid lookup of every decorator (including non-validator ones) and its legal levels, see the [Decorator Registry](/reference/decorator-registry).

### Strings

| Decorator                   | Effect                                                |
| --------------------------- | ----------------------------------------------------- |
| `@length(min, max)`         | Character count in `[min, max]`                       |
| `@minLength(n)`             | At least `n` characters                               |
| `@maxLength(n)`             | At most `n` characters                                |
| `@pattern("regex")`         | Must match `regexp`                                   |
| `@format(name)`             | Built-in format check (see below)                     |

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
    quantity int   @positive @lte(1000)
    price    int   @gte(0) @multipleOf(2)
    rating   float @range(0.0, 5.0)
}
```

### Arrays

| Decorator                   | Effect                                |
| --------------------------- | ------------------------------------- |
| `@minItems(n)`              | At least `n` elements                 |
| `@maxItems(n)`              | At most `n` elements                  |
| `@uniqueItems`              | All elements distinct                 |

```craftgo
type Post {
    tags string[] @minItems(1) @maxItems(10) @uniqueItems
}
```

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

A `T?` field becomes `*T` in Go. Validators only fire when the pointer is non-nil:

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
    page     int     @default(1)
    pageSize int     @default(20) @gte(1) @lte(100)
    sort     string? @default("created_at")
}
```

`@default` works on primitives, scalars, enums, and arrays of those. The field must be optional (`?`) for the default to fire - the formatter auto-adds `?` on save when missing, and the semantic analyzer warns until you save.

## Error messages

Generated messages follow the shape:

```
<field>: <reason>
```

`Validate()` is **fail-fast** - it returns the **first** violation it hits and stops, so a request with several problems surfaces one message at a time:

```
name: length out of range [1, 80]
```

Reason strings are fixed per validator: `length out of range [lo, hi]`, `does not match pattern`, `below minimum N`, `above maximum N`, `out of range [lo, hi]` (for `@range`), `must be a multiple of N`, `maxItems N`, `items must be unique`, etc.

Customize the response by overriding `server.SetDefaultValidationFailed`:

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

The DSL ships a closed set of validators. To add a project-specific check, validate inside your business logic:

```go
func (s *Service) CreateUser(ctx context.Context, req *types.CreateUserReq) (*types.User, error) {
    if err := req.Validate(); err != nil {
        return nil, err
    }
    if !s.svcCtx.AllowList.Contains(req.Email) {
        return nil, types.NewBadRequestErr(types.BadRequestBody{
            Message: "email domain not allowed",
        })
    }
    // ...
}
```

For checks that should live closer to the schema (uniqueness, foreign keys, business rules), the typed error pattern keeps the response shape clean.
