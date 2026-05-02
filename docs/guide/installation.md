# Installation

## Requirements

- Go 1.24 or later
- A POSIX shell (macOS, Linux, WSL on Windows)

## CLI

The main `craftgo` binary runs codegen and project commands.

```bash
go install github.com/dropship-dev/craftgo/cmd/craftgo@latest
```

Verify:

```bash
craftgo --help
```

The binary lands in `$GOBIN` (or `$GOPATH/bin`). Make sure that directory is on your `PATH`.

## Language server (optional)

The LSP powers editor features: completion, diagnostics, hover, go-to-definition, formatting.

```bash
go install github.com/dropship-dev/craftgo/cmd/craftgo-lsp@latest
```

The LSP is invoked by editor extensions. You do not run it directly.

## VS Code extension

Install the [craftgo extension](https://marketplace.visualstudio.com/items?itemName=craftgo.craftgo) from the marketplace, or build from source:

```bash
git clone https://github.com/dropship-dev/craftgo
cd craftgo/extensions/vscode
npm install
npm run package
code --install-extension craftgo-*.vsix
```

Reload the window. `.craftgo` files now have syntax highlighting, completion, hover, and diagnostics.

## Runtime library

Add craftgo as a dependency in your project:

```bash
go get github.com/dropship-dev/craftgo
```

Generated code imports from `pkg/server`, `pkg/log`, `pkg/metrics`, `pkg/otel`. You do not import these directly in most cases.

## Other editors

The LSP speaks standard LSP. Configure your editor to spawn `craftgo-lsp` for files with extension `.craftgo`.

Neovim with `nvim-lspconfig`:

```lua
local lspconfig = require('lspconfig')
local configs = require('lspconfig.configs')

if not configs.craftgo then
  configs.craftgo = {
    default_config = {
      cmd = { 'craftgo-lsp' },
      filetypes = { 'craftgo' },
      root_dir = lspconfig.util.root_pattern('go.mod', '.git'),
    },
  }
end

lspconfig.craftgo.setup({})
```

## Updating

```bash
go install github.com/dropship-dev/craftgo/cmd/craftgo@latest
go install github.com/dropship-dev/craftgo/cmd/craftgo-lsp@latest
go get -u github.com/dropship-dev/craftgo
```

After updating the LSP binary, restart your editor's language server (in VS Code: `Cmd+Shift+P` -> `craftgo: Restart Language Server`).
