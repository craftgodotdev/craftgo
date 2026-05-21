# Changelog

## 0.2.0 — unreleased

Full LSP-backed language support.

- **Language server integration** via the bundled `craftgo-lsp` binary
  (install with `go install github.com/craftgodotdev/craftgo/cmd/craftgo-lsp@latest`).
- **Live diagnostics** matching `craftgo lint` (parse + semantic).
- **Hover** for decorators (with allowed levels and arg shape from the
  registry), built-in primitives, declared types, and reserved error
  categories.
- **Completion** for decorators (filtered by declaration site), declared
  types in scope, built-in primitives, and top-level keywords.
- **Go-to-definition** for cross-file declared identifiers.
- **Find references** for any declared symbol.
- **Document symbols** (outline view) for type/enum/error/scalar/
  middleware/service declarations and their members.
- **Document formatting** through `internal/format` — `Format Document`
  in the editor produces the same output as `craftgo fmt`.
- **Rename** (with prepareRename) — declarations and every reference in
  the file are rewritten in one workspace edit.
- New configuration:
  - `craftgo.serverPath` — override the binary path.
  - `craftgo.trace.server` — enable LSP message tracing.
- New command: `craftgo: Restart Language Server`.

## 0.1.0

Initial release — syntax-only support.

- Syntax highlighting for `.craftgo` files (keywords, HTTP verbs,
  built-in types, decorators, error categories, durations, sizes, path
  parameters).
- Bracket matching and auto-close for `{}`, `[]`, `()`, `<>`, `""`, `` ` ``.
- Line comment toggle (`//`).
- Snippet starter set for declarations, service/method blocks, and
  common field shapes.
