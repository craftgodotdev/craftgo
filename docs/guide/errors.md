# Errors

An `error` declaration produces a Go error type with an HTTP status code attached. Returning it from your service layer lands the correct status, message, and body shape on the wire automatically.

## At a glance

```craftgo
error NotFound UserNotFound                       // empty body, 404

error Conflict EmailTaken {                       // body fields, 409
    email      string
    existingId string?
}
```

Use them like any Go error:

```go
return nil, types.NewUserNotFoundErr()
return nil, types.NewEmailTakenErr(types.EmailTakenBody{Email: req.Email})
```

The framework reads the typed error's `HTTPStatus()` and writes the right status code. Errors with body fields emit those fields as the response body; errors with no field on the JSON body - none at all, or only `@header`, `@cookie` and `@sensitive` ones - emit a default `{code, message}` envelope.

The rest of this page covers each form, the available categories, and how errors surface in OpenAPI.

## Two forms

### Category-only

```craftgo
error NotFound UserNotFound
```

The DSL is `error <Category> <Name>`. Generated Go:

```go
const ErrCodeUserNotFound = "USER_NOT_FOUND"

type UserNotFoundErr struct{}

func NewUserNotFoundErr() *UserNotFoundErr {
    return &UserNotFoundErr{}
}

func (e *UserNotFoundErr) Error() string   { return "Not found" }
func (e *UserNotFoundErr) ErrCode() string { return ErrCodeUserNotFound }
func (e *UserNotFoundErr) HTTPStatus() int { return 404 }

func (e *UserNotFoundErr) MarshalJSON() ([]byte, error) {
    return json.Marshal(map[string]string{"code": ErrCodeUserNotFound, "message": e.Error()})
}
```

The wire response when this error returns:

```
HTTP/1.1 404 Not Found
Content-Type: application/json

{"code":"USER_NOT_FOUND","message":"Not found"}
```

### Body form

When the error needs to carry data:

```craftgo
error Conflict EmailTaken {
    email      string
    existingId string?
}
```

```go
type EmailTakenBody struct {
    Email      string  `json:"email"`
    ExistingID *string `json:"existingId,omitempty"`
}

type EmailTakenErr struct {
    EmailTakenBody
}

func NewEmailTakenErr(body EmailTakenBody) *EmailTakenErr {
    return &EmailTakenErr{EmailTakenBody: body}
}

func (e *EmailTakenErr) MarshalJSON() ([]byte, error) { return json.Marshal(e.EmailTakenBody) }
```

The wire response carries only the user-declared fields:

```
HTTP/1.1 409 Conflict
Content-Type: application/json

{"email":"alice@example.com","existingId":"u-42"}
```

The error's code and message are not fields: `ErrCode()` and `Error()` return them, and `MarshalJSON` writes the body alone - `{}` when every body field is optional and unset. If you want them on the wire, declare them in the body:

```craftgo
error Conflict EmailTaken {
    code    string @default("EMAIL_TAKEN")
    message string @default("Email already registered")
    email   string
}
```

## Categories

The `<Category>` slot picks the HTTP status. Built-in categories:

| Category              | Status | Default message            |
| --------------------- | ------ | -------------------------- |
| `BadRequest`          | 400    | Bad request                |
| `Unauthorized`        | 401    | Unauthorized               |
| `PaymentRequired`     | 402    | Payment required           |
| `Forbidden`           | 403    | Forbidden                  |
| `NotFound`            | 404    | Not found                  |
| `MethodNotAllowed`    | 405    | Method not allowed         |
| `NotAcceptable`       | 406    | Not acceptable             |
| `Conflict`            | 409    | Conflict                   |
| `Gone`                | 410    | Resource gone              |
| `LengthRequired`      | 411    | Length required            |
| `PreconditionFailed`  | 412    | Precondition failed        |
| `PayloadTooLarge`     | 413    | Payload too large          |
| `UnsupportedMediaType`| 415    | Unsupported media type     |
| `UnprocessableEntity` | 422    | Unprocessable entity       |
| `Locked`              | 423    | Resource locked            |
| `TooManyRequests`     | 429    | Too many requests          |
| `Internal`            | 500    | Internal server error      |
| `NotImplemented`      | 501    | Not implemented            |
| `BadGateway`          | 502    | Bad gateway                |
| `ServiceUnavailable`  | 503    | Service unavailable        |
| `GatewayTimeout`      | 504    | Gateway timeout            |

