# Keywords

The DSL has 16 keywords. They are reserved - identifiers cannot use these names.

## Declaration keywords

| Keyword      | Position    | Purpose                                                       |
| ------------ | ----------- | ------------------------------------------------------------- |
| `package`    | first line  | The package every declaration in this file belongs to         |
| `import`     | header area | Reach declarations in another design subfolder                |
| `type`       | top level   | Declare a request / response struct                           |
| `enum`       | top level   | Declare a closed value set                                    |
| `error`      | top level   | Declare a typed error with HTTP status mapping                |
| `scalar`     | top level   | Declare a named primitive with bundled validators             |
| `service`    | top level   | Declare an HTTP service                                       |
| `extend`     | top level   | Add methods to an existing service (`extend service Name`)    |
| `middleware` | top level   | Declare a named middleware slot                               |

## Method body keywords

| Keyword     | Where                | Purpose                          |
| ----------- | -------------------- | -------------------------------- |
| `request`   | inside method body   | Names the request type           |
| `response`  | inside method body   | Names the response type          |

## Type keywords

| Keyword   | Where           | Purpose                              |
| --------- | --------------- | ------------------------------------ |
| `map`     | type expression | Map type: `map<string, int>`         |

## Literal keywords

| Keyword   | Purpose                                  |
| --------- | ---------------------------------------- |
| `true`    | Boolean true literal                     |
| `false`   | Boolean false literal                    |
| `null`    | Null literal (used in some decorator args)|

## HTTP verbs

These behave like keywords inside a service body. They are also legal as identifiers in other positions (e.g. as an enum value name), so the parser disambiguates by context.

| Verb      | Maps to       |
| --------- | ------------- |
| `get`     | `GET`         |
| `post`    | `POST`        |
| `put`     | `PUT`         |
| `patch`   | `PATCH`       |
| `delete`  | `DELETE`      |
| `head`    | `HEAD`        |
| `options` | `OPTIONS`     |

## `package`

The first non-comment statement in every `.craftgo` file:

```craftgo
package design
```

All files in the same directory must share the same `package` name. The directory itself is the unit of cross-file resolution: declarations in `design/users/service.craftgo` and `design/users/errors.craftgo` see each other directly because they share `package design` and live in the same folder.

The package name does not need to match the folder name (though doing so reads cleaner).

## `import`

Reach declarations from a different design subfolder:

```craftgo
package design

import "shared"

type User {
    contact shared.Contact
}
```

The string is the path of a sibling subfolder under `design/`. craftgo wires the matching Go imports automatically when codegen sees a `<pkg>.<Type>` reference. Import cycles, self-imports, and out-of-tree paths (`..`, `/abs/path`) are rejected by the semantic phase (`import/escape`, `import/self`, etc.). Middleware names are global across the project; type / enum / error / scalar names live in their declaring package and must be qualified at the call site (`shared.Audit`, `users.User`).

## `type`

Declare a request or response struct.

```craftgo
type CreateUserReq {
    name  string
    email string @format(email)
}

type Page<T> {                  // generic — bare ident, no constraint
    items T[]
    total int
}
```

See [Types and Scalars](/guide/types-and-scalars).

## `enum`

Declare a closed value set with three forms (bare / integer / string):

```craftgo
enum Status { Active  Inactive  Pending }
enum Priority { Low = 1  Medium = 2  High = 3 }
enum Color { Red = "red"  Green = "green"  Blue = "blue" }
```

All values inside one enum must share the same form. Mixing them (e.g. `Active = 1  Inactive = "off"`) raises `enum/mixed-types`. Enums are not generic — only `type` declarations support `<T, U, ...>` parameters.

See [Enums](/guide/enums).

## `scalar`

Declare a named primitive with bundled validators:

```craftgo
scalar Email     string  @format(email) @maxLength(254)
scalar OrderID   string  @length(8, 64) @pattern("^ord_[A-Z0-9]+$")
scalar Cents     int     @gte(0) @multipleOf(2)
scalar Latitude  float64 @gte(-90) @lte(90)
```

