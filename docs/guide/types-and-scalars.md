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

### Primitive types

| DSL        | Go         | Notes                                |
| ---------- | ---------- | ------------------------------------ |
| `string`   | `string`   |                                      |
| `bytes`    | `[]byte`   | base64-decoded from JSON             |
| `int`      | `int`      | platform-sized                       |
| `int32`    | `int32`    | explicit width                       |
| `int64`    | `int64`    |                                      |
| `uint`     | `uint`     |                                      |
| `float32`  | `float32`  |                                      |
| `float64`  | `float64`  |                                      |
| `bool`     | `bool`     |                                      |
| `file`     | `*multipart.FileHeader` | only valid with `@form` |

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

Becomes `map[string]bool` and `map[string]int`. Keys must be primitives.

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

Generic type parameters are bare identifiers — no constraint or variance syntax. The Go output uses standard Go 1.18+ generics with an implicit `any` constraint; each concrete instantiation also becomes a flat schema in OpenAPI (`Page<User>` emits a component named `PageOfUser`). `extend` only applies to `service` — there is no `extend type` / `extend enum`.

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
import "shared"

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

The recommended style is to keep field names lowercase (`createdAt string`) and reserve PascalCase for mixin references. Mixing the two on adjacent lines works, but a PascalCase field declared with a custom (non-builtin) type — e.g. `CreatedAt MyTimestamp` on its own line — is read as a mixin reference to `CreatedAt` followed by a field named `MyTimestamp`. When in doubt, write the field on its own line with a builtin or scalar-backed type.

#### Restrictions

A mixin must reference a `type` declaration. Referencing an `enum`, `error`, `scalar`, or `middleware` raises `mixin/non-type`. Unknown names raise `mixin/unresolved`.

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

The DSL form is `scalar <Name> <PrimitiveType> [@validators...]`. The primitive must be one of the built-in primitives (string, bytes, int variants, float variants, bool).

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

In Go output, scalars become type aliases:

```go
type Email = string
type OrderID = string
type Cents = int
```

The alias means `Email == string` at the type system level. No conversions needed at API boundaries.

### Restrictions

- Scalars wrap a primitive only. Cannot wrap struct types, enums, or other scalars.
- The validators must be compatible with the primitive (`@length` on `int` is a semantic error).
- Scalar names must be unique within a package.

## Enums

See [Enums](/guide/enums).

## Errors

See [Errors](/guide/errors).