Custom categories are not supported. Pick the closest standard one.

## Using errors in service code

```go
func (s *Service) GetUser(ctx context.Context, req *types.GetUserReq) (*types.User, error) {
    user, ok := s.svcCtx.Users[req.ID]
    if !ok {
        return nil, types.NewUserNotFoundErr()
    }
    return &user, nil
}

func (s *Service) CreateUser(ctx context.Context, req *types.CreateUserReq) (*types.User, error) {
    if existing, ok := s.svcCtx.UsersByEmail[req.Email]; ok {
        return nil, types.NewEmailTakenErr(types.EmailTakenBody{
            Email:      req.Email,
            ExistingID: &existing.ID,
        })
    }
    ...
}
```

The handler reads `HTTPStatus()` and writes the matching status code.

## Header and cookie fields

Error fields can carry HTTP headers and cookies on the response:

```craftgo
error TooManyRequests RateLimited {
    retryAfter int    @header("Retry-After")
    code       string
}
```

The `@header` and `@cookie` decorators on error fields write to the response writer instead of the JSON body. Body fields ride normally. An error whose every field, a mixin's included, rides a header or a cookie, or is `@sensitive`, has nothing for the JSON body, so its body is the `{"code","message"}` envelope.

## Declaring per-method

`@errors(...)` on a method advertises which errors that method can return. Used for OpenAPI and as a runtime hint:

```craftgo
service UserService {
    @errors(UserNotFound)
    get GetUser /users/{id} {
        request  GetUserReq
        response User
    }

    @errors(EmailTaken, ValidationFailed)
    post CreateUser /users {
        request  CreateUserReq
        response User
    }
}
```

The OpenAPI spec shows each declared error as a per-status response with the schema attached.

## Default error responses

If your service returns an error that isn't declared in `@errors(...)`, it still surfaces on the wire correctly (the typed error implements `HTTPStatus()`), but the OpenAPI spec won't list it. Best practice: declare every error you intentionally return.

For unexpected errors (raw `errors.New(...)` / `fmt.Errorf(...)` that carry no HTTP status), the framework logs the error with the request's trace context (`trace_id` / `span_id`) and responds 500 with a `{"message": ...}` JSON envelope. A typed error stays typed when wrapped: `WriteError` finds it with `errors.As`, so `fmt.Errorf("charge card: %w", err)` still renders the declared status and body, and is not logged.

## Custom error responses

The framework funnels every error response through one of three swappable hooks:

- `server.SetDefaultValidationFailed` - input that fails `Validate()` or parameter binding (default 400).
- `server.SetHandleUnknownError` - a service error that is **not** a craftgo typed error (no `HTTPStatus()`); the default logs it with trace context and responds 500. Use it to map a domain error to a status, redact, or return a uniform envelope.
- `(*server.Server).SetHandleNotFound` - requests that match no route (default 404); a method mismatch keeps its 405 with `Allow`.

A recognised typed error (one that implements `server.StatusError` - every `@errors(...)` declaration does) is rendered directly from its interface and is **not** logged: a declared 4xx/5xx is an expected outcome. For full control over a business error's wire shape, give the error a body struct.

## Cross-package errors

Errors live in the package they are declared in. Import them like any other type:

```craftgo
package design

service UserService {
    @errors(shared.AuthRequiredErr, UserNotFound)
    get GetUser /users/{id} { ... }
}
```

The Go side imports the shared package's error type automatically.

## Errors are types

Generated error types are regular Go types. You can:

- Return them from any function in the call chain
- Wrap them with `fmt.Errorf("upstream: %w", err)` and unwrap with `errors.As`
- Compare with `errors.Is` if you implement the comparison

The framework's `writeError` uses `interface{ HTTPStatus() int }` to extract the status, so wrapped errors still get the right status as long as `errors.As` can pull out the typed error.
