# Configuration

A craftgo project has two configuration files. They live in different places and serve different stages. [Architecture](/guide/architecture) shows what each stage produces.

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
  kind:       application
  types:      ./internal/types
  transport:  ./internal/transport
  routes:     ./internal/routes
  service:    ./internal/service
  middleware: ./internal/middleware
  wiring:     ./internal/wiring
  svccontext: ./svccontext/svccontext.go
  openapi:    ./docs/openapi.yaml
  config:     ./config
  main:       ./main.go
  # pb:       ./internal/pb        # only meaningful when the design holds a .proto
  # grpc:     ./internal/grpc

events: # only meaningful when the design declares events
  targets:
    - lang: go
      out: ./internal/events

proto: # only meaningful when the design holds .proto files
  includes: []
  plugins:
    go: ""
    goGrpc: ""

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
| `kind`       | `application`                        | mode                | `application` (default) or `contracts` - see below |
| `types`      | `./internal/types`                   | directory           | One subfolder per design package; `types.go`, `validate.go`, `enums.go`, `errors.go` |
| `transport`  | `./internal/transport`               | directory           | One subfolder per service; `<method>.go` per method |
| `routes`     | `./internal/routes`                  | directory           | Per-service `routes.go` plus an umbrella `routes.go` |
| `service`    | `./internal/service`                 | directory           | One subfolder per service; `<method>.go` per method (gen-once) |
| `middleware` | `./internal/middleware`              | directory           | One file per declared `middleware Name` (gen-once) |
| `wiring`     | `./internal/wiring`                  | directory           | The generated `wiring.Register` package `main.go` calls; `grpc.go` with `RegisterGRPC` lands beside it while a proto declares a service |
| `pb`         | `./internal/pb`                      | directory           | Where `protoc-gen-go` + `protoc-gen-go-grpc` write the pb code of every `.proto` under the design folder, mirroring the proto's directory. `"-"` runs no plugin (your own buf/protoc pipeline; every proto then needs `option go_package`) |
| `grpc`       | `./internal/grpc`                    | directory           | One subfolder per proto service: `server.go` plus `<rpc>.go` per RPC, delegating to the logic under `service` |
| `svccontext` | `./svccontext/svccontext.go`         | **file path**       | Single Go file with the dependency container (gen-once); `middlewares.go` lands beside it |
| `openapi`    | `./docs/openapi.yaml`                | **file path**       | The generated OpenAPI 3.1 spec. It describes the `.craftgo` design, so a project whose design holds only `.proto` files writes none |
| `config`     | `./config`                           | directory           | `config.go`, `config.yaml`, `example.config.yaml` (all gen-once) |
| `main`       | `./main.go`                          | **file path**       | The project entry point (gen-once) |

The four "file path" entries point at exact files. The other path keys are directories where craftgo writes one subfolder per package or service. `kind` is not a path at all - it says how much of the design this project generates.

### `events.*`

Configures the event output. `targets` lists the languages the event artefacts
are generated for - Go is a row in the list, not a privileged default, so the
manifest states where its artefacts land.

| Key                | Default                   | Holds                                                       |
| ------------------ | ------------------------- | ----------------------------------------------------------- |
| `targets[].lang`   | -                         | `go` - the only language target                              |
| `targets[].out`    | -                         | Destination directory, relative to the project root           |

```yaml
events:
  targets:
    - lang: go
      out: ./internal/events
```

Omit the block entirely and a design that declares events gets one Go target at
`./internal/events` - `./gen/events` under `output.kind: contracts`, whose
output is imported from other modules; a design that declares none generates
nothing either way. Set a target's `out` to `"-"` to skip it.

Transport and codec are deliberately absent: they are runtime wiring chosen in
`main.go`, not design-time facts. See [Events](/guide/events).

### `proto.*`

Read only when the design folder holds `.proto` files - see [gRPC](/guide/grpc).

| Key                | Default | Meaning                                                                                          |
| ------------------ | ------- | ------------------------------------------------------------------------------------------------ |
| `includes`         | `[]`    | Extra import roots, relative to the project root, for protos the design imports but does not own. Each file found there must carry `option go_package`. The design folder is always the first import root. |
| `plugins.go`       | `""`    | The `protoc-gen-go` command. Empty runs `go tool protoc-gen-go`, pinned by the `tool` directive in `go.mod`. A bare name is looked up on `PATH`, a path is run as given. |
| `plugins.goGrpc`   | `""`    | The `protoc-gen-go-grpc` command, same rules.                                                     |

### File and directory naming (`output.fileCase`)

The names craftgo derives from your DSL identifiers - the per-method `<method>.go`
handler and service files, the per-service directory, and each middleware file -
follow the case set by `output.fileCase`:

| `fileCase`        | method / RPC file | service directory | middleware file      |
| ----------------- | ----------------- | ----------------- | -------------------- |
| `snake` (default) | `create_user.go`  | `user_service/`   | `auth_middleware.go` |
| `kebab`           | `create-user.go`  | `user-service/`   | `auth-middleware.go` |
| `camel`           | `createUser.go`   | `userService/`    | `authMiddleware.go`  |

