SHELL := /bin/sh
.DEFAULT_GOAL := build

export LC_ALL := C
export TZ := UTC
export GOFLAGS := -mod=readonly

GO ?= go
GOFMT ?= gofmt
GIT ?= git

TOOLCHAIN := go1.26.5
MODULE := github.com/PinoHouse/works-on-my-whiteboard
MAIN_PACKAGE := $(MODULE)/cmd/whiteboard
GENERATED_DIR := generated
BINARY_DIR := $(GENERATED_DIR)/.bin
VERIFY_DIR := $(GENERATED_DIR)/.verify
WHITEBOARD := $(BINARY_DIR)/whiteboard

.NOTPARALLEL:
.PHONY: FORCE toolchain build fmt vet unit fuzz race content validate coverage smoke verify-fast verify-deep evidence audit-evidence verify clean

FORCE:

toolchain:
	@test "$$($(GO) env GOVERSION)" = "$(TOOLCHAIN)"
	@test "$$($(GO) env GOMOD)" = "$(CURDIR)/go.mod"
	@test "$$($(GO) env GOWORK)" = ""
	@test "$$($(GO) env GOFLAGS)" = "-mod=readonly"
	@test "$$(awk '$$1 == "toolchain" { count++; value = $$2 } END { if (count == 1) print value }' go.mod)" = "$(TOOLCHAIN)"
	@$(GO) mod verify

build: $(WHITEBOARD)

$(WHITEBOARD): toolchain FORCE
	@set -eu; \
	revision=$$($(GIT) rev-parse --verify HEAD); \
	verify_binary() { \
		binary=$$1; \
		want_revision=$$2; \
		metadata=$$($(GO) version -m "$$binary" 2>/dev/null) || return 1; \
		printf '%s\n' "$$metadata" | awk \
			-v want_toolchain="$(TOOLCHAIN)" \
			-v want_path="$(MAIN_PACKAGE)" \
			-v want_module="$(MODULE)" \
			-v want_revision="$$want_revision" \
			' \
			NR == 1 { \
				toolchain_rows++; \
				if ($$2 == want_toolchain) toolchain_matches++; \
			} \
			$$1 == "path" { \
				path_rows++; \
				if ($$2 == want_path) path_matches++; \
			} \
			$$1 == "mod" { \
				module_rows++; \
				if ($$2 == want_module) module_matches++; \
			} \
			$$1 == "build" && $$2 ~ /^-trimpath=/ { \
				trimpath_rows++; \
				if ($$2 == "-trimpath=true") trimpath_matches++; \
			} \
			$$1 == "build" && $$2 ~ /^vcs=/ { \
				vcs_rows++; \
				if ($$2 == "vcs=git") vcs_matches++; \
			} \
			$$1 == "build" && $$2 ~ /^vcs[.]revision=/ { \
				revision_rows++; \
				if ($$2 == "vcs.revision=" want_revision) revision_matches++; \
			} \
			$$1 == "build" && $$2 ~ /^vcs[.]modified=/ { \
				modified_rows++; \
				if ($$2 == "vcs.modified=false") modified_matches++; \
			} \
			END { \
				valid = toolchain_rows == 1 && toolchain_matches == 1 \
					&& path_rows == 1 && path_matches == 1 \
					&& module_rows == 1 && module_matches == 1 \
					&& trimpath_rows == 1 && trimpath_matches == 1 \
					&& vcs_rows == 1 && vcs_matches == 1 \
					&& revision_rows == 1 && revision_matches == 1 \
					&& modified_rows == 1 && modified_matches == 1; \
				exit !valid; \
			}'; \
	}; \
	test ! -L "$(GENERATED_DIR)"; \
	test ! -L "$(BINARY_DIR)"; \
	if test -e "$(GENERATED_DIR)"; then test -d "$(GENERATED_DIR)"; fi; \
	if test -e "$(BINARY_DIR)"; then test -d "$(BINARY_DIR)"; fi; \
	if test -f "$@" && test ! -L "$@" && test -x "$@" && verify_binary "$@" "$$revision"; then \
		exit 0; \
	fi; \
	mkdir -p "$(BINARY_DIR)"; \
	candidate=$$(mktemp "$(BINARY_DIR)/.whiteboard-candidate.XXXXXX"); \
	test -f "$$candidate"; \
	test ! -L "$$candidate"; \
	trap 'rm -f "$$candidate"' 0 1 2 3 15; \
	$(GO) build -buildvcs=true -trimpath -o "$$candidate" ./cmd/whiteboard; \
	test -f "$$candidate"; \
	test ! -L "$$candidate"; \
	test -x "$$candidate"; \
	verify_binary "$$candidate" "$$revision"; \
	if test -L "$@"; then rm -f "$@"; fi; \
	test ! -d "$@"; \
	mv -f "$$candidate" "$@"

