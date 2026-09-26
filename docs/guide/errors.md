# Errors

An `error` declaration produces a Go error type with an HTTP status code attached. Returning it from your service layer lands the correct status, message, and body shape on the wire automatically.

## At a glance

```craftgo
error NotFound UserNotFound                       // no fields, 404

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
// ErrCodeUserNotFound is the canonical machine-readable code for UserNotFoundErr.
const ErrCodeUserNotFound = "USER_NOT_FOUND"

// UserNotFoundErr is the NotFound error UserNotFound.
type UserNotFoundErr struct{}

// NewUserNotFoundErr constructs UserNotFoundErr.
func NewUserNotFoundErr() *UserNotFoundErr {
	return &UserNotFoundErr{}
}

// Error returns the NotFound category's default message.
func (e *UserNotFoundErr) Error() string { return "Not found" }

// ErrCode returns ErrCodeUserNotFound.
func (e *UserNotFoundErr) ErrCode() string { return ErrCodeUserNotFound }

// HTTPStatus returns the NotFound status.
func (e *UserNotFoundErr) HTTPStatus() int { return 404 }

// MarshalJSON encodes the {"code", "message"} envelope.
func (e *UserNotFoundErr) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"code": ErrCodeUserNotFound, "message": e.Error()})
}
```

The wire response when this error returns:

```
HTTP/1.1 404 Not Found
Content-Type: application/json; charset=utf-8
X-Content-Type-Options: nosniff

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
// ErrCodeEmailTaken is the canonical machine-readable code for EmailTakenErr.
const ErrCodeEmailTaken = "EMAIL_TAKEN"

// EmailTakenBody is the body of EmailTakenErr.
type EmailTakenBody struct {
	Email      string  `json:"email"`
	ExistingID *string `json:"existingId,omitempty"`
}

// EmailTakenErr is the Conflict error EmailTaken.
type EmailTakenErr struct {
	EmailTakenBody
}

// NewEmailTakenErr constructs EmailTakenErr.
func NewEmailTakenErr(body EmailTakenBody) *EmailTakenErr {
	return &EmailTakenErr{EmailTakenBody: body}
}

// Error returns the Conflict category's default message.
func (e *EmailTakenErr) Error() string { return "Conflict" }

// ErrCode returns ErrCodeEmailTaken.
func (e *EmailTakenErr) ErrCode() string { return ErrCodeEmailTaken }

// HTTPStatus returns the Conflict status.
func (e *EmailTakenErr) HTTPStatus() int { return 409 }

// MarshalJSON encodes the body alone.
func (e *EmailTakenErr) MarshalJSON() ([]byte, error) { return json.Marshal(e.EmailTakenBody) }
```

The wire response carries only the user-declared fields:

```
HTTP/1.1 409 Conflict
Content-Type: application/json; charset=utf-8
X-Content-Type-Options: nosniff

{"email":"alice@example.com","existingId":"u-42"}
```

The error's code and message are not fields: `ErrCode()` and `Error()` return them, and `MarshalJSON` writes the body alone - `{}` when every body field is optional and unset. If you want them on the wire, declare them in the body and set them where you build the error:

```craftgo
error Conflict EmailTaken {
    code    string
    message string
    email   string
}
```

```go
return nil, types.NewEmailTakenErr(types.EmailTakenBody{
	Code:    types.ErrCodeEmailTaken,
	Message: "Email already registered",
	Email:   req.Email,
})
```

The constructor fills in nothing: a `@default` on an error field reaches only the OpenAPI schema.

A body field cannot take the Go name of a member of the error type, which would hide it: the methods `error`, `errCode`, `httpStatus`, `marshalJSON` and `writeResponseHeaders`, or `<name>Body`, the struct the type embeds (`emailTakenBody` here). Like every body, an error's cannot hold `validate` either; each is `field/invalid-go-name`.

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
func (l *GetUserService) GetUser(req *types.GetUserReq) (*types.User, error) {
	user, ok := l.svcCtx.Users[req.ID]
	if !ok {
		return nil, types.NewUserNotFoundErr()
	}
	return &user, nil
}