```yaml
output:
  fileCase: snake   # snake (default) | kebab | camel
```

It affects **on-disk names only**. URL routes are unaffected (they come from
`@prefix` and the method path), Go package names and identifiers are unchanged,
and the `types/<package>/` layout - named after the DSL package - is untouched.
A `@group` directory keeps the exact group string you wrote.

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

When `main: -` is set, craftgo also skips `config/` and the `svccontext.go` scaffold, since those exist to support `main.go` - the container type is then yours to write, and `svccontext/middlewares.go` and the middleware scaffolds are still generated against it. Useful for projects that import the generated types as a library and run their own server.

### Module path is auto-resolved

The `craftgo.design.yaml` does **not** carry a Go module / package field. craftgo reads `module <path>` from `go.mod` (walking up from the project root) at gen time and uses that for every Go import in generated files.

If `go.mod` is missing, `craftgo gen` fails with a clear error. Run `go mod init <module>` first.

### A contract library or a whole application (`output.kind`)

`application` - the default - generates both halves of the design: the contract half (payload types, the event library, the documents) and the application around it (transport handlers, routes, service stubs, middleware, wiring, config, `svccontext` and `main.go`).

`contracts` generates only the half **other projects import** - the payload types, the event library, the pb code of any proto, and the documents - and stops there. Nothing under `output.transport`, `output.routes`, `output.service`, `output.middleware`, `output.wiring`, `output.config`, `output.svccontext` or `output.main` is written.

Its two defaults move out of `internal/`, because Go forbids importing that path across modules and being imported is the whole point of the project:

| Key                     | `application`        | `contracts`     |
| ----------------------- | -------------------- | --------------- |
| `output.types`          | `./internal/types`   | `./gen/types`   |
| `output.pb`             | `./internal/pb`      | `./gen/pb`      |
| `events.targets[].out`  | `./internal/events`  | `./gen/events`  |

```yaml
# contracts/design/craftgo.design.yaml
output:
  kind:  contracts
  types: ./gen/types

events:
  targets:
    - lang: go
      out: ./gen/events
```

