# LSP / IDE

craftgo ships its own language server, `craftgo-lsp`. It powers completion, hover, diagnostics, go-to-definition, and formatting for `.craftgo` files.

The only first-party editor integration today is the **VS Code extension**. Other editors that speak LSP can spawn the binary directly, but they are not officially supported and may need extra setup for syntax highlighting (no shipped grammar package outside the VS Code extension).

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

## Building the VS Code extension from source

```bash
git clone https://github.com/craftgodotdev/craftgo
cd craftgo/extensions/vscode
npm install
npm run package
code --install-extension craftgo-*.vsix
```

Reload the VS Code window after install.

## Features

### Completion

Type `@` inside a field to see decorators valid for the field's type:

```craftgo
type User {
    age int @|
    //      ^ shows: required, min, max, range, positive, negative, multipleOf, default, ...
}
```

The list is filtered by the field's primitive: `@length` only appears on string fields, `@minItems` only on arrays.

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
//        ^ shows: 10ms, 10s, 10m, 10h
```

### Hover

Hover over an identifier to see its declaration and doc comment. Works on:

- Decorators (`@length`, `@format`, `@default`, ...) - shows purpose, allowed sites, argument shape
- Type references inside fields - shows the type's declaration
- Enum values inside `@default(EnumValue)` - shows the enum value's source

### Diagnostics

Errors appear inline as you type:

- `decorator/unknown` - `@nope` is not registered
- `decorator/placement` - decorator appears at the wrong site (`@prefix` on a field)
- `decorator/conflict` - incompatible decorator pair (`@sensitive` with `@length`)
- `decorator/default-needs-optional` - `@default` on a non-optional field (warning; format auto-fixes)
- `decorator/typemismatch` - validator on the wrong primitive (`@length` on `int`)
- `field/duplicate` - same field name twice
- `service/path-conflict` - two methods on the same verb + path
- `enum/value-collision` - two values produce the same Go constant

### Go-to-definition

`Cmd+click` (or `gd` in vim-mode) on a type reference jumps to its declaration. Works across files in the same package and across packages (a `shared.Type` reference jumps to the `shared` package's declaration).

### Format

The `craftgo fmt` CLI rewrites a file in canonical form: aligned field columns, consistent decorator spacing, sorted imports. The VS Code extension wires this to the editor's "Format Document" command.

## Other editors

Any editor that speaks LSP can spawn `craftgo-lsp`. There is no first-party plugin for Neovim, Helix, Zed, or others today. If you set one up:

- The binary lives at `$(which craftgo-lsp)` after `go install`
- It speaks plain LSP over stdin/stdout
- File extension is `.craftgo`, scope is `source.craftgo`

Without the VS Code extension you will not have syntax highlighting; the language server still provides completion, hover, and diagnostics. Contributions for other editor integrations are welcome.

## Troubleshooting

### Completion stopped working

Reinstall and restart:

```bash
go install github.com/craftgodotdev/craftgo/cmd/craftgo-lsp@latest
# in VS Code: Cmd+Shift+P -> craftgo: Restart Language Server
```

### Cross-file references show as unresolved

The LSP looks for sibling `.craftgo` files in the same folder. If your enum lives in `enums.craftgo` and your type uses it from `types.craftgo`, both files must sit in the same `design/<package>/` subfolder.

### Errors on a syntactically valid file

The LSP and the `craftgo` CLI use the same semantic analyzer. Run `craftgo lint <design-dir>` to reproduce. If the message is unclear, file a bug.
