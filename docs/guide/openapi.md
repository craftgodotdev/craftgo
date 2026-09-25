# OpenAPI

craftgo emits OpenAPI 3.1 from the same DSL that drives the handlers. The spec is a first-class artifact, not an afterthought.

## At a glance

Every `craftgo gen` produces `docs/openapi.yaml` with:

- Every method as a `paths` entry
- Every non-generic type, enum, scalar and error as a `components.schemas` entry
- Every validator decorator mapped to its OpenAPI keyword (`minLength`, `pattern`, `enum`, ...)
- Doc comments flowing into descriptions
- Security schemes from your config

The spec renders directly in **Swagger UI** and **ReDoc**, and feeds `openapi-generator` for client libraries in any language - the day-to-day tools accept it as-is.

Nullability uses the canonical 3.1 idiom - `type: [T, "null"]` for inline types
and `anyOf: [{$ref}, {type: "null"}]` for named/generic refs - not the removed
3.0 `nullable: true` boolean, so strict 3.1 validators (Spectral, Redocly) and
client generators (hey-api, openapi-typescript) keep the `| null` union instead
of silently dropping it.

The rest of this page walks through what's emitted and how to render or publish it.

## What gets generated

Every `craftgo gen` writes `docs/openapi.yaml` covering:

- `paths` - one entry per route, an operation per `service` method with its
  path, query, header and cookie parameters, request body and responses
  written in place
- `components.schemas` - every non-generic `type`, `enum`, `scalar` and
  `error` with full structure, each generic instance a schema or an operation
  refers to (`PageOfUser`), and the `<Method>ReqBody` / `<Method>RespBody` of
  each JSON body
- `components.securitySchemes` - when `openapi.securitySchemes` is in your config

## Validity

The output is consumed cleanly by:

