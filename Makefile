.DEFAULT_GOAL := help

# ---- vars ----------------------------------------------------------------
BIN_DIR      := bin
BIN          := $(BIN_DIR)/craftgo

GO           ?= go
GOFLAGS      ?=
# Extra `go test` flags for every test target, e.g. TESTFLAGS=-race.
TESTFLAGS    ?=
# Root packages under `make test`; ./tests/e2e is the orchestrator `make e2e` runs.
GO_PKGS      := ./internal/... ./pkg/... ./cmd/...
# The CLI the gen targets run; `make gen` runs bin/craftgo instead.
CRAFTGO      ?= $(GO) run ./cmd/craftgo

# Projects `craftgo gen` regenerates from their design/ folder.
EXAMPLE_PROJECTS := example/todo example/upload example/raw example/ecommerce example/taskflow example/brokers example/grpc
E2E_DIRS     := tests/e2e/matrix

# pkg/events and pkg/wire are their own modules so a generated contract package
# can depend on them without pulling in the rest of craftgo; GO_PKGS does not
# reach them, so vet and golangci run in each.
PUBLISHED    := pkg/events pkg/events/nats pkg/events/kafka pkg/wire

# Every module with its own go.mod besides the root.
SUBMODULES   := $(EXAMPLE_PROJECTS) $(E2E_DIRS) $(PUBLISHED)

# ---- meta ----------------------------------------------------------------
.PHONY: help
help: ## Show this help.
	@awk 'BEGIN {FS = ":.*?## "}; /^[a-zA-Z0-9_%-]+:.*?## / {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# ---- build ---------------------------------------------------------------
.PHONY: build
build: ## Build the craftgo CLI to bin/craftgo.
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -o $(BIN) ./cmd/craftgo

.PHONY: build-all
build-all: ## Compile the root module and every sub-module.
	$(GO) build $(GOFLAGS) ./...
	@for d in $(SUBMODULES); do \
		echo "→ build $$d"; (cd "$$d" && $(GO) build $(GOFLAGS) ./...) || exit 1; \
	done

.PHONY: install
install: ## Install craftgo into $GOBIN (or $GOPATH/bin).
	$(GO) install $(GOFLAGS) ./cmd/craftgo

.PHONY: install-lsp
install-lsp: ## Install craftgo-lsp into $GOBIN. Run after editing internal/lsp or internal/semantic, then restart the language server in VS Code.
	$(GO) install $(GOFLAGS) ./cmd/craftgo-lsp

# ---- docs ---------------------------------------------------------------
.PHONY: docs-install
docs-install: ## Install VitePress dependencies for the docs site.
	cd docs && npm install

.PHONY: docs-dev
docs-dev: ## Run the docs site locally on http://localhost:5173.
	cd docs && npm run dev

.PHONY: docs-build
docs-build: ## Build the docs site to docs/.vitepress/dist for deployment.
	cd docs && npm run build

.PHONY: docs-preview
docs-preview: ## Serve the built docs locally to verify the output.
	cd docs && npm run preview

# ---- test / lint ---------------------------------------------------------
.PHONY: test
test: ## Run all unit tests in the root module.
	$(GO) test $(GOFLAGS) $(TESTFLAGS) -count=1 $(GO_PKGS)

.PHONY: test-race
test-race: ## Run unit tests with the race detector.
	$(GO) test $(GOFLAGS) $(TESTFLAGS) -race -count=1 $(GO_PKGS)

.PHONY: cover
cover: ## Run tests with coverage and write coverage.html.
	$(GO) test $(GOFLAGS) $(TESTFLAGS) -count=1 -coverprofile=coverage.txt $(GO_PKGS)
	$(GO) tool cover -html=coverage.txt -o coverage.html
	@echo "wrote coverage.html"

.PHONY: e2e
e2e: ## Run the e2e orchestrator: gen + `go test` the matrix fixture.
	$(GO) test $(GOFLAGS) $(TESTFLAGS) -count=1 ./tests/e2e/...

