# OpenAPI

craftgo emits OpenAPI 3.1 from the same DSL that drives the handlers. The spec is a first-class artifact, not an afterthought.

## At a glance

Every `craftgo gen` produces `docs/openapi.yaml` with:

- Every method as a `paths` entry
- Every type, enum, and error as a `components.schemas` entry
- Every validator decorator mapped to its OpenAPI keyword (`minLength`, `pattern`, `enum`, ...)
- Doc comments flowing into descriptions
- Security schemes from your config

The spec renders directly in **Swagger UI** and **ReDoc**, and feeds `openapi-generator` for client libraries in any language — the day-to-day tools accept it as-is.

::: warning Strict 3.1 validators
The document declares `openapi: 3.1.0` but currently uses the OpenAPI **3.0** `nullable: true` idiom for optional fields (3.1 replaces it with `type: [..., "null"]`). Lenient consumers (Swagger UI, ReDoc, openapi-generator) accept this, but a strict 3.1 validator — Spectral or Redocly in default config — will flag every `nullable` occurrence. Migrating the nullable emit to the 3.1 idiom is tracked for an upcoming release. If you gate CI on strict linting today, allowlist the `nullable` rule.
:::

The rest of this page walks through what's emitted and how to render or consume it.

## What gets generated

Every `craftgo gen` writes `docs/openapi.yaml` covering:

- `paths` - one entry per `service` method
- `components.schemas` - every `type`, `enum`, and `error` with full structure
- `components.parameters` - path, query, header, cookie params per operation
- `components.requestBodies` - body, multipart, and other content types
- `components.responses` - success and declared error responses
- `components.securitySchemes` - when `openapi.securitySchemes` is in your config

## Validity

The output is consumed cleanly by:

- The official OpenAPI parser behind **Swagger UI** and **ReDoc** — renders without errors.
- [`openapi-generator`](https://openapi-generator.tech/) and similar client generators.
- [oasdiff](https://github.com/oasdiff/oasdiff) — breaking-change detection between versions.

Strict structural linters ([Spectral](https://stoplight.io/open-source/spectral), [Redocly CLI](https://redocly.com/redocly-cli/)) currently report `nullable`-related findings under their default 3.1 ruleset — see the warning above. Aside from the `nullable` idiom, the structure (paths, schemas, parameters, `oneOf`/`anyOf` for cross-field constraints, `propertyNames` for map keys) is valid 3.1.

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
| `name string?`                 | omitted from `required: [...]` |
| `@nullable`                    | `nullable: true`         |
| `@default(v)`                  | `default: v`             |
| `@length(1, 80)`               | `minLength: 1, maxLength: 80` |
| `@minLength(1)`, `@maxLength(80)` | same as above         |
| `@pattern("...")`              | `pattern: ...`           |
| `@format(email)`               | `format: email`          |
| `@gte(0)`, `@lte(100)`         | `minimum: 0, maximum: 100` |
| `@gt(0)`, `@lt(100)`           | `minimum: 0, exclusiveMinimum: true` / `maximum: 100, exclusiveMaximum: true` |
| `@minItems(1)`, `@maxItems(10)` | `minItems: 1, maxItems: 10` |
| `@uniqueItems`                 | `uniqueItems: true`      |
| `@example("alice")`            | `example: alice`         |
| `@deprecated`                  | `deprecated: true`       |

## Documentation flows through

DSL doc comments become OpenAPI descriptions:

```craftgo
// Create a new user. The server fills the id and timestamps;
// the client supplies name and email.
post CreateUser /users {
    request  CreateUserReq
    response User
}
```

```yaml
paths:
  /v1/users:
    post:
      summary: Create a new user. The server fills...
      description: ...
```

Per-field docs flow into the schema's property description.

## Errors

Declared errors with `@errors(...)` populate per-operation responses:

```craftgo
error NotFound UserNotFound
error Conflict EmailTaken { email string }

service UserService {
    @errors(UserNotFound, EmailTaken)
    post CreateUser /users { ... }
}
```

```yaml
responses:
  '200': { ... }
  '404':
    description: Not Found
    content:
      application/json:
        schema:
          $ref: '#/components/schemas/UserNotFound'
  '409':
    description: Conflict
    content:
      application/json:
        schema:
          $ref: '#/components/schemas/EmailTaken'
```

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

By default `docs/openapi.yaml`. Change with `output.docs` in `craftgo.design.yaml`:

```yaml
output:
  docs: api/openapi
```
