# Releasing

Five modules are published from this repository and they all share one
version:

| Module                                              | Directory          | Tag                        |
| --------------------------------------------------- | ------------------ | -------------------------- |
| `github.com/craftgodotdev/craftgo`                  | `.`                | `v1.8.0`                   |
| `github.com/craftgodotdev/craftgo/pkg/events`       | `pkg/events`       | `pkg/events/v1.8.0`        |
| `github.com/craftgodotdev/craftgo/pkg/wire`         | `pkg/wire`         | `pkg/wire/v1.8.0`          |
| `github.com/craftgodotdev/craftgo/pkg/events/nats`  | `pkg/events/nats`  | `pkg/events/nats/v1.8.0`   |
| `github.com/craftgodotdev/craftgo/pkg/events/kafka` | `pkg/events/kafka` | `pkg/events/kafka/v1.8.0`  |

`pkg/events` and `pkg/wire` are the two dependency-free runtime modules: the
adapters require `pkg/events`, and a generated contract package with a `bytes
@format(raw)` field requires `pkg/wire`. Neither requires anything of ours, so
both are tagged in phase 1 alongside everything else - by the time a consumer
resolves an adapter or a contract, every module it can reach is published.

`example/*` and `tests/e2e/matrix` are modules too, but they are never
published - they exist so the generated code is compiled and tested.

A nested module is tagged with its directory as the prefix. That is not a
convention this repo invented: it is how the Go module proxy finds a module
that does not live at the repository root, so `pkg/events/v1.8.0` is the only
name `go get github.com/craftgodotdev/craftgo/pkg/events@v1.8.0` will resolve.

```
make tag-list              # the four latest tags of each module
make tag VERSION=v1.8.0    # phase 1 - local only, never pushes
git push origin ...        # printed by `make tag`, run it yourself
make tag-sync VERSION=v1.8.0  # phase 2 - checksums, after the push
```

## Why it takes two phases

`pkg/events/nats` and `pkg/events/kafka` import `pkg/events`. In the
repository they resolved it with

```
replace github.com/craftgodotdev/craftgo/pkg/events => ../
require github.com/craftgodotdev/craftgo/pkg/events v0.0.0-00010101000000-000000000000
```

**A `replace` in a dependency's `go.mod` is ignored.** Only the main module's
replaces apply. So a project that runs
`go get github.com/craftgodotdev/craftgo/pkg/events/nats@v1.8.0` reads that
`require` line verbatim, tries to fetch `pkg/events v0.0.0-000...`, and fails.
The published adapter has to require a real, tagged `pkg/events` version.

That is the whole problem, and it is why a release cannot be one `git push`:

1. the adapters must name a `pkg/events` version that exists, and
2. `go mod tidy` in an adapter can only verify that version once the tag is
   on origin.

So the work is split. Phase 1 writes everything that needs no network. Phase 2
is the one step that does, and it runs after you have pushed.

## Phase 1 - `make tag VERSION=vX.Y.Z`

Local only. **It never pushes**, and it never touches the network, so you can
read the plan before anything leaves your machine. `DRY_RUN=1` prints every
git and go command instead of running the ones that write:

```
make tag VERSION=v1.8.0 DRY_RUN=1
```

It refuses to run when `VERSION` is missing or is not `vX.Y.Z`, when `HEAD`
is detached, when the working tree is dirty, when any of the five tags already
exists, or when `CHANGELOG.md` either already carries a `## [X.Y.Z]` section
or lists nothing under `## [Unreleased]`. (Under `DRY_RUN=1` the dirty-tree,
existing-tag and changelog refusals become warnings - a dry run writes
nothing, so it can still show you the plan from a work-in-progress tree.)
Nothing is checked against origin, because nothing here touches the network;
if an earlier release stopped halfway, `git fetch --tags` and look before you
tag again.

`make tag` only cuts `vX.Y.Z`. A pre-release (`v1.8.0-rc.1`) is a manual tag -
the release workflow accepts one and GoReleaser marks it pre-release, but the
nested modules are not part of that path.

What it does:

1. In `pkg/events/nats` and `pkg/events/kafka`:

   ```
   go mod edit -dropreplace=github.com/craftgodotdev/craftgo/pkg/events \
               -require=github.com/craftgodotdev/craftgo/pkg/events@vX.Y.Z
   ```

   The first release drops the `replace` for good. Every later release only
   moves the `require` line, because `-dropreplace` on an absent replace is a
   no-op.

2. Bumps the version the binaries report - `var version` in
   `cmd/craftgo/main.go` and `Version` in `internal/lsp/server.go` - to the
   bare `X.Y.Z`. Release builds overwrite both through `-ldflags`; the source
   value is the fallback for `go install` from a checkout.

3. Rolls `CHANGELOG.md`: everything listed under `## [Unreleased]` becomes
   `## [X.Y.Z] - YYYY-MM-DD [UTC+7]`, dated today in `Asia/Ho_Chi_Minh` like
   every heading below it, and an empty `## [Unreleased]` stays on top for the
   next cycle. A section that lists nothing is refused rather than released,
   and so is a version whose section is already there.

4. Commits `release: vX.Y.Z`.

5. Creates five annotated tags on that commit: `vX.Y.Z` plus the four
   prefixed ones.

