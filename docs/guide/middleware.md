# Middleware

Middleware in craftgo is a regular `func(http.Handler) http.Handler`. There are two ways to wire it up: directly in `main.go`, or declared in the DSL and attached to services / methods.

## At a glance

```
[ 1 ] Runtime middleware - srv.Use(...) in main.go - applies to every request
[ 2 ] Declared middleware - DSL keyword + @middlewares(...) - per-service or per-method
```

Use **runtime middleware** for cross-cutting concerns that apply globally regardless of the API contract: request ID, access log, OTel, recovery.

Use **declared middleware** when the DSL needs to know about it: which services / methods opt in, which order, how it surfaces in OpenAPI's security section.

The rest of this page covers each in detail.

## Runtime middleware (no DSL involved)

For cross-cutting concerns that apply globally regardless of the API contract, use `srv.Use`:

```go
srv := server.New(svcCtx)
srv.Use(server.RequestID())
srv.Use(server.AccessLog(logger))
srv.Use(server.BodyLimit(1 << 20))
srv.Use(otelhttp.NewMiddleware("api"))
```

Order matters. The first `Use` is the outermost frame.

## Declared middleware (DSL-driven)

Declare a middleware once at file (package) level:

```craftgo
// design/shared/middlewares.craftgo
package shared

middleware AuthRequired
middleware RateLimit
middleware CORS
middleware RequestID
```

Declarations are global to their package. They do not live inside a service body.

Codegen produces:

- A typed slot on `ServiceContext.Middlewares` for each name (e.g. `svc.AuthRequired`, `svc.RateLimit`)
- An empty stub at `internal/middleware/<name>-middleware.go` you fill in
- A registration step in main.go that wires your stubs into the slots

The DSL only carries the contract (the name and where it applies). The implementation lives in the stub.

### Implementing a stub

```go
// internal/middleware/auth-required-middleware.go
func NewAuthRequiredMiddleware() server.Middleware {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            token := r.Header.Get("Authorization")
            if !strings.HasPrefix(token, "Bearer ") {
                http.Error(w, "unauthorized", http.StatusUnauthorized)
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

The signature matches `server.Middleware`, the same shape as any `func(http.Handler) http.Handler`.

### Wiring in main.go

The generated `main.go` already wires every declared middleware. You can edit `main.go` to pass parameters:

```go
svc.AuthRequired = middleware.NewAuthRequiredMiddleware(jwtVerifier)
svc.RateLimit    = middleware.NewRateLimitMiddleware(redisClient, 100)
```

`main.go` is gen-once - your edits stick across regenerations.

## Attaching middleware to services and methods

Use the `@middlewares` decorator. The order matches the order they will run.

### Per-service

Every method in the service runs the listed middlewares:

```craftgo
@prefix("/users")
@middlewares(RequestID, RateLimit, CORS, AuthRequired)
service UserService {
    get GetUser /{id} { ... }
    post CreateUser / { ... }
    delete DeleteUser /{id} { ... }
}
```

All three methods inherit the same chain.

### Per-method

A method-level `@middlewares` appends additional frames after the service-level chain:

```craftgo
@prefix("/users")
@middlewares(AuthRequired)
service UserService {
    get GetUser /{id} { ... }

    @middlewares(AdminOnly)
    delete DeleteUser /{id} { ... }
}
```

`DeleteUser` runs `AuthRequired` then `AdminOnly`. `GetUser` only runs `AuthRequired`.

## `extend service` for cross-file additions

A service can be defined in one file and extended in others. Extension blocks add methods to the original service without redeclaring its decorators.

```craftgo
// design/users/service.craftgo
package design

@prefix("/users")
@middlewares(AuthRequired)
service UserService {
    get GetUser /{id} { request GetUserReq  response User }
}
```

```craftgo
// design/users/admin.craftgo
package design

