# CLI

The `craftgo` binary drives codegen and project commands. Run `craftgo help` for the up-to-date list, or `craftgo <command> -h` for one command's usage. A bad flag or argument is reported once, followed by the command's usage.

## `craftgo init [path]`

Scaffolds a design folder with a starter `craftgo.design.yaml`.

```bash
craftgo init             # creates ./design
craftgo init api/spec    # creates ./api/spec
```

The path is the design folder; the manifest (`craftgo.design.yaml`) lands flat inside it. The Go module path is read from `go.mod` at gen time, so `init` itself does not need a `-package` flag.

After `init`, you write `.craftgo` files inside the design folder, then run `craftgo gen`.

## `craftgo gen [path]`

Generates types, validators, handlers, routes, and an OpenAPI spec from `.craftgo` files, and - from the `.proto` files in the same folder - the pb code, the gRPC server layer, its logic stubs and wiring. The protoc plugins run through `go tool`; pin them once with `go get -tool google.golang.org/protobuf/cmd/protoc-gen-go@latest google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest` (see [gRPC](/guide/grpc)).

```bash
craftgo gen                       # walk up from cwd for craftgo.design.yaml
craftgo gen design                # explicit path
craftgo gen -f design             # skip walk-up; use the given folder
craftgo gen -f design -c .        # set the project root for output paths
```

Flags:

| Flag                      | Effect                                                                                |
| ------------------------- | ------------------------------------------------------------------------------------- |
| `-f`, `--folder <path>`   | Path to the folder holding `craftgo.design.yaml`. Skips the walk-up.                  |
| `-c`, `--context <path>`  | Project root the `output.*` paths resolve against. Defaults to the parent of the design folder. |
| `--target <name>`         | Generate only the named target (`go`, `docs`); repeatable, default all. A narrowed run leaves the other targets' output untouched. |
| `-h`, `--help`            | Show help.                                                                            |

Without `-f`, `craftgo gen` walks upward from `<path>` (or cwd) probing direct subdirs at each level for a `craftgo.design.yaml`; with `-f`, a path is an error. The Go module path comes from `go.mod`, walking up from the project root - run `go mod init <module>` first if `go.mod` does not exist yet.

A key the manifest does not declare - a misspelling, or a key craftgo no longer reads - is ignored, and `craftgo gen` names it on stderr before generating:

```
craftgo: warning: output.typs is not a manifest key and is ignored
craftgo: warning: events.asyncapi is no longer a manifest key and is ignored - craftgo writes no asyncapi document
```

## `craftgo fmt [-l] [-w] [path]`

Canonical-format design files (`.craftgo`, `.cg`): every one under a directory, or the one file the path names. Default action: write back in place.

```bash
craftgo fmt                # format every design file under cwd
craftgo fmt design         # format files under design/
craftgo fmt -l             # list files that would change (no write)
craftgo fmt -w design      # explicit write mode
```

Flags go before the path; a flag after it, or a second path, is an error. A design folder without design files, one of protos alone, has nothing to format.

| Flag           | Effect                                                              |
| -------------- | ------------------------------------------------------------------- |
| `-l`           | List files that need formatting; do not modify. Exits 1 if any do.  |
| `-w`           | Write the formatted result back (default; with `-l`, list and write). |
| `-h`, `--help` | Show help.                                                          |

Use `-l` in CI to fail when files are not formatted. Use the default in local pre-commit hooks.

A file is formatted only when it has no errors, parse or semantic: a mistake the parser tolerates (a stray word read as a mixin, for example) must never be rearranged into something else. Such a file is reported on stderr with its diagnostics and left untouched, and the command exits 1. A file inside a design folder is checked with its whole project, so cross-package references resolve; any other file is checked on its own. A file whose formatted text would not parse, or would drop, duplicate, add or move a comment, is reported and left untouched the same way.

## `craftgo version`

Prints the CLI version alone, without a leading `v`. `--version` and `-v` do the same.

```bash
craftgo version
1.9.0
```

## `craftgo help`

Top-level help. Same content as running `craftgo` with no arguments.

## Exit codes

| Code | Meaning                                      |
| ---- | -------------------------------------------- |
| 0    | Success                                      |
| 1    | Any failure: parse or semantic errors, a generation error, `fmt` left a file with errors unformatted, or `fmt -l` listed a file |
| 2    | Usage error: no command, or an unknown one   |

CI scripts can rely on these to fail builds.

## Project structure expected

`craftgo gen` expects:

- A `craftgo.design.yaml` somewhere (walked up from cwd, or provided via `-f`)
- A `go.mod` at or above the project root (so the Go module path can be resolved)
- `.craftgo` files under the design folder

Output paths are configured in `craftgo.design.yaml` and resolved against the project root: the parent of the design folder, unless overridden with `-c`.
