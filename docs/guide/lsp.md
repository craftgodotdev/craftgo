# LSP / IDE

craftgo ships its own language server, `craftgo-lsp`. It powers completion, hover, diagnostics, go-to-definition, find references, rename, document and workspace symbols, document highlight, signature help and formatting for `.craftgo` files (the shorter `.cg` extension is also recognised). The server watches and resolves both extensions; to have your editor launch the server for `.cg` files, associate that extension with the craftgo language on the client side.

The only first-party editor integration is the **VS Code extension**. Other editors that speak LSP can spawn the binary directly, but they are not officially supported and may need extra setup for syntax highlighting (no shipped grammar package outside the VS Code extension).

## Install the LSP

```bash
go install github.com/craftgodotdev/craftgo/cmd/craftgo-lsp@latest
```

The binary lands in `$GOBIN` (or `$GOPATH/bin`). Make sure the directory is on your `PATH`.

## VS Code

Install the [craftgo extension](https://marketplace.visualstudio.com/items?itemName=craftgo.craftgo) from the marketplace. The extension auto-spawns `craftgo-lsp` for any file with the `.craftgo` extension. Syntax highlighting, snippets, and the language server all activate together.

Verify it works:

1. Open a `.craftgo` file
2. Type `@` inside a field
3. The completion popup should list valid decorators

If completion stops working after you upgrade craftgo, restart the language server:

```
Cmd+Shift+P -> craftgo: Restart Language Server
```

The LSP runs as a separate binary; rebuilding craftgo's source does not refresh the running server until you reinstall the binary and restart it.

## Features

### Completion

Type `@` inside a field to see decorators valid for the field's type:

```craftgo
type User {
    age int @|
    //      ^ shows: gt, gte, lt, lte, range, positive, negative, multipleOf, default, ...
}
```

The list is filtered by the field's type: `@length` appears on string and bytes fields, `@minItems` on arrays and maps.

### Smart argument completion

Inside `@default(...)` on an enum field, only the enum's values are suggested:

```craftgo
enum Status { Active  Inactive  Pending }

type User {
    status Status @default(|)
    //               ^ shows: Active, Inactive, Pending
}
```

Inside `@timeout(...)` or `@maxBodySize(...)`, common values appear as presets. Once you type a number, the matching unit suffixes get offered:

```craftgo
@timeout(10|)
//        ^ shows: 10ns, 10us, 10µs, 10ms, 10s, 10m, 10h
```

### Hover

Hover over an identifier to see its declaration and doc comment. Works on:

- Decorators (`@length`, `@format`, `@default`, ...) - shows purpose, allowed sites, argument shape
- Type references inside fields - shows the declaration's header line (`type Contact`, `enum Status`) and its `//` doc comment
- HTTP verbs, built-in primitives, error categories, field names, and middleware and error names inside `@middlewares(...)` / `@errors(...)`

### Diagnostics

Errors appear inline as you type:

- `decorator/unknown` - `@nope` is not registered
- `decorator/placement` - decorator appears at the wrong site (`@prefix` on a field)
- `decorator/conflict` - incompatible decorator pair (`@sensitive` with `@length`)
- `decorator/default-needs-optional` - `@default` on a non-optional field (warning; format auto-fixes)
- `decorator/typemismatch` - validator on the wrong primitive (`@length` on `int`)
- `field/duplicate` - same field name twice
- `service/duplicate-route` - two methods of one service on the same verb + route
- `path/collision` - two methods, in any services, whose routes net/http's ServeMux cannot both register
- `enum/value-collision` - two values produce the same Go constant (warning)

A key of `craftgo.design.yaml` that craftgo does not read shows as a warning at the top of the manifest, naming the key, and an edit that stops the manifest from loading shows the error there.

### Go-to-definition

`Cmd+click` (or `gd` in vim-mode) on a type reference jumps to its declaration. Works across files in the same package and across packages (a `shared.Type` reference jumps to the `shared` package's declaration). On a value inside `@default(...)` or `@example(...)` of an enum-typed field it jumps to that enum value; on a name inside `@middlewares(...)` or `@errors(...)`, to that middleware or error.

### Format

The `craftgo fmt` CLI rewrites a file in canonical form: aligned field columns, consistent decorator spacing, tab indentation. The VS Code extension wires this to the editor's "Format Document" command. A buffer with an error, parse or semantic, is left untouched: fix the diagnostic first, then format. So is a buffer whose formatted text would not parse, or would lose or move a comment.

## Other editors

Any editor that speaks LSP can spawn `craftgo-lsp`. There is no first-party plugin for Neovim, Helix, Zed, or others. If you set one up:

- The binary lives at `$(which craftgo-lsp)` after `go install`
- It speaks plain LSP over stdin/stdout
- File extensions are `.craftgo` and `.cg`; the grammar scope is `source.craftgo`

Without the VS Code extension you will not have syntax highlighting; the language server still provides completion, hover, and diagnostics. Contributions for other editor integrations are welcome.

## Troubleshooting

### Completion stopped working

Reinstall and restart:

```bash
go install github.com/craftgodotdev/craftgo/cmd/craftgo-lsp@latest
# in VS Code: Cmd+Shift+P -> craftgo: Restart Language Server
```

### Cross-file references show as unresolved

The LSP analyses every design file under the design root - the folder holding `craftgo.design.yaml` - with open buffers in place of their saved copies. A file with no loadable manifest above it is analysed on its own, so everything declared in other files shows as unresolved: check that `craftgo.design.yaml` sits above the file and loads (`craftgo gen` reports why it does not). Inside the project a bare name resolves within the file's package - every file declaring the same `package` name, in any folder - and a declaration of another package needs its qualifier (`shared.Contact`).

### Errors on a syntactically valid file

The LSP and the `craftgo` CLI use the same semantic analyzer. Run `craftgo fmt -l <design-dir>` to reproduce: it lists every file with errors under `<file>: not formatted, fix these first:` and writes nothing (`craftgo gen` stops on the same errors). Warnings such as `decorator/default-needs-optional` show only in the editor. If the message is unclear, file a bug.