.PHONY: test-submodules
test-submodules: ## Run tests inside every sub-module: the examples, the e2e fixture, the published modules.
	@for d in $(SUBMODULES); do \
		echo "→ test $$d"; (cd "$$d" && $(GO) test $(GOFLAGS) $(TESTFLAGS) -count=1 ./...) || exit 1; \
	done

.PHONY: test-all
test-all: test e2e test-submodules ## Run every test suite - root, e2e orchestrator, and each sub-module.

.PHONY: vet
vet: ## go vet over all root packages and every published nested module.
	$(GO) vet ./...
	@for d in $(PUBLISHED); do \
		(cd "$$d" && $(GO) vet ./...) || exit 1; \
	done

.PHONY: fmt
fmt: ## gofmt -w on the entire tree.
	gofmt -w -s .

.PHONY: fmt-check
fmt-check: ## Fail if any Go file isn't gofmt'd.
	@diff=$$(gofmt -l -s .); \
	if [ -n "$$diff" ]; then \
		echo "unformatted files:"; echo "$$diff"; exit 1; \
	fi

.PHONY: lint
lint: vet fmt-check golangci ## vet + fmt-check + golangci-lint.

.PHONY: golangci
golangci: ## golangci-lint (.golangci.yml); skipped when the binary is not installed.
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./... || exit 1; \
		for d in $(PUBLISHED); do \
			(cd "$$d" && golangci-lint run ./...) || exit 1; \
		done; \
	else echo "golangci-lint not installed - skipping"; fi

# ---- codegen + example --------------------------------------------------
.PHONY: gen gen-go gen-e2e
gen: build ## Build bin/craftgo, then regenerate every example with it.
gen-go: ## Regenerate every example through `go run ./cmd/craftgo`.
gen-e2e: ## Regenerate the e2e matrix fixture.

# One loop serves every gen target: craftgo gen -f <project>/design -c <project>.
gen: CRAFTGO = ./$(BIN)
gen gen-go: GEN_PROJECTS = $(EXAMPLE_PROJECTS)
gen-e2e: GEN_PROJECTS = $(E2E_DIRS)
gen gen-go gen-e2e:
	@for d in $(GEN_PROJECTS); do \
		echo "→ gen $$d"; $(CRAFTGO) gen -f "$$d/design" -c "$$d" || exit 1; \
	done

.PHONY: gen-all
gen-all: gen-go gen-e2e ## Regenerate every example and the e2e fixture.

# Extra `go run` arguments for an example's server, keyed by the example's name.
EXAMPLE_ARGS_brokers := -transport memory

# example-% stays off .PHONY: make never applies a pattern rule to a phony target.
example-%: ## Run an example's server: example-todo, example-grpc, ... one per folder in example/.
	cd example/$* && $(GO) run . $(EXAMPLE_ARGS_$*)

.PHONY: gen-diff
gen-diff: gen-all ## Re-gen examples + e2e and fail on any changed or new file under example/ or tests/e2e (drift guard for CI).
	@drift=$$(git status --porcelain -- example tests/e2e); \
	if [ -n "$$drift" ]; then \
		echo "codegen drift detected - run 'make gen-all' and commit the result:"; \
		echo "$$drift"; \
		exit 1; \
	fi

# ---- bench ---------------------------------------------------------------
BENCH_DIR    := bench
BENCH_RAW    := $(BENCH_DIR)/results.txt
BENCH_REPORT := $(BENCH_DIR)/REPORT.md
BENCH_PKG    := ./internal/bench/...
BENCH_RUN    ?= BenchmarkParse
BENCH_TIME   ?= 2s
BENCH_COUNT  ?= 3

.PHONY: bench
bench: ## Run bind-path microbenchmarks; raw output to bench/results.txt.
	@mkdir -p $(BENCH_DIR)
	$(GO) test -run=^$$ -bench=$(BENCH_RUN) -benchmem -benchtime=$(BENCH_TIME) -count=$(BENCH_COUNT) $(BENCH_PKG) | tee $(BENCH_RAW)

.PHONY: bench-report
bench-report: ## Convert bench/results.txt into Markdown at bench/REPORT.md.
	@scripts/bench-report.sh $(BENCH_RAW) $(BENCH_REPORT)

