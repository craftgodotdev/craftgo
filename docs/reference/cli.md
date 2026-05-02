# CLI

The `craftgo` binary drives codegen and project commands. Run `craftgo help` for the up-to-date list.

## `craftgo init [path]`

Scaffolds a design folder with a starter `craftgo.design.yaml`.

```bash
craftgo init             # creates ./design
craftgo init api/spec    # creates ./api/spec
```

The path is the design folder; the manifest (`craftgo.design.yaml`) lands flat inside it. The Go module path is read from `go.mod` at gen time, so `init` itself does not need a `-package` flag.

After `init`, you write `.craftgo` files inside the design folder, then run `craftgo gen`.

## `craftgo gen [path]`

Generates types, validators, handlers, routes, and an OpenAPI spec from `.craftgo` files.

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
| `-c`, `--context <path>`  | Project root the `output.*` paths resolve against. Defaults to cwd when `-f` is given, otherwise to the parent of the manifest dir. |
| `-h`, `--help`            | Show help.                                                                            |

Without `-f`, `craftgo gen` walks upward from `<path>` (or cwd) probing direct subdirs at each level for a `craftgo.design.yaml`. The Go module path comes from `go.mod`, walking up from the project root - run `go mod init <module>` first if `go.mod` does not exist yet.

## `craftgo fmt [path] [-l] [-w]`

Canonical-format `.craftgo` files. Default action: write back in place.

```bash
craftgo fmt                # format all .craftgo files under cwd
craftgo fmt design         # format files under design/
craftgo fmt -l             # list files that would change (no write)
craftgo fmt -w design      # explicit write mode
```

Flags:

| Flag    | Effect                                                       |
| ------- | ------------------------------------------------------------ |
| `-l`    | List files that need formatting; do not modify.              |
| `-w`    | Write the formatted result back (default).                   |

Use `-l` in CI to fail when files are not formatted. Use the default in local pre-commit hooks.

## `craftgo version`

Prints the CLI version.

```bash
craftgo version
craftgo 0.x.x
```

## `craftgo help`

Top-level help. Same content as running `craftgo` with no arguments.

## Exit codes

| Code | Meaning                                      |
| ---- | -------------------------------------------- |
| 0    | Success                                      |
| 1    | Generic failure (unrecognized command, etc.) |
| 2    | Semantic errors found                        |

CI scripts can rely on these to fail builds.

## Project structure expected

`craftgo gen` expects:

- A `craftgo.design.yaml` somewhere (walked up from cwd, or provided via `-f`)
- A `go.mod` at the project root (so the Go module path can be resolved)
- `.craftgo` files under the design folder

Output paths are configured in `craftgo.design.yaml` and resolved against the project root (the directory containing `go.mod`, unless overridden with `-c`).
