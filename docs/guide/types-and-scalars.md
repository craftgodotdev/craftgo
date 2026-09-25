# Types and Scalars

Types describe request and response shapes. Scalars are named primitives with built-in validators.

## Types

```craftgo
type CreateUserReq {
    name  string
    email string
    age   int?
}
```

Each field is `name type [decorators]`. Types compose from primitives, arrays, maps, and other types.

A field's decorators follow it: on its line, or on lines of their own below it, up to the next member. So a decorator on a line of its own between two fields is the upper field's, even across a blank line or a comment:

```craftgo
type Signup {
    email    string @format(email)
    password string
        @minLength(12)
    nickname string?
}
```

`@minLength(12)` belongs to `password`, and `craftgo fmt` moves it onto that line. A field takes decorators written above it only as the first member of its body or right below a mixin.

### Primitive types

| DSL        | Go         | Notes                                |
| ---------- | ---------- | ------------------------------------ |
| `string`   | `string`   |                                      |
| `bytes`    | `[]byte`   | base64-decoded from JSON; see `@format(raw)` below |
| `int`      | `int`      | platform-sized                       |
| `int32`    | `int32`    | explicit width                       |
| `int64`    | `int64`    |                                      |
| `uint`     | `uint`     |                                      |
| `float32`  | `float32`  |                                      |
| `float64`  | `float64`  |                                      |
| `bool`     | `bool`     |                                      |
| `datetime` | `time.Time` | RFC 3339 string in JSON; body fields only |
| `file`     | `*multipart.FileHeader` | a multipart part: a request's top-level field only, never in a response, an error body or an event payload |

#### raw encoded values: `bytes @format(raw)`

Sometimes a field carries a value your design does not own - a `jsonb` column, a nested document another system defines, a webhook body you forward on. `bytes` alone is not it: a `bytes` field is a byte STRING, base64 on the wire, and a document put through it comes back as a blob nobody downstream can read without decoding it first.

`@format(raw)` says the opposite: these bytes already ARE the value, in whatever encoding the message travels in, and the codec embeds them where the value belongs instead of encoding them again.

```craftgo
type WebhookReceived {
    id      string
    payload bytes @format(raw)
    meta    bytes? @format(raw)            // wire.Raw, omitted when nil
    trace   bytes @format(raw) @nullable   // wire.Raw, always emitted (nil sends null)
}
```

