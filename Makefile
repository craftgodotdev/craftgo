.DEFAULT_GOAL := help

# ---- vars ----------------------------------------------------------------
MODULE       := github.com/craftgodotdev/craftgo
BIN_DIR      := bin
BIN          := $(BIN_DIR)/craftgo
EXAMPLE_DIR  := example
EXAMPLE_PROJECTS := example/todo example/upload example/raw example/ecommerce

GO           ?= go
GOFLAGS      ?=
GO_PKGS      := ./internal/... ./pkg/... ./cmd/...

# Sub-modules that have their own go.mod (each gets `tidy`/`build` per target).
SUBMODULES   := $(EXAMPLE_PROJECTS) tests/e2e/matrix

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
e2e: ## Run the e2e orchestrator: gen + `go test` the matrix fixture.
	$(GO) test $(GOFLAGS) -count=1 ./tests/e2e/...

.PHONY: test-submodules
test-submodules: ## Run tests inside every sub-module (example/, e2e fixtures).
	@for d in $(SUBMODULES); do \
		echo "→ test $$d"; (cd "$$d" && $(GO) test $(GOFLAGS) -count=1 ./...) || exit 1; \
	done

.PHONY: test-all
test-all: test e2e test-submodules ## Run every test suite - root, e2e orchestrator, and each sub-module.

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
# The single consolidated e2e fixture (matrix). Its design exercises every DSL
# construct and boots a server for the http-roundtrip tests.
E2E_DIRS := tests/e2e/matrix

.PHONY: gen
gen: build ## Regenerate every example mini-project (todo, upload, raw, ecommerce).
	@for d in $(EXAMPLE_PROJECTS); do \
		echo "→ gen $$d"; ./$(BIN) gen -f "$$d/design" -c "$$d" || exit 1; \
	done

.PHONY: gen-go
gen-go: ## Regenerate every example mini-project without rebuilding the CLI.
	@for d in $(EXAMPLE_PROJECTS); do \
		echo "→ gen $$d"; $(GO) run ./cmd/craftgo gen -f "$$d/design" -c "$$d" || exit 1; \
	done

.PHONY: gen-e2e
gen-e2e: ## Regenerate the e2e matrix fixture from its design dir.
	@for d in $(E2E_DIRS); do \
		echo "→ gen $$d"; $(GO) run ./cmd/craftgo gen "$$d/design" || exit 1; \
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

.PHONY: gen-diff
gen-diff: gen-all ## Re-gen examples + e2e and fail if anything changed (drift guard for CI).
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
	echo "done - VS Code: Cmd+Shift+P → Developer: Reload Window"

$(EXT_SRC)/node_modules: $(EXT_SRC)/package.json
	cd $(EXT_SRC) && npm install
	@touch $(EXT_SRC)/node_modules

.PHONY: vscode-deps
vscode-deps: $(EXT_SRC)/node_modules ## Install npm dependencies for the VS Code extension.

.PHONY: vscode-build
vscode-build: $(EXT_SRC)/node_modules ## Build the VS Code extension bundle (out/extension.js).
	cd $(EXT_SRC) && npm run build

.PHONY: vscode-package
vscode-package: $(EXT_SRC)/node_modules ## Build a .vsix for the VS Code extension (vsce package).
	cd $(EXT_SRC) && npm run package

.PHONY: vscode-install
vscode-install: vscode-package ## Build .vsix and install it locally via `code --install-extension`.
	@vsix=$$(ls -t $(EXT_SRC)/*.vsix | head -1); \
	if [ -z "$$vsix" ]; then echo "no .vsix produced"; exit 1; fi; \
	echo "installing $$vsix"; \
	code --install-extension "$$vsix" --force

.PHONY: vscode-link
vscode-link: vscode-deps vscode-build ## Symlink extensions/vscode into ~/.vscode/extensions for live dev (reload window after).
	@target="$(EXT_INSTALL)/craftgo.craftgo-dev"; \
	mkdir -p $(EXT_INSTALL); \
	rm -rf "$$target"; \
	ln -s "$(CURDIR)/$(EXT_SRC)" "$$target"; \
	echo "linked $(CURDIR)/$(EXT_SRC) → $$target"; \
	echo "VS Code: Cmd+Shift+P → Developer: Reload Window"

.PHONY: vscode-uninstall
vscode-uninstall: ## Remove any locally-installed craftgo VS Code extension (vsix or symlink).
	@for d in $(EXT_INSTALL)/*craftgo*; do \
		[ -e "$$d" ] || continue; \
		echo "removing $$d"; rm -rf "$$d"; \
	done

.PHONY: clean
clean: ## Remove build artefacts and coverage files.
	rm -rf $(BIN_DIR) dist coverage.txt coverage.html $(BENCH_DIR)
	@find . -type f \( -name '*.test' -o -name '*.out' -o -name '*.prof' -o -name '*.cov' \) -delete

.PHONY: clean-gen
clean-gen: ## Remove regenerable artefacts under every example mini-project + e2e fixture (transport, routes, types, docs).
	@for d in $(EXAMPLE_PROJECTS) $(E2E_DIRS); do \
		echo "→ clean $$d"; \
		rm -rf "$$d/internal/transport" "$$d/internal/routes" "$$d/internal/types" "$$d/docs"; \
	done

# ---- one-shot CI surface -------------------------------------------------
.PHONY: ci
ci: lint test-all build ## What CI runs: lint, every test suite (root + e2e + submodules), build.
