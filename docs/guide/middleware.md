# Middleware

Middleware in craftgo is a regular `func(http.Handler) http.Handler`. There are two ways to wire it up: directly in `main.go`, or declared in the DSL and attached to services / methods.

This page is the **HTTP** side. Event consumers have their own middleware - a different Go shape (`func(sub events.Subscription, next events.Handler) events.Handler`), never declared in the DSL, but the same outermost-first ordering as below. See [Middleware](/guide/events#middleware) in the events guide.

## At a glance

```
[ 1 ] Runtime middleware - srv.Use(...) in main.go - applies to every request
[ 2 ] Declared middleware - DSL keyword + @middlewares(...) - per-service or per-method
```

(For consumers: `bus.Use(...)` is the same idea on the events side - a chain built in ordinary Go where the bus is, covered in the [events guide](/guide/events#middleware).)

Use **runtime middleware** for cross-cutting concerns that apply globally regardless of the API contract: access log, compression, request-wide guards of your own. Tracing and metrics go in with `server.WithTelemetry` at `server.New`, and Recovery is built into the server.

Use **declared middleware** when the DSL needs to know about it: which services / methods opt in, which order, how it surfaces in OpenAPI's security section.

The rest of this page covers each in detail.

## Runtime middleware (no DSL involved)

For cross-cutting concerns that apply globally regardless of the API contract, use `srv.Use`:

```go
srv := server.New(svcCtx, server.WithTelemetry(tel.HTTPMiddleware())) // traces + metrics, outside Recovery and every Use
srv.Use(server.AccessLog(srv.Logger()))
srv.SetDefaultMaxBodySize(1 << 20) // default body cap; a per-method @maxBodySize overrides it
```

Order matters. The first `Use` is the outermost frame of the `Use` chain; the `WithTelemetry` middleware and Recovery wrap the whole chain.

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

A middleware name is global to the whole design: any package references it by its bare name (or qualified, `shared.AuthRequired`), and declaring one name in two packages is a `middleware/collision` error. Declarations do not live inside a service body.

Codegen produces:

- A typed slot on `ServiceContext.Middlewares` for each name (e.g. `svc.AuthRequired`, `svc.RateLimit`)
- An empty stub at `internal/middleware/<name>_middleware.go` you fill in
- A registration step in main.go that wires your stubs into the slots

The DSL only carries the contract (the name and where it applies). The implementation lives in the stub.

### Implementing a stub

```go
// internal/middleware/auth_required_middleware.go
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
    get GetUser /{id} { request GetUserReq  response User }
    post CreateUser / { request CreateUserReq  response User }
    delete DeleteUser /{id} { request GetUserReq }
}
```

All three methods inherit the same chain.

### Per-method

A method-level `@middlewares` appends additional frames after the service-level chain:

```craftgo
@prefix("/users")
@middlewares(AuthRequired)
service UserService {
    get GetUser /{id} { request GetUserReq  response User }

    @middlewares(AdminOnly)
    delete DeleteUser /{id} { request GetUserReq }
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

An `extend` block can also carry any method decorator but `@operationId` (`@middlewares`, `@security`, `@tags`, `@deprecated`, `@timeout`, ...) - those propagate to every method inside. Useful for the 50/50 split: primary holds public endpoints, an extend block holds the authenticated chain.

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
- `@prefix` lives on the primary `service` block; an extend block carrying it raises `service/extend-decorator-not-method`. `@group` is allowed on an extend block and moves that block's methods into the group's directory.
- Inside an extend block, individual methods may opt out of the inherited chain via `@ignoreMiddleware`, and an `@ignoreMiddleware` on the block itself opts out every method of the block (see [Opt-out: `@ignoreMiddleware`](#opt-out-ignoremiddleware) below).

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

`@ignoreMiddleware` takes no arguments. Pair it with `@ignoreSecurity` / `@ignoreTags` to drop those inherited chains too. On an `extend service` block it applies to every method of the block, as if each method wrote it: the primary service's chain is dropped, and the block's own `@middlewares(...)` start the chain afresh.

## Middleware order at runtime

For a request to a method like `DeleteUser` above, the chain executes outermost-first:

```
[runtime] server.WithTelemetry middleware (traces + metrics), when set
[runtime] Recovery (installed by the server)
[runtime] srv.Use middleware in declaration order
[runtime] CORS, when srv.SetCORS is set
[DSL]     service-level @middlewares in declaration order  } the per-route mws routes.go
[DSL]     method-level @middlewares appended                } passes to srv.Handle
[runtime] @timeout / @maxBodySize (server.WithLimits), else the server's default handler timeout and body cap
[handler] decode body, validate, call logic, encode response
```

The health probes are answered ahead of all of it, inside Recovery only.

An HTTP middleware does its work on the way IN, so the first one listed is the first to run and that reads the way it sounds. A consumer [`events.Chain`](/guide/events#middleware) folds the same way - first listed is outermost - so the intuition carries over unchanged.

Recovery wraps every `srv.Use` and per-route middleware, so a panic in any of them surfaces as 500 `{"message":"internal server error"}` with its stack logged, instead of a connection `net/http` drops. Only the `WithTelemetry` middleware sits outside it, so the panic line carries the trace ids; a panic in that middleware itself is not recovered. The generated `routes.go` reads the DSL `@middlewares(...)` values as typed fields on `svcCtx` (e.g. `svcCtx.AuthRequired`, `svcCtx.RateLimit`, pre-wired at startup by `main.go`) and passes them as variadic args to `srv.Handle(pattern, h, mws...)`. The service- and method-level chains are merged into one flat, outermost-first list before the call (first entry = first hit on the way in). See the [Runtime API](/reference/runtime-api#chain) for composing your own chains with `server.Chain`.

## Accessing middleware values from logic

A middleware that puts data on the request context is read by your service code:

```go
// in middleware
ctx := context.WithValue(r.Context(), userKey, principal)
next.ServeHTTP(w, r.WithContext(ctx))

// in the logic stub
func (l *GetUserService) GetUser(req *types.GetUserReq) (*types.User, error) {
	p, ok := l.ctx.Value(userKey).(*Principal)
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
