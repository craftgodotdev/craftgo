# OpenAPI

craftgo emits OpenAPI 3.1 from the same DSL that drives the handlers. The spec is a first-class artifact, not an afterthought.

## At a glance

Every `craftgo gen` produces `docs/openapi.yaml` with:

- Every method as a `paths` entry
- Every type, enum, and error as a `components.schemas` entry
- Every validator decorator mapped to its OpenAPI keyword (`minLength`, `pattern`, `enum`, ...)
- Doc comments flowing into descriptions
- Security schemes from your config

The spec is **valid OAS 3.1** and passes Spectral, Redocly, and the official parser. Use it directly with Swagger UI, ReDoc, or `openapi-generator` for client libraries in any language.

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

The output passes:

- [Spectral](https://stoplight.io/open-source/spectral) - `spectral lint docs/openapi.yaml`
- [Redocly CLI](https://redocly.com/redocly-cli/) - `redocly lint docs/openapi.yaml`
- [oasdiff](https://github.com/oasdiff/oasdiff) - for breaking-change detection between versions
- The official OpenAPI parser used by Swagger UI

We test this in CI. If you find a spec that fails a standard linter, that is a bug.

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
      operationId: UserService_GetUser
```

Override with `@operationId`:

```craftgo
@operationId("getUserById")
get GetUser /users/{id} { ... }
```

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

| Decorator                      | OpenAPI                  |
| ------------------------------ | ------------------------ |
| `@required`                    | `required: [...]`        |
| `@length(1, 80)`               | `minLength: 1, maxLength: 80` |
| `@minLength(1)`, `@maxLength(80)` | same as above         |
| `@pattern("...")`              | `pattern: ...`           |
| `@format(email)`               | `format: email`          |
| `@min(0)`, `@max(100)`         | `minimum: 0, maximum: 100` |
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

The spec carries the security requirement; runtime enforcement is your middleware's job.

## Spec location

By default `docs/openapi.yaml`. Change with `output.docs` in `craftgo.design.yaml`:

```yaml
output:
  docs: api/openapi
```