- The official OpenAPI parser behind **Swagger UI** and **ReDoc** - renders without errors.
- [`openapi-generator`](https://openapi-generator.tech/) and similar client generators.
- [oasdiff](https://github.com/oasdiff/oasdiff) - breaking-change detection between versions.

The structure (paths, schemas, parameters, `anyOf` and `not` for cross-field constraints, `oneOf` for errors sharing a status, `propertyNames` for map keys) is valid 3.1: [Redocly CLI](https://redocly.com/redocly-cli/)'s structural rules and [openapi-spec-validator](https://github.com/python-openapi/openapi-spec-validator) accept it, given security schemes that carry the fields their type requires. Their style rules may still warn, about an operation without a `summary`, say.

## Renders

The spec renders in any OpenAPI viewer:

- **Swagger UI** - drop in `swagger-ui-dist` and point at `openapi.yaml`
- **ReDoc** - `<redoc spec-url='openapi.yaml'></redoc>`
- **Stoplight Elements** - `<elements-api apiDescriptionUrl="openapi.yaml" />`

## Client generation

Use the spec to generate clients in any language. Some popular options:

```bash
# TypeScript (typed fetch wrappers)
npx openapi-typescript-codegen -i docs/openapi.yaml -o client/

# Java
openapi-generator-cli generate -i docs/openapi.yaml -g java -o client-java/

# Python
openapi-generator-cli generate -i docs/openapi.yaml -g python -o client-python/

# Rust
openapi-generator-cli generate -i docs/openapi.yaml -g rust -o client-rust/
```

The generated client matches the contract because both come from the same DSL.

## Operation IDs

Each method gets an `operationId` derived from its DSL name:

```craftgo
service UserService {
    get GetUser /users/{id} { ... }
}
```

becomes

```yaml
paths:
  /v1/users/{id}:
    get:
      operationId: GetUser
```

The `operationId` is the bare method name (`GetUser`) when that name is unique
across the project. If two services declare a method with the same name, both
are prefixed with the service name (`OrdersServicePing` / `CatalogServicePing`)
so every `operationId` stays globally unique.

Override with `@operationId`:

```craftgo
@operationId("getUserById")
get GetUser /users/{id} { ... }
```

Two methods that resolve to the same `operationId` (two explicit
`@operationId("...")` sharing a value, or an override that collides with another
method's auto id) are reported at design time, so the spec never carries a
duplicate.

## Schema components

Each `type` becomes a reusable schema:

```craftgo
type User { id string  name string  email string }
```

```yaml
components:
  schemas:
    User:
      type: object
      properties:
        id:    { type: string }
        name:  { type: string }
        email: { type: string }
      required: [id, name, email]
```

Field-level validators map to OpenAPI keywords:

| Decorator / shape              | OpenAPI                  |
| ------------------------------ | ------------------------ |
| Non-optional field (no `?`)    | listed in `required: [...]` |
| `name string?`                 | omitted from `required: [...]`, `type: [T, "null"]` |
| `@nullable`                    | `type: [T, "null"]` (or `anyOf: [{$ref}, {type: "null"}]`) |
| `@default(v)`                  | `default: v`             |
| `@length(1, 80)`               | `minLength: 1, maxLength: 80` |
| `@minLength(1)`, `@maxLength(80)` | same as above         |
| `@pattern("...")`              | `pattern: ...`           |
| `@format(email)`               | `format: email`          |
| `@gte(0)`, `@lte(100)`         | `minimum: 0, maximum: 100` |
| `@gt(0)`, `@lt(100)`           | `exclusiveMinimum: 0` / `exclusiveMaximum: 100` |
| `@minItems(1)`, `@maxItems(10)` | `minItems: 1, maxItems: 10` |
| `@uniqueItems`                 | `uniqueItems: true`      |
| `@example("alice")`            | `example: alice`         |
| `@deprecated`                  | `deprecated: true`       |

Only a JSON body field admits `null`. A parameter, a response header and a
multipart part are sent or not: `?` makes one optional, and its schema is the
type alone.

On a float field the validator compares against the literal's float, and a
bound judges the literal and that float as the validator does: `@lte(0.1)` on
a `float32` field is `maximum: 0.10000000149011612`, which both pass, and
`@gt(0.1)` is `exclusiveMinimum: 0.10000000149011612`, which both fail.

## Documentation flows through

DSL doc comments become OpenAPI descriptions; `@summary("...")` sets an
operation's `summary`:

```craftgo
// Create a new user. The server fills the id and timestamps;
// the client supplies name and email.
@summary("Create a user")
post CreateUser /users {
    request  CreateUserReq
    response User
}
```

```yaml
paths:
  /v1/users:
    post:
      description: |-
        Create a new user. The server fills the id and timestamps;
        the client supplies name and email.
      summary: Create a user
```

Per-field docs flow into the schema's property description.

## Errors

Each `error` becomes a component named after its Go type (`UserNotFound` →
`UserNotFoundErr`). Each error a method's `@errors(...)` lists adds a
response at its category's status, described by the category:

```craftgo
type CreateUserReq {
    name  string
    email string
}

type User {
    id    string
    name  string
    email string
}

error NotFound UserNotFound
error Conflict EmailTaken { email string }

service UserService {
    @errors(UserNotFound, EmailTaken)
    post CreateUser /users {
        request  CreateUserReq
        response User
    }
}
```

The operation's `responses` and the two error components:

```yaml
responses:
  "201":
    content:
      application/json:
        schema:
          $ref: '#/components/schemas/CreateUserRespBody'
    description: Created
  "404":
    content:
      application/json:
        schema:
          $ref: '#/components/schemas/UserNotFoundErr'
    description: NotFound
  "409":
    content:
      application/json:
        schema:
          $ref: '#/components/schemas/EmailTakenErr'
    description: Conflict
```

```yaml
EmailTakenErr:
  description: Conflict error response (HTTP 409).
  properties:
    email:
      type: string
  required:
  - email
  type: object
UserNotFoundErr:
  description: NotFound error response (HTTP 404).
  properties:
    code:
      type: string
    message:
      type: string
  required:
  - code
  - message
  type: object
```

An error with no field, like `UserNotFound`, is documented as the `code` and
`message` the server sends for it. Errors of one category share its response,
their schemas in a `oneOf`.

## Security schemes

Define schemes in `craftgo.design.yaml`:

```yaml
openapi:
  securitySchemes:
    bearerAuth:
      type: http
      scheme: bearer
      bearerFormat: JWT
```

Reference them per-method in DSL:

```craftgo
@security(bearerAuth)
get GetUser /users/{id} { ... }
```

For a public method inside an otherwise-authenticated service, use `@ignoreSecurity` at the method level to drop the inherited chain:

```craftgo
@security(bearerAuth)
service Users {
    get GetUser /users/{id} { ... }       // requires bearerAuth

    @ignoreSecurity
    get Healthz /healthz { ... }          // no security clause emitted
}
```

The spec carries the security requirement; runtime enforcement is your middleware's job.

## Spec location

By default `docs/openapi.yaml`. Change with `output.openapi` in `craftgo.design.yaml`:

```yaml
output:
  openapi: ./api/openapi.yaml
```

`output.openapi: "-"` writes no document. What only the document gets wrong,
such as two component schemas sharing a name or an `oauth2` security scheme
without flows, stops a run that writes it, and no other: with `"-"`, or with
`craftgo gen --target go`, the Go code is generated. A new `main.go` embeds
and serves the document only when it is on disk as the Go code is generated,
so a project first generated with `--target go` gets a `main.go` without it;
add the [embed](/guide/runtime#api-reference-docs) once the document exists.
