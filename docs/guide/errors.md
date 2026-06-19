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

The framework reads the typed error's `HTTPStatus()` and writes the right status code. Errors with body fields emit those fields as the response body; errors without body fields emit a default `{code, message}` envelope.

The rest of this page covers each form, the available categories, and how errors surface in OpenAPI.

## Two forms

### Category-only

```craftgo
error NotFound UserNotFound
```

The DSL is `error <Category> <Name>`. Generated Go:

```go
const ErrCodeUserNotFound = "USER_NOT_FOUND"

type UserNotFoundErr struct {
    code    string
    message string
}

func NewUserNotFoundErr() *UserNotFoundErr {
    return &UserNotFoundErr{
        code:    ErrCodeUserNotFound,
        message: "Not Found",
    }
}

func (e *UserNotFoundErr) Error() string    { return e.message }
func (e *UserNotFoundErr) HTTPStatus() int  { return 404 }
```

The wire response when this error returns:

```
HTTP/1.1 404 Not Found
Content-Type: application/json

{"code":"USER_NOT_FOUND","message":"Not Found"}
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
    code    string
    message string
    EmailTakenBody
}

func NewEmailTakenErr(body EmailTakenBody) *EmailTakenErr {
    return &EmailTakenErr{
        code:                 ErrCodeEmailTaken,
        message:              "Conflict",
        EmailTakenBody:       body,
    }
}
```

The wire response carries only the user-declared fields:

```
HTTP/1.1 409 Conflict
Content-Type: application/json

{"email":"alice@example.com","existingId":"u-42"}
```

The framework's `code` and `message` fields are unexported, so `json.Marshal` omits them. If you want them on the wire, declare them in the body:

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
| `BadRequest`          | 400    | Bad Request                |
| `Unauthorized`        | 401    | Unauthorized               |
| `PaymentRequired`     | 402    | Payment Required           |
| `Forbidden`           | 403    | Forbidden                  |
| `NotFound`            | 404    | Not Found                  |
| `MethodNotAllowed`    | 405    | Method Not Allowed         |
| `NotAcceptable`       | 406    | Not Acceptable             |
| `Conflict`            | 409    | Conflict                   |
| `Gone`                | 410    | Gone                       |
| `LengthRequired`      | 411    | Length Required            |
| `PreconditionFailed`  | 412    | Precondition Failed        |
| `PayloadTooLarge`     | 413    | Payload Too Large          |
| `UnsupportedMediaType`| 415    | Unsupported Media Type     |
| `UnprocessableEntity` | 422    | Unprocessable Entity       |
| `Locked`              | 423    | Locked                     |
| `TooManyRequests`     | 429    | Too Many Requests          |
| `Internal`            | 500    | Internal Server Error      |
| `NotImplemented`      | 501    | Not Implemented            |
| `BadGateway`          | 502    | Bad Gateway                |
| `ServiceUnavailable`  | 503    | Service Unavailable        |
| `GatewayTimeout`      | 504    | Gateway Timeout            |

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

The `@header` and `@cookie` decorators on error fields write to the response writer instead of the JSON body. Body fields ride normally.

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

For unexpected errors (raw `errors.New(...)` / `fmt.Errorf(...)` that carry no HTTP status), the framework logs the error with the request's trace context (`trace_id` / `span_id` / `request_id`) and responds 500 with a `{"message": ...}` JSON envelope.

## Custom error responses

The framework funnels every error response through one of three swappable hooks:

- `server.SetDefaultValidationFailed` - input that fails `Validate()` or parameter binding (default 400).
- `server.SetHandleUnknownError` - a service error that is **not** a craftgo typed error (no `HTTPStatus()`); the default logs it with trace context and responds 500. Use it to map a domain error to a status, redact, or return a uniform envelope.
- `(*server.Server).SetHandleNotFound` - requests that match no route (default 404).

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
