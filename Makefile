.DEFAULT_GOAL := help

# ---- vars ----------------------------------------------------------------
BIN_DIR      := bin
BIN          := $(BIN_DIR)/craftgo
EXAMPLE_PROJECTS := example/todo example/upload example/raw example/ecommerce example/taskflow example/brokers example/grpc

GO           ?= go
GOFLAGS      ?=
# Extra `go test` flags for every test target, e.g. TESTFLAGS=-race.
TESTFLAGS    ?=
# Root packages under `make test`; ./tests/e2e is the orchestrator `make e2e` runs.
GO_PKGS      := ./internal/... ./pkg/... ./cmd/...

# Sub-modules that have their own go.mod (each gets `tidy`/`build` per target).
# pkg/events and pkg/wire are their own modules so a generated contract package
# can depend on them without pulling in the rest of craftgo; they are therefore
# not covered by GO_PKGS and are tested, vetted and linted here instead.
SUBMODULES   := $(EXAMPLE_PROJECTS) tests/e2e/matrix pkg/events pkg/events/nats pkg/events/kafka pkg/wire

# ---- meta ----------------------------------------------------------------
.PHONY: help
help: ## Show this help.
	@awk 'BEGIN {FS = ":.*?## "}; /^[a-zA-Z0-9_-]+:.*?## / {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

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
install: ## Install craftgo into $$GOBIN (or $$GOPATH/bin).
	$(GO) install $(GOFLAGS) ./cmd/craftgo

.PHONY: install-lsp
install-lsp: ## Install craftgo-lsp into $$GOBIN. Run after editing internal/lsp or internal/semantic, then restart the language server in VS Code.
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
test-submodules: ## Run tests inside every sub-module (example/, e2e fixtures).
	@for d in $(SUBMODULES); do \
		echo "→ test $$d"; (cd "$$d" && $(GO) test $(GOFLAGS) $(TESTFLAGS) -count=1 ./...) || exit 1; \
	done

.PHONY: test-all
test-all: test e2e test-submodules ## Run every test suite - root, e2e orchestrator, and each sub-module.

.PHONY: vet
vet: ## go vet over all root packages and every published nested module.
	$(GO) vet ./...
	@(cd pkg/events && $(GO) vet ./...)
	@(cd pkg/events/nats && $(GO) vet ./...)
	@(cd pkg/events/kafka && $(GO) vet ./...)
	@(cd pkg/wire && $(GO) vet ./...)

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
		golangci-lint run $(GO_PKGS) || exit 1; \
		(cd pkg/events && golangci-lint run ./...) || exit 1; \
		(cd pkg/events/nats && golangci-lint run ./...) || exit 1; \
		(cd pkg/events/kafka && golangci-lint run ./...) || exit 1; \
		(cd pkg/wire && golangci-lint run ./...) || exit 1; \
	else echo "golangci-lint not installed - skipping"; fi

# ---- codegen + example --------------------------------------------------
# The single consolidated e2e fixture (matrix). Its design exercises every DSL
# construct and boots a server for the http-roundtrip tests.
E2E_DIRS := tests/e2e/matrix

.PHONY: gen
gen: build ## Regenerate every example mini-project (todo, upload, raw, ecommerce, taskflow, brokers).
	@for d in $(EXAMPLE_PROJECTS); do \
		echo "→ gen $$d"; ./$(BIN) gen -f "$$d/design" -c "$$d" || exit 1; \
	done

.PHONY: gen-go
gen-go: ## Regenerate every example mini-project without rebuilding the CLI.
	@for d in $(EXAMPLE_PROJECTS); do \
		echo "→ gen $$d"; $(GO) run ./cmd/craftgo gen -f "$$d/design" -c "$$d" || exit 1; \
	done

.PHONY: gen-e2e
gen-e2e: ## Regenerate every manifest in the e2e fixtures.
	@for d in $(E2E_DIRS); do \
		for m in $$(find "$$d" -name craftgo.design.yaml | sort); do \
			mdir=$$(dirname "$$m"); \
			echo "→ gen $$mdir"; $(GO) run ./cmd/craftgo gen -f "$$mdir" -c "$$(dirname "$$mdir")" || exit 1; \
		done; \
	done

.PHONY: gen-all
gen-all: gen-go gen-e2e ## Regenerate the example mini-projects AND every e2e fixture.

.PHONY: example-todo
example-todo: ## Run the todo example server.
	cd example/todo && $(GO) run .

.PHONY: example-upload
example-upload: ## Run the upload example server.
	cd example/upload && $(GO) run .

.PHONY: example-raw
example-raw: ## Run the raw passthrough example server.
	cd example/raw && $(GO) run .

.PHONY: example-ecommerce
example-ecommerce: ## Run the ecommerce showcase server.
	cd example/ecommerce && $(GO) run .

.PHONY: example-taskflow
example-taskflow: ## Run the taskflow reference application.
	cd example/taskflow && $(GO) run .

.PHONY: example-brokers
example-brokers: ## Run the brokers event example over the in-process transport.
	cd example/brokers && $(GO) run . -transport memory

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
bench: ## Run bind-path microbenchmarks; raw output to $(BENCH_RAW).
	@mkdir -p $(BENCH_DIR)
	$(GO) test -run=^$$ -bench=$(BENCH_RUN) -benchmem -benchtime=$(BENCH_TIME) -count=$(BENCH_COUNT) $(BENCH_PKG) | tee $(BENCH_RAW)

.PHONY: bench-report
bench-report: ## Convert $(BENCH_RAW) into Markdown at $(BENCH_REPORT).
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

.PHONY: deps
deps: ## Download/verify modules.
	$(GO) mod download
	$(GO) mod verify

.PHONY: clean
clean: ## Remove build artefacts and coverage files.
	rm -rf $(BIN_DIR) dist coverage.txt coverage.html $(BENCH_DIR)
	@find . -type f \( -name '*.test' -o -name '*.out' -o -name '*.prof' -o -name '*.cov' \) -delete

.PHONY: clean-gen
clean-gen: ## Remove regenerable artefacts under every example mini-project + e2e fixture (transport, routes, types, docs).
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
ci: lint test-all build ## What CI runs: lint, every test suite (root + e2e + submodules), build.

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
