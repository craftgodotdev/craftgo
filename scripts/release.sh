#!/usr/bin/env bash
# release.sh - cut a release of every published module at one version.
#
#   tag <version>    write the release commit + the five tags, print the push
#   sync <version>   after the push: tidy the adapters, commit the checksums
#   list             the four latest tags of each published module
#
# Nothing here ever pushes. `tag` leaves a commit and five local tags and
# prints the exact `git push` for a human to run; `sync` is the follow-up that
# needs those tags to be on origin. RELEASING.md has the two phases and why
# the adapters cannot be tidied before the pkg/events tag is published.
#
# DRY_RUN=1 prints every git/go command that would write instead of running
# it, and downgrades the clean-tree / free-tag refusals to warnings, so the
# plan can be inspected from a work-in-progress tree with no network.

set -euo pipefail

GO="${GO:-go}"
DRY_RUN="${DRY_RUN:-}"

# Published modules, in the order a release touches them:
#   <directory>|<module path>|<tag prefix>
# Go's convention for a nested module is its subdirectory as the tag prefix,
# so pkg/events releases as `pkg/events/v1.8.0`. The root module's tags carry
# no prefix, which is also what keeps them the only ones GoReleaser sees -
# .github/workflows/release.yml filters tags on `v[0-9]+...`, and a filter
# pattern's `*` never crosses a `/`.
#
# The two dependency-free runtime modules come first: pkg/events, which the
# adapters require, and pkg/wire, which a generated contract package requires.
# Both are tagged in the same phase-1 push as the adapters, so by the time a
# consumer resolves an adapter every module it can reach is already published.
MODULES=(
	".|github.com/craftgodotdev/craftgo|"
	"pkg/events|github.com/craftgodotdev/craftgo/pkg/events|pkg/events/"
	"pkg/wire|github.com/craftgodotdev/craftgo/pkg/wire|pkg/wire/"
	"pkg/events/nats|github.com/craftgodotdev/craftgo/pkg/events/nats|pkg/events/nats/"
	"pkg/events/kafka|github.com/craftgodotdev/craftgo/pkg/events/kafka|pkg/events/kafka/"
)

# The adapters that `replace` pkg/events with a relative path and must pin a
# real version instead: a consumer of an adapter ignores that replace.
ADAPTERS=(pkg/events/nats pkg/events/kafka)
EVENTS_MODULE="github.com/craftgodotdev/craftgo/pkg/events"

# Keep a Changelog, dated in the maintainer's timezone: `tag` turns the
# Unreleased section into `## [X.Y.Z] - YYYY-MM-DD [UTC+7]`.
CHANGELOG="CHANGELOG.md"

# Where the version is written down in the tree. Bare (no leading v), to match
# the -ldflags GoReleaser injects. The two Go vars are what a binary reports;
# the docs constant is what the site's nav shows. <file>|<declaration>
VERSION_VARS=(
	"cmd/craftgo/main.go|var version"
	"internal/lsp/server.go|var Version"
	"docs/.vitepress/config.ts|const VERSION"
)

# ---- output --------------------------------------------------------------
die()     { printf 'release: %s\n' "$*" >&2; exit 1; }
warn()    { printf 'release: warning: %s\n' "$*" >&2; }
note()    { printf '  %s\n' "$*"; }
section() { printf '\n== %s\n' "$*"; }

# Anything set and not obviously falsy counts as a dry run: a typo in
# DRY_RUN should stop a release, never quietly write one.
dry() {
	case "$DRY_RUN" in
		"" | 0 | false | no | off) return 1 ;;
		*) return 0 ;;
	esac
}

# shq <word...> - re-quote so the printed line is copy-pasteable.
shq() {
	local out="" arg
	for arg in "$@"; do
		case "$arg" in
			*[!A-Za-z0-9=/@:._+-]*) out="$out '${arg//\'/\'\\\'\'}'" ;;
			*) out="$out $arg" ;;
		esac
	done
	printf '%s' "${out# }"
}

# run <cmd...> - a command that writes. Always printed; executed only outside
# a dry run. Read-only commands are called directly, never through this.
run() {
	printf '  + %s\n' "$(shq "$@")"
	dry || "$@"
}

# run_in <dir> <cmd...> - the same, in a subdirectory.
run_in() {
	local dir="$1"; shift
	printf '  + (cd %s && %s)\n' "$dir" "$(shq "$@")"
	dry || ( cd "$dir" && "$@" ) || die "failed in $dir: $(shq "$@")"
}

# refuse <message> - a hard stop for a real run, a warning for a dry run:
# a dry run writes nothing, so it can still show the plan.
refuse() {
	if dry; then
		warn "$* (a real run would refuse)"
	else
		die "$*"
	fi
}