.PHONY: bench-all
bench-all: bench bench-report ## Run benchmarks and regenerate the Markdown report.

# ---- module hygiene ------------------------------------------------------
.PHONY: tidy
tidy: ## go mod tidy in the root module and every sub-module.
	$(GO) mod tidy
	@for d in $(SUBMODULES); do \
		echo "→ tidy $$d"; (cd "$$d" && $(GO) mod tidy) || exit 1; \
	done

.PHONY: tidy-check
tidy-check: ## Fail if go mod tidy would change the go.mod or go.sum of any module.
	$(GO) mod tidy -diff
	@for d in $(SUBMODULES); do \
		echo "→ tidy-check $$d"; (cd "$$d" && $(GO) mod tidy -diff) || exit 1; \
	done

.PHONY: deps
deps: ## Download/verify modules.
	$(GO) mod download
	$(GO) mod verify

.PHONY: clean
clean: ## Remove build artefacts and coverage files.
	rm -rf $(BIN_DIR) dist coverage.txt coverage.html
	@find . -type f \( -name '*.test' -o -name '*.out' -o -name '*.prof' -o -name '*.cov' \) -delete

.PHONY: clean-gen
clean-gen: ## Delete the generated transport, routes, types, events and docs folders of every example and the e2e fixture.
	@for d in $(EXAMPLE_PROJECTS) $(E2E_DIRS); do \
		echo "→ clean $$d"; \
		rm -rf "$$d/internal/transport" "$$d/internal/routes" "$$d/internal/types" "$$d/internal/events" "$$d/docs"; \
	done

# ---- release -------------------------------------------------------------
# Five modules are published from this repo and they share one version: the
# root module, pkg/events, the two adapters, and pkg/wire. Go's convention for
# a nested module is the subdirectory as the tag prefix, so a release is five
# tags - vX.Y.Z, pkg/events/vX.Y.Z, pkg/wire/vX.Y.Z, pkg/events/nats/vX.Y.Z,
# pkg/events/kafka/vX.Y.Z.
#
# `tag` is local-only: it never pushes. It writes the release commit and the
# five tags, then prints the single `git push` for you to run. `tag-sync` is
# the follow-up that needs the tags on origin. See RELEASING.md.
VERSION ?=
DRY_RUN ?=

.PHONY: tag
tag: ## Cut a release locally - VERSION=vX.Y.Z, DRY_RUN=1 to only print the plan. Never pushes.
	@GO="$(GO)" DRY_RUN="$(DRY_RUN)" scripts/release.sh tag "$(VERSION)"

.PHONY: tag-sync
tag-sync: ## After you push the tags: tidy the adapters against the published pkg/events. VERSION=vX.Y.Z.
	@GO="$(GO)" DRY_RUN="$(DRY_RUN)" scripts/release.sh sync "$(VERSION)"

.PHONY: tag-list
tag-list: ## Show the four latest tags of each published module (five modules).
	@scripts/release.sh list

# ---- one-shot CI surface -------------------------------------------------
.PHONY: ci
ci: TESTFLAGS += -race
ci: lint tidy-check test e2e test-submodules gen-diff build ## Run the CI gates locally: lint, module tidiness, the root, e2e and sub-module tests with -race, codegen drift, build.

# ---- docs diagrams --------------------------------------------------------
# Sources are docs/diagrams/*.excalidraw (edit them on excalidraw.com or with
# the VS Code Excalidraw extension); the site embeds docs/public/diagrams/*.svg.
# The export runs the real Excalidraw in a headless browser:
#   npm i -g excalidraw-brute-export-cli && npx playwright install firefox
.PHONY: docs-diagrams
docs-diagrams: ## Re-export every docs/diagrams/*.excalidraw to docs/public/diagrams/*.svg.
	@for f in docs/diagrams/*.excalidraw; do \
		out=docs/public/diagrams/$$(basename $${f%.excalidraw}).svg; \
		echo "→ $$out"; \
		npx excalidraw-brute-export-cli -i "$$f" -o "$$out" -f svg -s 1 -b true -d false -e false --quiet || exit 1; \
	done
