# Configuration

A craftgo project has two configuration files. They live in different places and serve different stages.

## At a glance

| File                               | Read by      | When        | Controls                                                |
| ---------------------------------- | ------------ | ----------- | ------------------------------------------------------- |
| `<design>/craftgo.design.yaml`     | `craftgo` CLI | At gen time | Where generated files land + OpenAPI metadata           |
| `<project>/config/config.yaml`     | `main.go`    | At boot     | Server addr, OTel, metrics, your own custom fields      |

The first is read once when you run `craftgo gen`. The second is read every time the binary starts. Both are gen-once - craftgo writes them when missing and never overwrites your edits.

## Codegen config (`craftgo.design.yaml`)

Lives **inside** the design folder. The directory containing this file is the **design root**; its parent is the **project root** (the directory that holds `go.mod`).

```
myproject/
├── design/                          design root
│   ├── craftgo.design.yaml          ← this file
│   └── users/service.craftgo
├── go.mod
└── internal/                        generated, sits at project root
```

`craftgo init` creates a starter file. Default content matches every key's default, so an empty manifest works:

```yaml
output:
  types:      ./internal/types
  transport:  ./internal/transport
  routes:     ./internal/routes
  service:    ./internal/service
  middleware: ./internal/middleware
  svccontext: ./svccontext/svccontext.go
  openapi:    ./docs/openapi.yaml
  config:     ./config
  main:       ./main.go

openapi:
  title:    My API
  version:  1.0.0
  basePath: /api
  securitySchemes:
    bearer:
      type: http
      scheme: bearer
      bearerFormat: JWT
```

### `output.*` paths

All paths are relative to the **project root** (the parent of the design folder, the directory holding `go.mod`). Override any of them to relocate the corresponding artifact.

| Key          | Default                              | Kind                | Holds                                                     |
| ------------ | ------------------------------------ | ------------------- | --------------------------------------------------------- |
| `types`      | `./internal/types`                   | directory           | One subfolder per design package; `types.go`, `validate.go`, `enums.go`, `errors.go` |
| `transport`  | `./internal/transport`               | directory           | One subfolder per service; `<method>.go` per method |
| `routes`     | `./internal/routes`                  | directory           | Per-service `routes.go` plus an umbrella `routes.go` |
| `service`    | `./internal/service`                 | directory           | One subfolder per service; `<method>.go` per method (gen-once) |
| `middleware` | `./internal/middleware`              | directory           | One file per declared `middleware Name` (gen-once) |
| `svccontext` | `./svccontext/svccontext.go`         | **file path**       | Single Go file with the dependency container (gen-once); `middlewares.go` lands beside it |
| `openapi`    | `./docs/openapi.yaml`                | **file path**       | The generated OpenAPI 3.1 spec |
| `config`     | `./config`                           | directory           | `config.go`, `config.yaml`, `example.config.yaml` (all gen-once) |
| `main`       | `./main.go`                          | **file path**       | The project entry point (gen-once) |

The four "file path" entries point at exact files. The rest are directories where craftgo writes one subfolder per package or service.

### Customizing paths

A common use is to put generated artifacts at the project root instead of under `internal/`:

```yaml
output:
  types:     ./types
  transport: ./transport
  routes:    ./routes
  service:   ./service
```

Or split off the spec to a separate `api/` folder:

```yaml
output:
  openapi: ./api/openapi.yaml
```

Or skip generating an artifact entirely with `-`:

```yaml
output:
  main: -          # do not generate main.go
```

When `main: -` is set, craftgo also skips `config/`, `svccontext`, and `middleware` since those exist to support `main.go`. Useful for projects that import the generated types as a library and run their own server.

### Module path is auto-resolved

The `craftgo.design.yaml` does **not** carry a Go module / package field. craftgo reads `module <path>` from `go.mod` (walking up from the project root) at gen time and uses that for every Go import in generated files.

If `go.mod` is missing, `craftgo gen` fails with a clear error. Run `go mod init <module>` first.

### `openapi.*` block

Metadata that flows into the generated `openapi.yaml`.

| Key                | Type          | Effect                                                |
| ------------------ | ------------- | ----------------------------------------------------- |
| `title`            | string        | OpenAPI document title                                |
| `version`          | string        | OpenAPI document version                              |
| `basePath`         | string        | Path prefix prepended to every operation path         |
| `securitySchemes`  | map           | Named OpenAPI security schemes (see below)            |

`version` can also be set per-file via `@version("...")` - file-level decorator wins when present. `title` is manifest-only.

`basePath` rides into the `servers[0].url` field of the generated spec. If you need multiple servers or richer descriptions, edit the generated `openapi.yaml` after gen (it is committed; craftgo regenerates it on every run).

### `openapi.securitySchemes`

Each entry is a named scheme referenced from the DSL with `@security(<name>)`. Schemes:

```yaml
openapi:
  securitySchemes:
    bearer:                    # @security(bearer)
      type: http
      scheme: bearer
      bearerFormat: JWT

    apiKeyHeader:              # @security(apiKeyHeader)
      type: apiKey
      in: header
      name: X-API-Key

    oauth2:                    # @security(oauth2)
      type: oauth2

    openIdConnect:
      type: openIdConnect
      openIdConnectUrl: https://issuer.example.com/.well-known/openid-configuration
```