# ---- shared checks -------------------------------------------------------
# require_version <version> <target name>
require_version() {
	local v="${1:-}" target="${2:-tag}"
	[ -n "$v" ] || die "VERSION is required: make $target VERSION=v1.8.0"
	[[ "$v" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] \
		|| die "VERSION must match ^v[0-9]+\\.[0-9]+\\.[0-9]+\$ (got '$v')"
}

require_clean_tree() {
	[ -z "$(git status --porcelain)" ] \
		|| refuse "working tree is not clean - commit or stash first"
}

cd_repo_root() {
	local root
	root="$(git rev-parse --show-toplevel 2>/dev/null)" || die "not a git repository"
	cd "$root"
}

module_of() { local e="${1#*|}"; printf '%s' "${e%|*}"; }
prefix_of() { printf '%s' "${1##*|}"; }

tag_names() {
	local version="$1" entry
	for entry in "${MODULES[@]}"; do
		printf '%s%s\n' "$(prefix_of "$entry")" "$version"
	done
}

# ---- tag -----------------------------------------------------------------
cmd_tag() {
	local version="${1:-}"
	require_version "$version" tag
	local bare="${version#v}"
	local branch tag entry dir sha unreleased
	local -a paths=()

	cd_repo_root
	branch="$(git rev-parse --abbrev-ref HEAD)"

	section "release $version on $branch at $(git rev-parse --short HEAD)"
	if dry; then note "DRY RUN - nothing below is executed or written"; fi
	note "no network is used and nothing is pushed; the push is printed at the end"

	section "preflight"
	# A detached HEAD has no branch to name in the push command, and a release
	# commit that no branch points at is a commit waiting to be garbage
	# collected. This one is a hard stop even in a dry run.
	[ "$branch" != "HEAD" ] \
		|| die "HEAD is detached - check out the branch you are releasing from"
	require_clean_tree
	for tag in $(tag_names "$version"); do
		if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
			refuse "tag $tag already exists"
		else
			note "tag $tag is free"
		fi
	done
	# The changelog is rolled below, so it has to be rollable: a section
	# for this version already exists only if a release stopped halfway,
	# and an empty Unreleased means a release with nothing to show.
	if grep -q "^## \[$bare\]" "$CHANGELOG"; then
		refuse "$CHANGELOG already has a '## [$bare]' section"
	fi
	unreleased="$(sed -n '/^## \[Unreleased\]/,/^## \[[0-9]/p' "$CHANGELOG")"
	if ! grep -q '^- ' <<<"$unreleased"; then
		refuse "$CHANGELOG lists nothing under '## [Unreleased]'"
	fi
	note "origin is not consulted (no network here). If an earlier release"
	note "stopped halfway, 'git fetch --tags' and look before tagging again."

	section "pin $EVENTS_MODULE@$version in the adapters"
	note "a consumer of an adapter ignores its 'replace', so the published"
	note "go.mod has to require a real pkg/events version"
	for dir in "${ADAPTERS[@]}"; do
		run_in "$dir" "$GO" mod edit \
			-dropreplace="$EVENTS_MODULE" \
			-require="$EVENTS_MODULE@$version"
	done
	note "'go mod tidy' is deliberately NOT run: $version is not on origin yet."
	note "go.work resolves pkg/events locally meanwhile; the checksums land in"
	note "'make tag-sync VERSION=$version' once you have pushed."

	section "bump the reported version to $bare"
	for entry in "${VERSION_VARS[@]}"; do
		bump_version_var "${entry%%|*}" "${entry##*|}" "$bare"
	done

	section "roll the changelog"
	roll_changelog "$bare"

	section "commit"
	for dir in "${ADAPTERS[@]}"; do paths+=("$dir/go.mod"); done
	for entry in "${VERSION_VARS[@]}"; do paths+=("${entry%%|*}"); done
	paths+=("$CHANGELOG")
	run git add -- "${paths[@]}"
	run git commit -m "release: $version"

	section "tag"
	for entry in "${MODULES[@]}"; do
		run git tag -a "$(prefix_of "$entry")$version" \
			-m "$(module_of "$entry") $version"
	done

	# Two pushes, not one: GitHub raises no push event when a single push
	# carries more than three tags, and .github/workflows/release.yml only
	# runs on that event, so the root tag travels alone.
	section "push - run this yourself, in this order"
	if dry; then sha="<release-commit>"; else sha="$(git rev-parse HEAD)"; fi
	printf '\n  git push origin %s:refs/heads/%s' "$sha" "$branch"
	for tag in $(tag_names "$version"); do
		if [ "$tag" != "$version" ]; then printf ' \\\n      %s' "$tag"; fi
	done
	printf '\n\n  git push origin %s\n\n' "$version"
	note "the unprefixed tag $version is the only one GoReleaser reacts to,"
	note "and it is alone in the second push so that push event fires;"
	note "the four nested tags just publish module versions."
	note "check it ran: gh run list --workflow=release.yml --limit 1"
	note "then: make tag-sync VERSION=$version"
	if dry; then printf '\n  (dry run: nothing was written)\n'; fi
}

# bump_version_var <file> <declaration> <bare version>
# Rewrites `<declaration> = "X.Y.Z"`. Done with a temp file rather than
# `sed -i`, whose flags differ between GNU and BSD.
bump_version_var() {
	local file="$1" decl="$2" bare="$3" current tmp
	current="$(sed -nE "s/^${decl} = \"([0-9]+\.[0-9]+\.[0-9]+)\".*/\1/p" "$file" | head -1)"
	[ -n "$current" ] || die "no '$decl = \"X.Y.Z\"' declaration in $file"
	if [ "$current" = "$bare" ]; then
		note "$file: $decl is already $bare"
		return 0
	fi
	note "$file: $decl $current -> $bare"
	dry && return 0
	tmp="$(mktemp)"
	sed -E "s/^${decl} = \"[0-9]+\.[0-9]+\.[0-9]+\"/${decl} = \"${bare}\"/" "$file" >"$tmp"
	mv "$tmp" "$file"
	grep -q "^${decl} = \"${bare}\"" "$file" || die "failed to bump $decl in $file"
}