6. Prints the push - one command, the commit and all five tags together:

   ```
   git push origin <sha>:refs/heads/<branch> \
       v1.8.0 \
       pkg/events/v1.8.0 \
       pkg/wire/v1.8.0 \
       pkg/events/nats/v1.8.0 \
       pkg/events/kafka/v1.8.0
   ```

Run `make ci` before you tag. Nothing in `make tag` runs the test suite.

### What it deliberately does not do

- **No push.** A release becomes irreversible the moment a tag is on origin -
  the proxy caches it and the version can never be re-cut. A human presses
  that button.
- **No `go mod tidy`.** After step 1 the adapters require `pkg/events@vX.Y.Z`,
  which does not exist yet; tidy would go looking for it and fail. The commit
  therefore carries a `go.mod` whose `pkg/events` line has no matching
  `go.sum` entry. That is fine for consumers - `go get` computes and records
  its own checksums from the tag - and it is fine locally, because `go.work`
  resolves `pkg/events` from the working tree and a workspace module needs no
  sum. It is only `go mod tidy` itself that cannot work offline, which is what
  phase 2 fixes.

Between phase 1 and the push, `make tidy` will fail in `pkg/events/nats` and
`pkg/events/kafka` for exactly that reason. Everything else - `make ci`,
`make gen-all`, `go build`, `go test`, golangci-lint - keeps working.

## The push

Run the printed command. It pushes the release commit and all five tags in
one go, so the module versions and the branch can never drift apart.

`.github/workflows/release.yml` reacts to the root tag only
(`v[0-9]+.[0-9]+.[0-9]+`) and runs GoReleaser: cross-compiled `craftgo` and
`craftgo-lsp` binaries, archives, checksums, a GitHub Release. The four
nested tags cannot match that filter - a tag filter pattern is anchored at the
start of the tag name and its `*` never crosses a `/` - so they publish module
versions and nothing else.

## Phase 2 - `make tag-sync VERSION=vX.Y.Z`

Once the tags are on origin:

```
make tag-sync VERSION=v1.8.0
```

It runs, in each adapter,

```
GOFLAGS=-mod=mod GOPROXY=direct go mod tidy
```

`GOPROXY=direct` reads the tag straight from GitHub rather than waiting for
`proxy.golang.org` to notice it. If `go.sum` changed it commits
`release: vX.Y.Z checksums`. It does not push and it does not move any tag:
the published versions already point at the release commit, and a consumer
verifies the module against its own checksum, not against this repo's
`go.sum`.

`DRY_RUN=1` works here too.

## `go.work`

`go.work` is tracked (and `.gitignore` deliberately does not ignore it). It is
what makes local development work now that the adapters have no `replace`:
inside the workspace, `github.com/craftgodotdev/craftgo/pkg/events` resolves
to `pkg/events/` on disk (and `pkg/wire` likewise), so a change there is
visible to the adapters, the
examples and the e2e fixture before it is tagged or pushed.

`example/*` and `tests/e2e/matrix` keep their `replace` lines even though the
workspace would cover them. `go mod tidy` ignores workspaces, so those
replaces are what let `make tidy` resolve their placeholder `v0.0.0` requires
without a network round-trip. Removing them would break `make tidy`.

One consequence worth knowing: in workspace mode Go runs minimal version
selection across every member, so a dependency shared with an example can
resolve higher than it would for someone installing the module on its own.
Release builds are therefore pinned with `GOWORK=off` in `.goreleaser.yaml`,
so a published binary matches the published module graph.

## For consumers (dropping your own `replace`)

A project that was tracking craftgo from a checkout will have something like
this in its `go.mod`:

```
require (
	github.com/craftgodotdev/craftgo v0.0.0
	github.com/craftgodotdev/craftgo/pkg/events v0.0.0
	github.com/craftgodotdev/craftgo/pkg/events/nats v0.0.0
)

replace github.com/craftgodotdev/craftgo => ../craftgo
replace github.com/craftgodotdev/craftgo/pkg/events => ../craftgo/pkg/events
replace github.com/craftgodotdev/craftgo/pkg/events/nats => ../craftgo/pkg/events/nats
```

Once a release is published, all of that collapses to plain requires:

```
go mod edit \
  -dropreplace=github.com/craftgodotdev/craftgo \
  -dropreplace=github.com/craftgodotdev/craftgo/pkg/events \
  -dropreplace=github.com/craftgodotdev/craftgo/pkg/events/nats
go get github.com/craftgodotdev/craftgo@v1.8.0 \
       github.com/craftgodotdev/craftgo/pkg/events@v1.8.0 \
       github.com/craftgodotdev/craftgo/pkg/events/nats@v1.8.0
go mod tidy
```

The three modules move together, so ask for the same version for each.
`pkg/events` alone is enough for a generated contract package: it is a
standard-library-only module, and the adapter modules are what pull in a
broker client.

Pin the CLI separately - it is a tool, not a library:

```
go install github.com/craftgodotdev/craftgo/cmd/craftgo@v1.8.0
```

## If a release goes wrong

Before the push, nothing has escaped:

```
git tag -d v1.8.0 pkg/events/v1.8.0 pkg/events/nats/v1.8.0 pkg/events/kafka/v1.8.0
git reset --hard HEAD~1
```

After the push, a published version is permanent - the proxy has it. Cut the
next patch version instead of trying to move a tag.