func (l *CreateUserService) CreateUser(req *types.CreateUserReq) (*types.User, error) {
	if existing, ok := l.svcCtx.UsersByEmail[req.Email]; ok {
		return nil, types.NewEmailTakenErr(types.EmailTakenBody{
			Email:      req.Email,
			ExistingID: &existing.ID,
		})
	}
	...
}
```

`Users` and `UsersByEmail` stand for whatever store your `ServiceContext` holds. The handler reads `HTTPStatus()` and writes the matching status code.

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

`@errors(...)` on a method lists the errors that method can return, for the OpenAPI document; it changes nothing at runtime:

```craftgo
service UserService {
    @errors(UserNotFound)
    get GetUser /users/{id} {
        request  GetUserReq
        response User
    }

    @errors(EmailTaken)
    post CreateUser /users {
        request  CreateUserReq
        response User
    }
}
```

The OpenAPI spec shows each declared error as a per-status response with the schema attached.

## Default error responses

If your service returns an error that isn't declared in `@errors(...)`, it still surfaces on the wire correctly (the typed error implements `HTTPStatus()`), but the OpenAPI document won't list it. Best practice: declare every error you intentionally return.

For an unexpected error (a raw `errors.New(...)` or `fmt.Errorf(...)` that carries no HTTP status), the framework logs it (`unhandled service error`) with the request's trace context (`trace_id` / `span_id`) and responds 500 `{"message":"internal server error"}`; the error's text stays off the wire. Context errors are the exception:

- An error wrapping `context.DeadlineExceeded`, or any context error once the request's own deadline (`@timeout`, `server.handlerTimeout`) has passed, answers 504 `{"message":"gateway timeout"}` unlogged; a dependency's deadline on a live request is logged at Warn, `dependency deadline exceeded`.
- A context error once the request context is canceled - by a client that went away, or by a middleware's own cancel - writes and logs nothing, so a client still connected sees an empty 200.

A typed error stays typed when wrapped: `WriteError` finds it with `errors.As`, so `fmt.Errorf("charge card: %w", err)` still renders the declared status and body, and is not logged.

The errors the framework writes itself are JSON `{"message": "..."}`, with `Content-Type: application/json; charset=utf-8` and `X-Content-Type-Options: nosniff`, at these statuses. None of them is in the OpenAPI document, which lists each operation's success response and its `@errors` only:

| Status | When | Body |
|---|---|---|
| 400 | the body does not decode, a parameter does not bind, or the request fails `Validate()` | `{"message":"<error text>"}`, e.g. `{"message":"name: length out of range [1, 80]"}` |
| 404 | no route matches the path | `{"message":"not found"}` |
| 405 | a route matches the path under another method; `Allow` lists its methods | `{"message":"method not allowed"}` |
| 413 | the body is over its cap (`@maxBodySize`, `server.maxBodySize`, `BodyLimit`) | `{"message":"request entity too large"}` |
| 500 | a panic, or an error that carries no status | `{"message":"internal server error"}` |
| 504 | a deadline, as above | `{"message":"gateway timeout"}` |

## Custom error responses

Three swappable hooks shape the error responses the framework writes for you:

- `server.SetDefaultValidationFailed` - input that fails JSON decoding, parameter binding or `Validate()` (default 400 `{"message":"<error text>"}`); a body read past its cap answers 413 `{"message":"request entity too large"}` without calling it.
- `server.SetHandleUnknownError` - a service error that is neither a craftgo typed error (no `HTTPStatus()`) nor a context error the framework answers itself (504 for a deadline, nothing for a canceled request); the default logs it with trace context and responds 500 `{"message":"internal server error"}`. A `context.Canceled` on a request that is still live does reach it. Use it to map a domain error to a status, redact, or return a uniform envelope.
- `(*server.Server).SetHandleNotFound` - the requests the mux would answer 404 (default 404 `{"message":"not found"}`, which `SetHandleNotFound(nil)` restores); a method mismatch keeps its 405 `{"message":"method not allowed"}` with `Allow`.

A recognised typed error (one that implements `server.StatusError` - every `error` declaration does) is rendered directly from its interface and is **not** logged: a declared 4xx/5xx is an expected outcome. For full control over a business error's wire shape, give the error a body struct.

## Cross-package errors

Errors live in the package they are declared in. Name another package's error by its package and its design name:

```craftgo
package users

service UserService {
    @errors(shared.AuthRequired, UserNotFound)
    get GetUser /users/{id} {
        request  GetUserReq
        response User
    }
}
```

with `error Unauthorized AuthRequired` in package `shared`. Its Go type is `AuthRequiredErr` in the shared types package, which your logic imports to return `shared.NewAuthRequiredErr()`.

## Errors are types

Generated error types are regular Go types. You can:

- Return them from any function in the call chain
- Wrap them with `fmt.Errorf("upstream: %w", err)` and unwrap with `errors.As`
- Compare with `errors.Is` if you implement the comparison

`server.WriteError` finds a `server.StatusError` (an `error` with `HTTPStatus() int`) in the chain with `errors.As`, so wrapped errors still get the right status as long as `errors.As` can pull out the typed error.