fmt: toolchain
	@files=$$($(GOFMT) -l .) || exit $$?; \
	if test -n "$$files"; then \
		printf '%s\n' "$$files"; \
		exit 1; \
	fi

vet: toolchain
	@$(GO) vet ./...

unit: toolchain
	@$(GO) test -count=1 ./...

fuzz: toolchain
	@$(GO) test ./internal/evidence -run '^$$' -fuzz '^FuzzCanonicalRecord$$' -fuzztime=2s
	@$(GO) test ./labs/primitives/token-bucket -run '^$$' -fuzz '^FuzzBucketInvariant$$' -fuzztime=2s

race: toolchain
	@$(GO) test -race -count=1 ./...

content: build
	@"$(WHITEBOARD)" validate --root . --content

validate: content

coverage: content
	@"$(WHITEBOARD)" coverage --root . --format markdown --output generated/coverage.md --check

smoke: coverage
	@./scripts/verify-experiments.sh smoke "$(CURDIR)"

verify-fast: fmt vet unit fuzz race smoke

verify-deep: verify-fast
	@./scripts/verify-experiments.sh deep "$(CURDIR)"

evidence: verify-deep
	@"$(WHITEBOARD)" run --required --profile deep --root . --evidence-root evidence --snapshot

audit-evidence: verify-deep
	@set -eu; \
	repository=$$(pwd -P); \
	test ! -L "$(GENERATED_DIR)"; \
	test -d "$(GENERATED_DIR)"; \
	test "$$(cd "$(GENERATED_DIR)" && pwd -P)" = "$$repository/$(GENERATED_DIR)"; \
	test ! -L "$(VERIFY_DIR)"; \
	created_verify=false; \
	if test -e "$(VERIFY_DIR)"; then \
		test -d "$(VERIFY_DIR)"; \
	else \
		umask 077; \
		mkdir -m 700 "$(VERIFY_DIR)"; \
		created_verify=true; \
	fi; \
	test ! -L "$(VERIFY_DIR)"; \
	test -d "$(VERIFY_DIR)"; \
	test "$$(cd "$(VERIFY_DIR)" && pwd -P)" = "$$repository/$(VERIFY_DIR)"; \
	umask 077; \
	scratch=$$(mktemp -d "$(VERIFY_DIR)/audit.XXXXXX"); \
	test -d "$$scratch"; \
	test ! -L "$$scratch"; \
	cleanup() { \
		status=$$?; \
		trap - 0 1 2 3 15; \
		if test ! -L "$(GENERATED_DIR)" \
			&& test -d "$(GENERATED_DIR)" \
			&& test ! -L "$(VERIFY_DIR)" \
			&& test -d "$(VERIFY_DIR)" \
			&& test "$$(cd "$(VERIFY_DIR)" && pwd -P)" = "$$repository/$(VERIFY_DIR)"; then \
			rm -rf "$$scratch"; \
			if test "$$created_verify" = true; then rmdir "$(VERIFY_DIR)" 2>/dev/null || :; fi; \
		fi; \
		exit "$$status"; \
	}; \
	trap cleanup 0; \
	trap 'exit 129' 1; \
	trap 'exit 130' 2; \
	trap 'exit 131' 3; \
	trap 'exit 143' 15; \
	"$(WHITEBOARD)" report --root . --evidence-root evidence --release current --profile deep --format json --output "$$scratch/report.json"

verify: audit-evidence
	@"$(WHITEBOARD)" validate --root . --evidence-root evidence --release current --format text

clean:
	@set -eu; \
	repository=$$(pwd -P); \
	if test ! -e "$(GENERATED_DIR)" && test ! -L "$(GENERATED_DIR)"; then exit 0; fi; \
	test ! -L "$(GENERATED_DIR)"; \
	test -d "$(GENERATED_DIR)"; \
	cd "$(GENERATED_DIR)"; \
	test "$$(pwd -P)" = "$$repository/$(GENERATED_DIR)"; \
	rm -rf .bin .verify
