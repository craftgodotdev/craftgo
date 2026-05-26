# craftgo — VSCode extension

Syntax highlighting, snippets, and language configuration for [CraftGo](https://github.com/craftgodotdev/craftgo) DSL files (`.craftgo`).

## Features

- **Syntax highlighting** — keywords, HTTP verbs, built-in types, decorators, error categories, durations (`12s`, `500ms`), sizes (`10MB`), path parameters (`/{id}`).
- **Bracket matching + auto-close** — `{}`, `[]`, `()`, `<>`, `""`, `` ` ``.
- **Comment toggle** — `//`.
- **Snippets** — service blocks, type/enum/error declarations, HTTP method scaffolds, common field shapes. Type `srv`, `t`, `e`, `get`, `post`, ... and Tab.

## Status

Full LSP-backed support. The extension speaks LSP to a separate `craftgo-lsp` binary written in Go, so live diagnostics match what `craftgo lint` would print on the CLI exactly.

### Features (LSP)

- Live diagnostics (parser + semantic analyser).
- Hover for decorators (with registry doc, levels, args), built-in primitives, declared types, error categories.
- Completion of decorators (filtered by site), types in scope, primitives, keywords.
- Go-to-definition + Find references.
- Document outline (symbols).
- Document formatting (`craftgo fmt` parity).
- Rename (declarations + every in-file reference).

## Install

### Prerequisite: LSP server

```sh
go install github.com/craftgodotdev/craftgo/cmd/craftgo-lsp@latest
```

Make sure `$GOPATH/bin` is on `$PATH`. Or set `craftgo.serverPath` in VSCode settings to an absolute path.

### Extension from source

```sh
cd extensions/vscode
npm install
npm run package
code --install-extension craftgo-0.2.0.vsix
```

### From Marketplace

Pending first release.

## Snippet reference

| Prefix   | Expands to                                  |
| -------- | ------------------------------------------- |
| `pkg`    | `package …`                                 |
| `imp`    | `import "…"`                                |
| `t`      | `type Name { … }`                           |
| `e`      | `enum Name { … }`                           |
| `err`    | `error <Category> Name`                     |
| `scal`   | `scalar Name <primitive>`                   |
| `mw`     | `middleware Name(…)`                        |
| `srv`    | `@prefix … service NameService { … }`       |
| `esrv`   | `extend service NameService { … }`          |
| `get` …  | HTTP method block with request/response    |
| `stream` | Streaming method block                      |
| `req`    | `request Type`                              |
| `resp`   | `response Type`                             |
| `fstr`   | Required string field with length           |
| `fstr?`  | Optional string field                       |
| `fid`    | `id string @path @required @length(1, 64)` |
| `@doc`   | `@doc("…")`                                 |
| `@dep`   | `@deprecated("…")`                          |

## License

MIT — see [LICENSE](../../LICENSE) at repo root.
