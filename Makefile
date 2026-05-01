.DEFAULT_GOAL := help

# ---- vars ----------------------------------------------------------------
MODULE       := github.com/dropship-dev/craftgo
BIN_DIR      := bin
BIN          := $(BIN_DIR)/craftgo
EXAMPLE_DIR  := example
DESIGN_DIR   := $(EXAMPLE_DIR)/design

GO           ?= go
GOFLAGS      ?=
GO_PKGS      := ./internal/... ./pkg/... ./cmd/...

# Sub-modules that have their own go.mod (each gets `tidy`/`build` per target).
SUBMODULES   := $(EXAMPLE_DIR) tests/e2e/users tests/e2e/complex tests/e2e/multi-service

# ---- meta ----------------------------------------------------------------
.PHONY: help
help: ## Show this help.
	@awk 'BEGIN {FS = ":.*?## "}; /^[a-zA-Z0-9_-]+:.*?## / {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# ---- build ---------------------------------------------------------------
.PHONY: build
build: ## Build the craftgo CLI to bin/craftgo.
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -o $(BIN) ./cmd/craftgo

.PHONY: install
install: ## Install craftgo into $$GOBIN (or $$GOPATH/bin).
	$(GO) install $(GOFLAGS) ./cmd/craftgo

# ---- test / lint ---------------------------------------------------------
.PHONY: test
test: ## Run all unit tests in the root module.
	$(GO) test $(GOFLAGS) -count=1 $(GO_PKGS)

.PHONY: test-race
test-race: ## Run unit tests with the race detector.
	$(GO) test $(GOFLAGS) -race -count=1 $(GO_PKGS)

.PHONY: cover
cover: ## Run tests with coverage and write coverage.html.
	$(GO) test $(GOFLAGS) -count=1 -coverprofile=coverage.txt $(GO_PKGS)
	$(GO) tool cover -html=coverage.txt -o coverage.html
	@echo "wrote coverage.html"

.PHONY: e2e
e2e: ## Run the cross-module e2e orchestrator (tests/e2e/...).
	$(GO) test $(GOFLAGS) -count=1 ./tests/e2e/...

.PHONY: test-submodules
test-submodules: ## Run tests inside every sub-module (example/, e2e fixtures).
	@for d in $(SUBMODULES); do \
		echo "→ test $$d"; (cd "$$d" && $(GO) test $(GOFLAGS) -count=1 ./...) || exit 1; \
	done

.PHONY: test-all
test-all: test e2e test-submodules ## Run every test suite — root, e2e orchestrator, and each sub-module.

.PHONY: vet
vet: ## go vet over all root packages.
	$(GO) vet $(GO_PKGS)

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
lint: vet fmt-check ## vet + fmt-check (cheap CI-style lint).

# ---- codegen + example --------------------------------------------------
E2E_DIRS := tests/e2e/users tests/e2e/complex tests/e2e/multi-service

.PHONY: gen
gen: build ## Regenerate the example project from its design dir.
	./$(BIN) gen $(DESIGN_DIR)

.PHONY: gen-go
gen-go: ## Regenerate the example without rebuilding the CLI (uses `go run`).
	$(GO) run ./cmd/craftgo gen $(DESIGN_DIR)

.PHONY: gen-e2e
gen-e2e: ## Regenerate every tests/e2e/* fixture from its design dir.
	@for d in $(E2E_DIRS); do \
		echo "→ gen $$d"; $(GO) run ./cmd/craftgo gen "$$d/design" || exit 1; \
	done

.PHONY: gen-all
gen-all: gen-go gen-e2e ## Regenerate the example AND every e2e fixture.

.PHONY: example
example: ## Run the example server (./example/main.go) on :8080.
	cd $(EXAMPLE_DIR) && $(GO) run .

.PHONY: gen-diff
gen-diff: gen-all ## Re-gen example + e2e and fail if anything changed (drift guard for CI).
	@if ! git diff --quiet -- $(EXAMPLE_DIR) $(E2E_DIRS); then \
		echo "codegen drift detected:"; \
		git --no-pager diff --stat -- $(EXAMPLE_DIR) $(E2E_DIRS); \
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

# ---- clean ---------------------------------------------------------------
# ---- VS Code extension ---------------------------------------------------
# Glob both legacy (`harry2401.craftgo-*`) and current (`craftgo.craftgo-*`)
# folder shapes so the target works regardless of which `.vsix` the user
# installed. The extension folder under ~/.vscode survives publisher
# renames at the source level — files on disk only get rewritten when
# the user reinstalls a fresh `.vsix`.
EXT_SRC      := extensions/vscode
EXT_INSTALL  := $$HOME/.vscode/extensions

.PHONY: sync-vscode
sync-vscode: ## Copy syntax / package files into the locally-installed craftgo extension folder so grammar edits show up after a Reload Window.
	@found=$$(ls -d $(EXT_INSTALL)/*craftgo* 2>/dev/null | head -1); \
	if [ -z "$$found" ]; then \
		echo "no installed craftgo extension found under $(EXT_INSTALL)"; \
		exit 1; \
	fi; \
	echo "syncing $(EXT_SRC)/ → $$found"; \
	cp -R $(EXT_SRC)/syntaxes $$found/; \
	cp $(EXT_SRC)/language-configuration.json $$found/ 2>/dev/null || true; \
	cp $(EXT_SRC)/package.json $$found/; \
	echo "done — VS Code: Cmd+Shift+P → Developer: Reload Window"

.PHONY: clean
clean: ## Remove build artefacts and coverage files.
	rm -rf $(BIN_DIR) dist coverage.txt coverage.html $(BENCH_DIR)
	@find . -type f \( -name '*.test' -o -name '*.out' -o -name '*.prof' -o -name '*.cov' \) -delete

.PHONY: clean-gen
clean-gen: ## Remove regenerable artefacts under example/ and every e2e fixture (handler, routes, types, docs).
	@for d in $(EXAMPLE_DIR) $(E2E_DIRS); do \
		echo "→ clean $$d"; \
		rm -rf "$$d/internal/handler" "$$d/internal/routes" "$$d/internal/types" "$$d/docs"; \
	done

# ---- one-shot CI surface -------------------------------------------------
.PHONY: ci
ci: lint test-all build ## What CI runs: lint, every test suite (root + e2e + submodules), build.
