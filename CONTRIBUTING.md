# Contributing to craftgo

Thanks for pitching in. craftgo is design-first: you write a small DSL and the CLI generates the Go. That one fact shapes how contributing works here, so this guide stays short.

## Ground rules

Be respectful and assume good faith - we follow the [Contributor Covenant](https://www.contributor-covenant.org/version/2/1/code_of_conduct/). Argue about ideas, not people.

## Getting started

1. Fork the repo, then branch off `main`.
2. Make your change.
3. Run `make ci` - lint, every test suite (the root one with `-race`), the codegen drift check, and build in one shot.
4. Open a pull request against [`craftgodotdev/craftgo`](https://github.com/craftgodotdev/craftgo).

New here? Issues tagged [good first issue](https://github.com/craftgodotdev/craftgo/issues?q=is%3Aopen+is%3Aissue+label%3A%22good+first+issue%22) and [help wanted](https://github.com/craftgodotdev/craftgo/issues?q=is%3Aopen+is%3Aissue+label%3A%22help+wanted%22) are the easy on-ramps - comment to claim one. Found a bug or have an idea? [Open an issue](https://github.com/craftgodotdev/craftgo/issues/new/choose); a clear report is worth as much as a patch.

## The craftgo-specific bit: generated code is committed

The generated output - handlers, types, OpenAPI specs under `example/` and `tests/e2e/` - lives in the repo. If your change touches codegen, regenerate and commit it:

```bash
make gen-all && git add -A
```

CI runs `make gen-diff` and fails if the committed output doesn't match what your code produces. It's the project's best review signal: a behavior change shows up as a diff in the generated files. And every fix should add a small case that exercises exactly what you changed, in the matching topic package under `tests/e2e/matrix/design/` (`numbers/`, `bindings/`, `events/`, ...). The matrix has to generate, so a design your fix now rejects gets a test in `internal/semantic` instead.

## Opening a PR

- `make fmt` - CI rejects code that isn't `gofmt -s` clean.
- `make ci` passes locally.
- Commits in logical units with plain-English messages; put the "why" in the PR description.
- One focused change per PR.

## Cutting a release

Maintainers only, and it is its own procedure: four modules are published from this repo at one version, and the two broker adapters have to swap a local `replace` for a real `pkg/events` version before they are tagged. [RELEASING.md](./RELEASING.md) walks through it; `make tag VERSION=vX.Y.Z DRY_RUN=1` prints the plan without touching anything.

Not sure about an approach? Open an issue first - aligning early beats a big rework. Welcome aboard. 🛠
