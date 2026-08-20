OS = $(shell uname | tr A-Z a-z)
ARCH ?= $(shell uname -m | sed -e 's/x86_64/amd64/' | sed -e 's/aarch64/arm64/')
DEBUG ?= false
export PATH := $(abspath bin/):${PATH}

# Build variables
export CGO_ENABLED ?= 0
ifeq (${VERBOSE}, 1)
ifeq ($(filter -v,${GOARGS}),)
	GOARGS += -v
endif
TEST_FORMAT = short-verbose
endif

GLAB_VERSION ?= $(shell git describe --tags --abbrev=0 2>/dev/null || git rev-parse --short HEAD)
BUILD_COMMIT_SHA ?= $(shell git rev-parse --short HEAD)

ifndef CGO_CPPFLAGS
    export CGO_CPPFLAGS := $(CPPFLAGS)
endif
ifndef CGO_CFLAGS
    export CGO_CFLAGS := $(CFLAGS)
endif
ifndef CGO_LDFLAGS
    export CGO_LDFLAGS := $(LDFLAGS)
endif

HASGOTESTSUM := $(shell which gotestsum 2> /dev/null)
HASGOCILINT := $(shell which golangci-lint 2> /dev/null)

ifdef HASGOTESTSUM
    GOTEST=gotestsum
else
    GOTEST=bin/gotestsum
endif

ifdef HASGOCILINT
    GOLINT=golangci-lint
else
    GOLINT=bin/golangci-lint
endif

GO_LDFLAGS := -X main.commit=$(BUILD_COMMIT_SHA) $(GO_LDFLAGS)
GO_LDFLAGS := $(GO_LDFLAGS) -X main.version=$(GLAB_VERSION)
GOURL ?= gitlab.com/gitlab-org/cli
BUILDLOC ?= ./bin/glab

# Dependency versions
GOTESTSUM_VERSION = 1.13.0
GOLANGCI_LINT_VERSION = 2.12.2

# Add the ability to override some variables
# Use with care
-include override.mk

.PHONY: build
.DEFAULT_GOAL := build
build:
	go build -trimpath -ldflags "$(GO_LDFLAGS) -X main.debugMode=$(DEBUG) -w -s" -o $(BUILDLOC) $(GOURL)/cmd/glab

clean: ## Clear the working area and the project
	rm -rf ./bin ./.glab-cli ./test/testdata-* ./coverage.txt coverage-*
.PHONY: clean

.PHONY: install
install: ## Install glab in $GOPATH/bin
	GO111MODULE=on go install -trimpath -ldflags "$(GO_LDFLAGS) -X main.debugMode=$(DEBUG) -w -s" $(GOURL)/cmd/glab

.PHONY: run
run:
	go run -trimpath -ldflags "$(GO_LDFLAGS) -X main.debugMode=$(DEBUG)" ./cmd/glab $(run)

.PHONY: rt
rt: ## Test release without publishing
	goreleaser release --snapshot --clean

.PHONY: rtdebug
rtdebug: ## Test release with debug info
	goreleaser release --snapshot --clean --verbose

.PHONY: release
release:
	goreleaser $(run)

.PHONY: manpage
manpage: ## Generate manual pages
	go run ./cmd/gen-docs/docs.go --manpage --path ./share/man/man1

.PHONY: gen-docs
gen-docs: ## Generate web docs and the GitLab Docs navigation submenu for glab
	go run ./cmd/gen-docs/docs.go

.PHONY: check
check: test lint check-embed ## Run tests and linters

.PHONY: check-embed
# grep --include is GNU-only; use find|xargs so this works with BusyBox grep in CI (alpine:3).
check-embed: ## Forbid embedding .md files as help text; use heredoc.Doc instead
	@matches=$$(find internal/ -name '*.go' -print0 | xargs -0 grep -nE '^[[:space:]]*//go:embed[[:space:]]+[^[:space:]]*\.md([[:space:]]|$$)' 2>/dev/null || true); \
	if [ -n "$$matches" ]; then \
		echo "Forbidden pattern: //go:embed of a .md file. Inline help text via heredoc.Doc:"; \
		echo "$$matches"; \
		exit 1; \
	fi

ifdef HASGOTESTSUM
bin/gotestsum:
	@echo "Skip this"
else
bin/gotestsum: bin/gotestsum-${GOTESTSUM_VERSION}
	@ln -sf gotestsum-${GOTESTSUM_VERSION} bin/gotestsum
endif

bin/gotestsum-${GOTESTSUM_VERSION}:
	@mkdir -p bin
	curl -L https://github.com/gotestyourself/gotestsum/releases/download/v${GOTESTSUM_VERSION}/gotestsum_${GOTESTSUM_VERSION}_${OS}_${ARCH}.tar.gz | tar -zOxf - gotestsum > ./bin/gotestsum-${GOTESTSUM_VERSION} && chmod +x ./bin/gotestsum-${GOTESTSUM_VERSION}