Supported `type` values: `http`, `apiKey`, `oauth2`, `openIdConnect`, `mutualTLS`. Per-type extra fields:

- `http`: `scheme` (e.g. `bearer`, `basic`), optional `bearerFormat`
- `apiKey`: `in` (`header` / `query` / `cookie`), `name`
- `oauth2`: scopes are application-defined - validated as strings, not against a fixed set
- `openIdConnect`: `openIdConnectUrl`

The semantic analyzer cross-checks every `@security(<name>)` reference against this map. Unknown names fail at gen time, not at deploy.

## Runtime config (`config/config.yaml`)

Generated by `craftgo gen` on first run alongside `config.go`. Read by `main.go` via `config.Load()`. Default content:

```yaml
server:
  addr: ":8080"
  handlerTimeout: 0s
  maxBodySize: 0
  compression:
    enabled: false
    minSize: 0
    level: 0

otel:
  enabled: true
  serviceName: my-app
  exporter: none
  endpoint: ""

metrics:
  enabled: true
  exporter: prometheus
  endpoint: ""
  adminAddr: ":9090"
  path: /metrics

docs:
  enabled: true
  ui: redoc
  path: /docs
  specPath: /openapi.yaml
```

### `server`

| Key                          | Type      | Effect                                                                  |
| ---------------------------- | --------- | ----------------------------------------------------------------------- |
| `addr`                       | string    | Listen address. `":8080"`, `"127.0.0.1:8080"`, etc.                     |
| `handlerTimeout`             | duration  | Global per-handler deadline. `0s` = no global cap; per-method `@timeout` overrides. |
| `maxBodySize`                | int       | Global request body cap in bytes. `0` = no cap.                         |
| `compression.enabled`        | bool      | Toggle gzip / deflate response compression.                             |
| `compression.minSize`        | int       | Skip compression when body is smaller. `0` falls back to 1024.          |
| `compression.level`          | int       | Compression level (1-9). `0` falls back to default.                     |

Compression is off by default. Turn it on only when not behind a compressing reverse proxy (Nginx, Envoy, CloudFront).

### `otel`

| Key           | Effect                                                                 |
| ------------- | ---------------------------------------------------------------------- |
| `enabled`     | Toggle the OTel HTTP middleware.                                       |
| `serviceName` | `service.name` resource attribute on every span.                       |
| `exporter`    | `none` / `stdout` / `otlp_grpc` / `otlp_http`.                          |
| `endpoint`    | OTLP collector address. Ignored for `none` / `stdout`.                 |

Setting `enabled: true` with `exporter: none` produces in-process spans whose IDs flow into log lines but are not exported.

### `metrics`

| Key          | Effect                                                                 |
| ------------ | ---------------------------------------------------------------------- |
| `enabled`    | Toggle the meter provider and admin scrape listener.                   |
| `exporter`   | `prometheus` / `otlp_grpc` / `otlp_http` / `none`.                      |
| `endpoint`   | OTLP collector address (ignored for prometheus / none).                |
| `adminAddr`  | Listen address for `/metrics` scrape (prometheus only).                |
| `path`       | URL path for the scrape (default `/metrics`).                          |

For `otlp_grpc` / `otlp_http`, `endpoint` may be a bare `host:port` (plaintext)
or a full URL whose scheme selects transport security - `http://…` (plaintext)
or `https://…` (TLS). `exporter: none` installs a silent meter (no scrape, no
push).

### `docs`

| Key        | Effect                                                                 |
| ---------- | ---------------------------------------------------------------------- |
| `enabled`  | Serve the OpenAPI document + a rendered docs page (on by default).     |
| `ui`       | `redoc` / `swagger` / `scalar` - the renderer (assets load from a CDN). |
| `path`     | HTML docs page route (default `/docs`).                                |
| `specPath` | Raw OpenAPI document route (default `/openapi.yaml`).                   |

The admin listener runs separately from the public API listener.

### Adding custom fields

Edit `config/config.go` (gen-once - your edits stick):

```go
type Config struct {
    Server  ServerConfig  `yaml:"server"`
    OTel    OTelConfig    `yaml:"otel"`
    Metrics MetricsConfig `yaml:"metrics"`

    DB struct {
        DSN string `yaml:"dsn"`
    } `yaml:"db"`
}
```

Then add the matching block to `config.yaml`:

```yaml
db:
  dsn: postgres://localhost/myapp
```

Read from your service via `svcCtx.Config.DB.DSN`.

### File location at runtime

`main.go` reads `config/config.yaml` by default. Pass a different path by editing the call:

```go
cfg, err := config.Load("/etc/myapp/config.yaml")
```

craftgo does not read environment variables. The YAML file is the single source of runtime configuration. Mount the right file per environment:

```
deploy/
├── config.dev.yaml
├── config.staging.yaml
└── config.production.yaml
```

CI / your deployer copies the right file to `config/config.yaml` before the binary starts.