# roll_changelog <bare version>
# Opens the version's section under an emptied Unreleased, dated today where
# every existing heading is dated. sed takes the two new lines as literal
# newlines escaped with a backslash; `\n` in a replacement is GNU-only.
roll_changelog() {
	local bare="$1" today tmp
	today="$(TZ=Asia/Ho_Chi_Minh date +%F)"
	note "$CHANGELOG: the Unreleased entries become ## [$bare] - $today [UTC+7]"
	dry && return 0
	tmp="$(mktemp)"
	sed "s|^## \[Unreleased\]$|## [Unreleased]\\
\\
## [$bare] - $today [UTC+7]|" "$CHANGELOG" >"$tmp"
	mv "$tmp" "$CHANGELOG"
	grep -q "^## \[$bare\] - $today \[UTC+7\]$" "$CHANGELOG" \
		|| die "failed to roll $CHANGELOG"
}

# ---- sync ----------------------------------------------------------------
cmd_sync() {
	local version="${1:-}"
	require_version "$version" tag-sync
	local dir tag
	local -a paths=()

	cd_repo_root
	section "sync adapter checksums for $version"
	if dry; then note "DRY RUN - nothing below is executed or written"; fi
	require_clean_tree

	for dir in "${ADAPTERS[@]}"; do
		tag="$dir/$version"
		git rev-parse -q --verify "refs/tags/$tag" >/dev/null \
			|| refuse "tag $tag does not exist - run 'make tag VERSION=$version' first"
	done
	note "the tags must also be on origin, or the tidy below cannot resolve them"

	section "tidy"
	note "GOPROXY=direct so the just-pushed tag resolves without waiting for"
	note "the module proxy; GOFLAGS=-mod=mod because tidy has to write go.sum"
	for dir in "${ADAPTERS[@]}"; do
		printf '  + (cd %s && GOFLAGS=-mod=mod GOPROXY=direct %s mod tidy)\n' "$dir" "$GO"
		dry || ( cd "$dir" && GOFLAGS=-mod=mod GOPROXY=direct "$GO" mod tidy ) || die \
			"go mod tidy failed in $dir - is $EVENTS_MODULE $version pushed?"
	done

	section "commit the checksums"
	for dir in "${ADAPTERS[@]}"; do paths+=("$dir/go.mod" "$dir/go.sum"); done
	if dry; then
		note "only if go.sum actually changed:"
		printf '  + %s\n' "$(shq git add -- "${paths[@]}")"
		printf '  + %s\n' "$(shq git commit -m "release: $version checksums")"
		printf '\n  (dry run: nothing was written)\n'
		return 0
	fi
	if [ -z "$(git status --porcelain -- "${ADAPTERS[@]}")" ]; then
		note "go.sum already matches $version - nothing to commit"
		return 0
	fi
	run git add -- "${paths[@]}"
	run git commit -m "release: $version checksums"
	printf '\n  git push origin HEAD\n\n'
	note "no tag moves: the published versions already point at the release"
	note "commit, and a consumer computes its own checksums from the tag."
}

# ---- list ----------------------------------------------------------------
cmd_list() {
	cd_repo_root
	local entry prefix tags
	for entry in "${MODULES[@]}"; do
		prefix="$(prefix_of "$entry")"
		# `pkg/events/v*` cannot match `pkg/events/nats/v1.0.0`: the element
		# after the prefix would have to start with `v`, and `nats` does not.
		# `pkg/wire/v*` has no nested module under it at all.
		tags="$(git tag --list "${prefix}v*" --sort=-v:refname | head -4 | tr '\n' ' ')"
		printf '%-50s %s\n' "$(module_of "$entry")" "${tags:-(none yet)}"
	done
}

# ---- dispatch ------------------------------------------------------------
case "${1:-}" in
	tag)  shift; cmd_tag  "${1:-}" ;;
	sync) shift; cmd_sync "${1:-}" ;;
	list) shift; cmd_list ;;
	*) die "usage: scripts/release.sh {tag|sync} vX.Y.Z | scripts/release.sh list" ;;
esac