Each deployable around it holds its own design folder and its own manifest, generates its own application half, and imports the contracts project for the payload types and the event descriptors - see [Events layout](/guide/project-structure#events-layout) for the shape on disk. A deployable that serves no HTTP wants no OpenAPI document of its own - turn it off with `output.openapi: "-"`.

Switching an existing project to `kind: contracts` leaves whatever it generated as an application exactly where it was. A contracts project names no `output.transport`, `output.routes`, `output.service`, `output.wiring` or `output.middleware`, so nothing walks those directories any more. craftgo reports it - naming every path that still holds generated files - but deletes nothing: the directory may be one you now use for something else.

### Stale output is pruned

Every file craftgo REGENERATES opens with a generated header - `// Code generated by craftgo. DO NOT EDIT.` in Go, `# Generated by craftgo. DO NOT EDIT.` in the YAML documents. That header is the whole record: at the end of a run, craftgo walks the output directories the manifest names and **deletes every file carrying it that this run did not write**, then removes the directories that leaves empty. Nothing is stored on the side and nothing extra is committed.

It covers every output the run regenerates, not just the event contracts: the transport handlers, the routes, `wiring.go`, `svccontext/middlewares.go`, the event library, the `output.types` folder of a DSL package that is gone, the OpenAPI document, the gRPC server packages and - by the plugins' own headers, under `output.pb` only - the pb code of a proto that is gone. Rename a service and its old files go with its name; delete an `event` and its descriptor goes with it. Nothing else could know: the design that dropped them no longer says what they were called.

Three rules follow, and all three are load-bearing:

- **An output directory belongs to exactly one design.** Point a second design's manifest at a directory the first one writes into and the first run to finish deletes the other's output. Give each design its own `output.*` paths - or, when several deployables share contracts, generate those from one contracts project and import them (see [`output.kind`](#a-contract-library-or-a-whole-application-output-kind)). Two manifests generating the *same* design into one directory stay fine: they write the same files, so neither sweep finds anything to delete.
- **Gen-once territory is never walked.** `output.service`, `output.middleware`, `output.config` and `main.go` are written only when missing, so the sweep never enters those directories and your own code in them is never a question it has to answer. The project root is never swept either, whatever else the repository keeps there.
- **The header is the only thing the sweep reads.** Strip it from a generated file and the sweep stops seeing that file, so it survives a design that no longer produces it - but it is not protected: if the design still names that path, the next run rewrites the file, header and all.


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

The blocks a project gets follow its design: `server:` and `docs:` are absent from a project whose design declares gRPC services and no HTTP route, and `grpc:` is present only when a proto declares a service. A project whose `main.go` is its own (`output.main: "-"` writes none) keeps every block, since what it serves is not the design's to know.

```yaml
server:                # present when the design declares an HTTP route
  addr: ":8080"
  handlerTimeout: 0s
  maxBodySize: 0
  strictJSON: true
  compression:
    enabled: false
    minSize: 0
    level: 0

grpc:                  # present when a proto declares a service
  addr: ":9000"
  handlerTimeout: 0s
  reflection: true

logging:
  level: info

serviceName: my-app

otel:
  enabled: true
  exporter: none
  endpoint: ""

metrics:
  enabled: true
  exporter: prometheus
  endpoint: ""
  adminAddr: ":9090"
  path: /metrics

docs:                  # present when the design declares an HTTP route
  enabled: true
  ui: redoc
  path: /docs
  specPath: /openapi.yaml
```

### `server`

Present when the design declares an HTTP route.

| Key                          | Type      | Effect                                                                  |
| ---------------------------- | --------- | ----------------------------------------------------------------------- |
| `addr`                       | string    | Listen address. `":8080"`, `"127.0.0.1:8080"`, etc.                     |
| `handlerTimeout`             | duration  | Global per-handler deadline. `0s` = no global cap; per-method `@timeout` overrides. |
| `maxBodySize`                | int       | Global request body cap in bytes. `0` = no cap.                         |
| `strictJSON`                 | bool      | Reject a JSON body with an unknown field (`400 <field>: unknown field`) or data after the JSON value. `false` ignores both, as `encoding/json` does. |
| `compression.enabled`        | bool      | Toggle gzip / deflate response compression.                             |
| `compression.minSize`        | int       | Skip compression when body is smaller. `0` falls back to 1024.          |
| `compression.level`          | int       | Compression level (1-9). `0` falls back to default.                     |

Compression is off by default. Turn it on only when not behind a compressing reverse proxy (Nginx, Envoy, CloudFront).

### `grpc`

Present when a proto declares a service; see [gRPC](/guide/grpc).

| Key              | Type     | Meaning                                                                 |
| ---------------- | -------- | ----------------------------------------------------------------------- |
| `addr`           | string   | Listen address of the gRPC server. `":9000"`, `"127.0.0.1:9000"`, etc.   |
| `handlerTimeout` | duration | Default deadline for unary RPCs, carried on the handler context; a shorter client deadline still wins. Streams are not bounded. `0s` = no default. |
| `reflection`     | bool     | Serve `grpc.reflection` so `grpcurl` / `grpcui` work without the `.proto` files. Leave it off on a public listener. |

### `logging`

| Key     | Effect                                                                              |
| ------- | ----------------------------------------------------------------------------------- |
| `level` | Minimum log level: `debug` / `info` / `warn` / `error`. Default `info`; unrecognised values keep `info`. Feeds `log.SetLevel` in `main.go`, retuning the server and the generated logic layer together. |

### `otel`

| Key           | Effect                                                                 |
| ------------- | ---------------------------------------------------------------------- |
| `enabled`     | Toggle span emission. The HTTP wrapper emits both signals, so this does not stop metrics. |
| `serviceName` | Overrides the top-level `serviceName` for spans.                       |
| `exporter`    | `none` / `stdout` / `otlp_grpc` / `otlp_http`.                          |
| `endpoint`    | OTLP collector address. Ignored for `none` / `stdout`.                 |

Setting `enabled: true` with `exporter: none` produces in-process spans whose IDs flow into log lines but are not exported.

### `metrics`

| Key          | Effect                                                                 |
| ------------ | ---------------------------------------------------------------------- |
| `enabled`    | Toggle the meter provider and admin scrape listener.                   |
| `exporter`   | `prometheus` / `otlp_grpc` / `otlp_http` / `none`.                      |
| `endpoint`   | OTLP collector address (ignored for prometheus / none).                |
| `serviceName`| Overrides the top-level `serviceName` for metrics.                      |
| `adminAddr`  | Listen address for `/metrics` scrape (prometheus only).                |
| `path`       | URL path for the scrape (default `/metrics`).                          |

For `otlp_grpc`, `endpoint` may be a bare `host:port` (plaintext) or a full URL
whose scheme selects transport security - `http://…` (plaintext) or `https://…`
(TLS). `otlp_http` takes the URL form only; `telemetry.Init` refuses anything
else at startup. `exporter: none` installs a silent meter (no scrape, no push).

### Telemetry identity

The top-level `serviceName` is the `service.name` both signals report under -
what a backend keys on to tell one service's telemetry from another's. Each
block can override it, but only do that to report under a different name on
purpose. With none of the three set, the SDK falls back to
`unknown_service:<binary>`: survivable for a Prometheus scrape, where the
target labels already identify the process, but under OTLP push every service
in the fleet arrives at the collector under that one name.

The two `enabled` switches are independent. One `otelhttp` wrapper emits both
signals, so `otel.enabled: false` stops the spans while `http.server.*` keeps
flowing as long as `metrics.enabled` is true.

### `docs`

Present when the design declares an HTTP route: the docs page is served on the HTTP listener, and a project with no route starts none.

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
    Logging LogConfig     `yaml:"logging"`
    OTel    OTelConfig    `yaml:"otel"`
    Metrics MetricsConfig `yaml:"metrics"`
    Docs    DocsConfig    `yaml:"docs"`

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