The first form after the name is the underlying primitive (`string`, `bytes`, `int`, `int8/16/32/64`, `uint`, `uint8/16/32/64`, `float32`, `float64`, `bool`). Validators following the primitive inherit to every field of that scalar type.

See [Types and Scalars](/guide/types-and-scalars).

## `error`

Declare a typed error. Two forms:

```craftgo
error NotFound UserNotFound                    // empty body, 404

error Conflict EmailTaken {                    // body fields, 409
    email      string
    existingId string?
}
```

The first identifier after `error` is the HTTP category (one of 21 reserved names like `BadRequest`, `NotFound`, `Conflict`, `Internal`). The second is the Go type name. Optional body block carries fields that ride on the wire.

See [Errors](/guide/errors).

## `service`

Declare an HTTP service:

```craftgo
@prefix("/v1")
@tags(users)
@middlewares(AuthRequired)
service UserService {
    @doc("Get user by id.")
    get GetUser /users/{id} {
        request  GetUserReq
        response User
    }
}
```

The body holds zero or more method declarations. Method form: `<verb> <Name> <path> { request <Type>  response <Type> }`. The `request` and `response` lines are optional (a method may have neither, only request, or only response).

See [DSL Basics](/guide/dsl-basics) for path parameters, decorators, and the full method shape.

## `extend service`

Add methods to an existing service from a different file:

```craftgo
package design

extend service UserService {
    @middlewares(AdminOnly)
    delete PurgeUser /users/{id}/purge {
        request  GetUserReq
        response shared.OkResp
    }
}
```

`extend` blocks may carry method-level-applicable decorators (`@middlewares`, `@security`, `@tags`, `@deprecated`, `@doc`) - those propagate to every method inside. Service-only decorators like `@prefix` / `@group` belong on the primary `service` block. The extended service must already exist in the same package.

Used to split a large service across files, separate authenticated endpoints from public ones (the 50/50 pattern: primary holds public methods, an extend block holds the authenticated chain), or organise admin endpoints under a different middleware chain than the default. See [DSL Basics](/guide/dsl-basics#extending-a-service-across-files) for the full pattern.

## `middleware`

Declare a named middleware slot:

```craftgo
middleware AuthRequired
middleware RateLimit
middleware CORS
```

Declared at file (package) level. Codegen produces a typed slot on `ServiceContext` and an empty stub at `internal/middleware/<name>-middleware.go` you fill in. Methods opt in via `@middlewares(Name, ...)`. Middleware names are global across the project — `@middlewares(AuthRequired)` resolves the same regardless of package — because middleware represents runtime behavior, not data; type / enum / error / scalar names stay package-scoped and must be qualified across packages.

See [Middleware](/guide/middleware).

## `request` and `response`

Used inside a method body to name the request and response types:

```craftgo
post CreateUser /users {
    request  CreateUserReq
    response User
}
```

Both are optional. Methods without `request` accept no body. Methods without `response` return an empty body with the configured status.

## `map`

Used inside a type expression for map types:

```craftgo
type Settings {
    flags  map<string, bool>
    quotas map<string, int>
}
```

The first generic argument is the key type and the second is the value type (any DSL type). The key must be a string- or integer-kind primitive (`string`, `int*`, `uint*`) or a scalar/enum over one — `encoding/json` can only marshal those as object keys. A `bool`, `float*`, or struct key is rejected at design time (it would compile but fail `json.Marshal` at runtime).

## Reserved names you cannot use as identifiers

Avoid using any keyword above as a type, field, enum value, or service name. The lexer emits a syntax error if you try. For valid Go-side identifiers that happen to match keywords (e.g. naming a field `type`), pick a different name.

## File grammar in one shape

```
package <ident>

[import "<path>"]*

[<decl>]*

where <decl> is one of:
  [@decorator]* type Name { fields... }
  [@decorator]* type Name<TypeParam any, ...> { fields... }
  [@decorator]* enum Name { values... }
  [@decorator]* error Category Name [{ fields... }]
  [@decorator]* scalar Name <Primitive> [@validators...]
  [@decorator]* service Name { methods... }
  [@decorator]* extend service Name { methods... }
  [@decorator]* middleware Name
```