It generates [`wire.Raw`](https://pkg.go.dev/github.com/craftgodotdev/craftgo/pkg/wire) - a `[]byte` whose codec methods hand the bytes through untouched. It is its own module and imports nothing but the standard library, so a package that consumes your contract inherits that and nothing else. Every shape is `wire.Raw` - never a pointer: a nil holds absence, an explicit `null` arrives as the four bytes `null`, and the two stay apart. A pointer would lose them, because Go's JSON decoder nils a pointer on `null` without reading the value.

Three ways to carry a document, and what each costs:

| Declared | Go | What travels | What it costs |
| --- | --- | --- | --- |
| `bytes` | `[]byte` | base64 of the bytes | a reader gets a blob, not a document: no consumer can index into it and the payload grows by a third |
| `bytes @format(raw)` | `wire.Raw` | the value itself, embedded | nothing is checked, because nothing is read |
| `any` | `any` | the value, decoded and re-encoded | an explicit `null` becomes Go `nil` and encodes as an absent key (a NOT NULL violation further down), an integer past 2^53 loses digits to `float64`, and `1.50` comes back `1.5` |

Those three losses are not hypothetical: a round trip through `map[string]any` is the only thing `any` can do, and each one is a value another system already stored. `bytes @format(raw)` keeps all three, because it never looks.

What travels unchanged is the VALUE. Insignificant whitespace between tokens does not survive - Go's JSON encoder compacts what a raw value hands it - but every token, and the text of every number and string, is what arrived.

`raw` is the one `@format` that is not a check, and the one that may sit on something other than a string: it is refused on `string`, on every number, on `bool`, `any`, `datetime`, an `enum`, a declared `type`, a map and an array. On a `bytes` field it is also the only decorator that applies - no other validator has anything to measure - and `@default` is refused, because craftgo has no value to write. It is a body field: `@query`, `@header`, `@path`, `@cookie` and `@form` have no parser for one. In OpenAPI it is an unconstrained schema described as `raw encoded value`.

A scalar names the shape once:

```craftgo
scalar RawDoc bytes @format(raw)

type Photo {
    original RawDoc
    thumb    RawDoc?
}
```

`RawDoc` generates as a Go alias for `wire.Raw` (not a defined type), because the pass-through lives on that type's methods and a defined type would leave them behind.

Not to be confused with [`@json("key")`](/reference/decorator-registry), which sets a field's wire key, or with `@format(json)`, which checks that a *string* field parses as JSON.

### Optional fields

Append `?` to mark a field optional:

```craftgo
type UpdateUser {
    name string?
}
```

The Go field becomes a pointer so the JSON decoder can distinguish "absent" from "explicit zero":

```go
type UpdateUser struct {
    Name *string `json:"name,omitempty"`
}
```

### Arrays

```craftgo
type Post {
    tags string[]
    pics Picture[]
}
```

Becomes `[]string` and `[]Picture`. Arrays accept `@minItems`, `@maxItems`, `@uniqueItems`.

### Maps

```craftgo
type Settings {
    flags map<string, bool>
    quotas map<string, int>
}
```

Becomes `map[string]bool` and `map[string]int`. Keys must be a non-optional string or integer primitive, a scalar over one of those, or an enum.

### Nested types

```craftgo
type Address {
    street string
    city   string
}

type User {
    name    string
    address Address
}
```

### Generics

```craftgo
type Page<T> {
    items T[]
    total int
}

type UserList {
    page Page<User>
}
```

Generic type parameters are bare identifiers starting with an uppercase letter - no constraint or variance syntax. The Go output uses standard Go 1.18+ generics with an implicit `any` constraint; each concrete instantiation also becomes a flat schema in OpenAPI (`Page<User>` emits a component named `PageOfUser`). Inside the generic's body a type parameter hides any declaration of the same name, as in Go. `extend` only applies to `service` - there is no `extend type` / `extend enum`.

A type **argument** cannot carry a trailing `?` (`Page<User?>` is rejected): the optionality has no well-defined position once the argument is substituted into the decl's body, so the Go type and the OpenAPI schema would disagree. Declare the nullability on a concrete field of the generic instead (`type Box<T> { item T? }`, used as `Box<User>`).

A field typed by a type parameter may carry `@header` or `@cookie` (`type Paged<T> { count T @header("X-Count") items T[] }`). Each request, response or error mixin that instantiates the type is checked with its argument: `response Paged<int>` sends `X-Count` as an integer, and `response Paged<User>` is `binding/type` at the response clause.

### Mixins

Reuse another type's fields by writing its name on its own inside a type body. No special prefix - just the PascalCase identifier.

```craftgo
type Auditable {
    createdAt string
    updatedAt string
}

type Identified {
    id string
}

type User {
    Auditable
    Identified
    name string
}
```

Multiple mixins are allowed. The compact form is equivalent:

```craftgo
type User { Auditable  Identified  name string }
```

Generics work too:

```craftgo
type Page<T> {
    items T[]
    total int
}

type UserList {
    Page<User>
    requestId string
}
```

Cross-package mixins use the qualified form:

```craftgo
type User {
    shared.Auditable
    name string
}
```

#### Disambiguation

The parser reads each line in a type body and decides whether the first identifier names a field or a mixin:

1. If the next token is `.` or `<` -> mixin (qualified or generic name).
2. If the next token is a builtin primitive on the same line (`string`, `int`, `bool`, `bytes`, `float64`, ...) -> field.
3. If the first identifier starts lowercase -> field (the canonical form: `name string`).
4. Otherwise -> mixin (PascalCase identifier alone, or followed by another PascalCase identifier that is the start of the next member).

The "PascalCase + builtin -> field" carve-out lets you name a field with an exported JSON tag (`CreatedAt string`) without breaking the compact mixin form.

When the wire key is not one you can spell as a field - a contract another system owns with `OrderItem` or `snake_case` keys - keep the field name yours and set the key with `@json`:

```craftgo
type OrderCaptured {
    orderItems OrderItem[] @json("OrderItem")
    storeId    string      @json("store_id")
}
```

The Go tag, the OpenAPI document and validation messages all carry the `@json` key. It applies to body fields only; a field bound with `@path`, `@query`, `@header`, `@cookie` or `@form` names its wire location in that decorator. A field without a binding decorator that a request reads from a path variable, or from the query string of a `get`, `delete`, `head` or `options` method, is read under its own name, which its validation messages carry when no JSON value holds the field.

The recommended style is to keep field names lowercase (`createdAt string`) and reserve PascalCase for mixin references. Mixing the two on adjacent lines works, but a PascalCase field declared with a custom (non-builtin) type - e.g. `CreatedAt MyTimestamp` on its own line - is read as a mixin reference to `CreatedAt` followed by a field named `MyTimestamp`. When in doubt, write the field on its own line with a builtin or scalar-backed type.

#### Restrictions

A mixin must reference a `type` declaration. Referencing an `enum`, `error`, `scalar`, or `middleware` raises `mixin/non-type`. An unknown name raises `ref/unknown-symbol`, like any other type reference (`ref/unknown-package` when a `pkg.Type` names a package that does not exist). A mixin takes no decorators: one after it on its line is an error. A decorator on a line of its own above a mixin belongs to the field above, when there is one; at the top of the body or below another mixin it is an error. One on a line of its own below a mixin goes to the field below it.

The Go output uses struct embedding:

```go
type User struct {
    Auditable
    Identified
    Name string `json:"name"`
}
```

## Scalars

A scalar is a named primitive type with validators baked in. Every field that uses the scalar inherits its validators automatically.

### Declaration

```craftgo
scalar Email string @format(email) @maxLength(254)
scalar OrderID string @length(8, 64) @pattern("^ord_[A-Z0-9]+$")
scalar Cents int @gte(0) @multipleOf(2)
scalar Latitude float64 @gte(-90) @lte(90)
```

The DSL form is `scalar <Name> <PrimitiveType> [@validators...]`. The primitive must be one of the built-in primitives (string, bytes, int variants, float variants, bool); a scalar over `datetime`, `file` or `any` is rejected as `scalar/bad-primitive`.

### Use

```craftgo
type Order {
    id    OrderID
    email Email
    total Cents
}
```

`Order.Validate()` runs the OrderID's `@length` and `@pattern`, the Email's `@format` and `@maxLength`, and the Cents' `@gte` and `@multipleOf`. You did not repeat any of those validators on the field.

### Why scalars

Scalars centralize validation rules. Change `Email` to allow longer addresses and every field that uses it picks up the change with zero edits.

In Go output, scalars become **defined types** (not aliases), so each can carry
its own `Validate()` method holding the declared constraints:

```go
type Email string
type OrderID string
type Cents int
```

Because they are distinct types, assigning a raw `string` to an `Email` value
needs a conversion (`Email("a@b.com")`) - the small price for centralised,
method-carrying validation. Generated request structs already use the scalar
type, so wire decoding and validation stay automatic.

### Restrictions

- Scalars wrap a primitive only. Cannot wrap struct types, enums, or other scalars.
- The validators must be compatible with the primitive (`@length` on `int` is a semantic error).
- Scalar names must be unique within a package.

## Enums

See [Enums](/guide/enums).

## Errors

See [Errors](/guide/errors).
