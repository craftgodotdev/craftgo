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
SUBMODULES   := $(EXAMPLE_DIR) testdata/e2e/users testdata/e2e/complex testdata/e2e/multi-service

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
e2e: ## Run the cross-module e2e orchestrator (testdata/e2e/...).
	$(GO) test $(GOFLAGS) -count=1 ./tests/...

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
.PHONY: gen
gen: build ## Regenerate the example project from its design dir.
	./$(BIN) gen $(DESIGN_DIR)

.PHONY: gen-go
gen-go: ## Regenerate the example without rebuilding the CLI (uses `go run`).
	$(GO) run ./cmd/craftgo gen $(DESIGN_DIR)

.PHONY: example
example: ## Run the example server (./example/main.go) on :8080.
	cd $(EXAMPLE_DIR) && $(GO) run .

.PHONY: gen-diff
gen-diff: gen-go ## Re-gen and fail if anything changed (drift guard for CI).
	@if ! git diff --quiet -- $(EXAMPLE_DIR); then \
		echo "codegen drift detected in $(EXAMPLE_DIR):"; \
		git --no-pager diff --stat -- $(EXAMPLE_DIR); \
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
.PHONY: clean
clean: ## Remove build artefacts and coverage files.
	rm -rf $(BIN_DIR) dist coverage.txt coverage.html $(BENCH_DIR)
	@find . -type f \( -name '*.test' -o -name '*.out' -o -name '*.prof' -o -name '*.cov' \) -delete

.PHONY: clean-gen
clean-gen: ## Remove regenerable artefacts under example/ (handlers, routes, types, docs).
	rm -rf $(EXAMPLE_DIR)/internal/handler $(EXAMPLE_DIR)/internal/routes $(EXAMPLE_DIR)/internal/types $(EXAMPLE_DIR)/docs

# ---- one-shot CI surface -------------------------------------------------
.PHONY: ci
ci: lint test build ## What CI runs: lint, test, build.