TEST_PKGS ?= ./internal/... ./cmd/...
.PHONY: test
# NOTE: some tests require uncustomized environment variables for VISUAL, EDITOR, and PAGER to test
# certain behaviors related to glab output preferences. Also, the CI_PROJECT_PATH environment variable
# is set to support forked clones that will have a different origin remote url.
#
# Finally, there are some integration tests perform actual API calls and require GITLAB_TOKEN (personal access token)
# and GITLAB_TEST_HOST (GitLab instance to test) to be set. If either of these are not set the integration tests
# will fail in CI or will be skipped if not in CI.
test: TEST_FORMAT ?= short
test: SHELL = /bin/bash # set environment variables to ensure consistent test behavior
test: VISUAL=
test: EDITOR=
test: PAGER=
test: GITLAB_TOKEN=
test: export CI_PROJECT_PATH=$(shell git remote get-url origin)
test: bin/gotestsum ## Run tests
	$(GOTEST) --no-summary=skipped --format-hide-empty-pkg --junitfile ./coverage.xml --format ${TEST_FORMAT} -- -coverprofile=./coverage.txt -covermode=atomic $(filter-out -v,${GOARGS}) $(if ${TEST_PKGS},${TEST_PKGS},./...)

.PHONY: test-changed
test-changed: bin/gotestsum ## Run tests on packages changed vs origin/main (including reverse dependencies)
	$(eval CHANGED_PKGS := $(shell ./scripts/changed-test-pkgs.sh))
	@if [ -z "$(CHANGED_PKGS)" ]; then \
		echo "No Go packages changed vs origin/main — skipping tests"; \
	else \
		$(MAKE) test TEST_PKGS="$(CHANGED_PKGS)"; \
	fi

.PHONY: test-race
test-race: TEST_FORMAT ?= short
test-race: SHELL = /bin/bash # set environment variables to ensure consistent test behavior
test-race: VISUAL=
test-race: EDITOR=
test-race: PAGER=
test-race: GITLAB_TOKEN=
test-race: export CI_PROJECT_PATH=$(shell git remote get-url origin)
test-race: bin/gotestsum ## Run tests with race detection
	$(GOTEST) --no-summary=skipped --format-hide-empty-pkg --junitfile ./coverage.xml --format ${TEST_FORMAT} -- -coverprofile=./coverage.txt -covermode=atomic -race $(filter-out -v,${GOARGS}) $(if ${TEST_PKGS},${TEST_PKGS},./...)

.PHONY: integration-test-race
integration-test-race: TEST_FORMAT ?= short
integration-test-race: SHELL = /bin/bash # set environment variables to ensure consistent test behavior
integration-test-race: VISUAL=
integration-test-race: EDITOR=
integration-test-race: PAGER=
integration-test-race: export CI_PROJECT_PATH=$(shell git remote get-url origin)
integration-test-race: bin/gotestsum ## Run tests with race detection
	$(GOTEST) --no-summary=skipped --format-hide-empty-pkg --junitfile ./coverage.xml --format ${TEST_FORMAT} -- -coverprofile=./coverage.txt -covermode=atomic -race -tags=integration $(filter-out -v,${GOARGS}) $(if ${TEST_PKGS},${TEST_PKGS},./...) -count=1

ifdef HASGOCILINT
bin/golangci-lint:
	@echo "Skip this"
else
bin/golangci-lint: bin/golangci-lint-${GOLANGCI_LINT_VERSION}
	@ln -sf golangci-lint-${GOLANGCI_LINT_VERSION} bin/golangci-lint
endif

bin/golangci-lint-${GOLANGCI_LINT_VERSION}:
	@mkdir -p bin
	curl -sfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | bash -s -- -b ./bin v${GOLANGCI_LINT_VERSION}
	@mv bin/golangci-lint $@

.PHONY: coverage
coverage: ## Run coverage report
	go tool cover -func coverage.txt

.PHONY: lint
lint: bin/golangci-lint ## Run linter
	$(GOLINT) run

.PHONY: fix
fix: bin/golangci-lint ## Fix lint violations
	$(GOLINT) run --fix
	gofmt -s -w .
	goimports -w .

.PHONY: generate
generate: ## Run go generate
	go generate ./...

.PHONY: list-todo
list-todo: ## Detect FIXME, TODO and other comment keywords
	golangci-lint run --enable=godox --disable-all

.PHONY: bootstrap
bootstrap: ## Install development tools for git hooks
	@./scripts/bootstrap.sh

.PHONY: lint-shell
lint-shell: ## Lint all shell scripts
	@if command -v shellcheck > /dev/null 2>&1; then \
		echo "Linting shell scripts..."; \
		find . -name "*.sh" -type f -not -path "*/node_modules/*" -exec shellcheck {} \;; \
		echo "✓ Shell script linting complete"; \
	else \
		echo "⚠️  shellcheck not found. Run 'make bootstrap' to install tools."; \
		exit 1; \
	fi

# Add custom targets here
-include custom.mk

.PHONY: list
list: ## List all make targets
	@${MAKE} -pRrn : -f $(MAKEFILE_LIST) 2>/dev/null | awk -v RS= -F: '/^# File/,/^# Finished Make data base/ {if ($$1 !~ "^[#.]") {print $$1}}' | egrep -v -e '^[^[:alnum:]]' -e '^$@$$' | sort

.PHONY: help
help:
	@grep -h -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'