extend service UserService {
    @middlewares(AdminOnly)
    delete DeleteUser /{id} { request GetUserReq  response shared.OkResp }
}
```

The extended methods live under the same `/users` prefix and inherit `AuthRequired` from the primary block. Their own `@middlewares(AdminOnly)` appends to that chain.

When to use `extend`:

- Split a large service across files for readability
- Keep admin / internal endpoints next to the public ones but easy to find
- Add methods from a different package that imports the service's package

An `extend` block can also carry its own method-level-applicable decorators (`@middlewares`, `@security`, `@tags`, `@deprecated`) - those propagate to every method inside. Useful for the 50/50 split: primary holds public endpoints, an extend block holds the authenticated chain.

```craftgo
service Users {
    get  Healthz /healthz { response HealthResp }   // public
    post Signup  /signup  { request SignupReq response User } // public
}

@middlewares(AuthRequired)
extend service Users {
    get    List /users      { response UserList }    // inherits AuthRequired
    delete Del  /users/{id} { request GetUserReq response OkResp } // inherits
}
```

Restrictions:

- The extended service must exist somewhere in the same package.
- Whole-service decorators (`@prefix`, `@group`) live on the primary `service` block; an extend block carrying them raises `service/extend-decorator-not-method`.
- Inside an extend block, individual methods may opt out of the inherited chain via `@ignoreMiddleware` (see [Opt-out: `@ignoreMiddleware`](#opt-out-ignoremiddleware) below).

## Opt-out: `@ignoreMiddleware`

A method with `@ignoreMiddleware` drops the inherited middleware chain (from primary + extend block) entirely. The method-level chain (if any) then starts from empty - useful for a public endpoint sitting inside an otherwise-authenticated service, or for an admin endpoint that needs a completely different chain:

```craftgo
@middlewares(AuthRequired, RateLimit)
service Secured {
    get ListItems / { response ItemList }              // chain: [AuthRequired, RateLimit]

    @ignoreMiddleware
    get Healthz /healthz { response HealthResp }       // chain: [] - no middleware

    @ignoreMiddleware
    @middlewares(BasicAuth, Audit)
    post Reset /reset { request ResetReq response OkResp } // chain: [BasicAuth, Audit] - reset + replace
}
```

The combine semantic is **clear-then-append**: `@ignoreMiddleware` clears the inherited chain, then any method-level `@middlewares(...)` decorators append to the now-empty chain.

`@ignoreMiddleware` is method-level only, takes no arguments. Pair it with `@ignoreSecurity` / `@ignoreTags` to drop those inherited chains too.

## Middleware order at runtime

For a request to a method like `DeleteUser` above, the chain executes outermost-first:

```
[runtime] Recovery (always outermost)
[runtime] srv.Use middleware in declaration order
[runtime] per-route mws passed to srv.Handle(pattern, h, mws...)
[DSL]     service-level @middlewares in declaration order
[DSL]     method-level @middlewares appended
[handler] decode body, validate, call logic, encode response
```

Recovery sits at the outermost position so a panic in any user middleware still surfaces as a 500 instead of crashing the server. The generated `routes.go` resolves the DSL `@middlewares(...)` names through `srv.With(names, handler)` and registers via the variadic `srv.Handle(pattern, h, mws...)` — both fold their lists outermost-first (first entry = first hit on the way in). See the [Runtime API](/reference/runtime-api#chain) for composing your own chains with `server.Chain`.

## Accessing middleware values from logic

A middleware that puts data on the request context is read by your service code:

```go
// in middleware
ctx := context.WithValue(r.Context(), userKey, principal)
next.ServeHTTP(w, r.WithContext(ctx))

// in service method
func (s *Service) GetUser(ctx context.Context, req *types.GetUserReq) (*types.User, error) {
    p, ok := ctx.Value(userKey).(*Principal)
    if !ok {
        return nil, types.NewUnauthorizedErr()
    }
    ...
}
```

Use a typed key (`type ctxKey int`) to avoid stringly-typed lookups.

## What declared middleware is not

- Not auto-imported - the stub file is a starting point you customize
- Not configured by the DSL - rate limits, allowed origins, JWT issuers belong in your config (`config.yaml`) and read inside the middleware
- Not transport-aware on its own - if you later add a gRPC transport, write a separate gRPC interceptor with the same business logic; the DSL middleware applies only to HTTP handlers
