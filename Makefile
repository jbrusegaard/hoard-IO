# Makefile - hoardio (AI-verified Go development)
BIN      := hoardio
PKG      := ./cmd/hoardio
VERSION  := $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS  := -X main.version=$(VERSION)

# GOOS/GOARCH pairs the whole module must compile for: the unix fstatat path and
# the portable stat fallback each have to keep building, and the TUI must work on
# every one of them (bubbletea does not support js/wasm or plan9, so those stay
# out of the list).
CROSS_TARGETS := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64 \
                 freebsd/amd64 netbsd/amd64 openbsd/amd64 dragonfly/amd64 \
                 solaris/amd64 aix/ppc64

COVERAGE_THRESHOLD := 70 # raise toward 80 as cmd/ gains tests
PATCH_THRESHOLD    := 80
MUTATION_THRESHOLD := 60

.PHONY: all build run test test-verbose lint coverage patch-coverage security \
        mutation deadcode bench profile build-check verify \
        vet fmt check install clean

all: verify

## Build and run
build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) $(PKG)

run: build
	./$(BIN) $(ARGS)

install:
	go install -ldflags "$(LDFLAGS)" $(PKG)

## Tests
test:
	go test -race -shuffle=on -count=1 ./...

test-verbose:
	go test -race -shuffle=on -count=1 -v ./...

## Lint (golangci-lint v2 config: .golangci.yml; revive.toml is for standalone revive)
lint:
	golangci-lint run ./...
	go vet ./...

## Coverage gate
coverage:
	go test -coverprofile=coverage.out -covermode=atomic ./...
	@COVERAGE=$$(go tool cover -func=coverage.out | grep total | awk '{print $$NF}' | sed 's/%//'); \
	echo "Coverage: $${COVERAGE}%"; \
	if [ $$(echo "$${COVERAGE} < $(COVERAGE_THRESHOLD)" | bc -l) -eq 1 ]; then \
		echo "FAIL: Coverage $${COVERAGE}% is below threshold $(COVERAGE_THRESHOLD)%"; \
		exit 1; \
	fi

## Patch coverage (changed lines vs main; skips gracefully without git or Go changes)
patch-coverage:
	@if ! git rev-parse --git-dir >/dev/null 2>&1; then \
		echo "Not a git repository, skipping patch coverage"; exit 0; \
	fi; \
	MERGE_BASE=$$(git merge-base main HEAD 2>/dev/null || echo "$$(git rev-parse HEAD)"); \
	if [ "$$MERGE_BASE" = "$$(git rev-parse HEAD)" ]; then \
		echo "On main branch, skipping patch coverage"; exit 0; \
	fi; \
	CHANGED_FILES=$$(git diff --name-only "$$MERGE_BASE"...HEAD -- '*.go' | grep -v '_test.go' || true); \
	if [ -z "$$CHANGED_FILES" ]; then \
		echo "No non-test Go files changed, skipping patch coverage"; exit 0; \
	fi; \
	go test -coverprofile=coverage.out -covermode=atomic ./... > /dev/null 2>&1; \
	TOTAL=0; COVERED=0; \
	for FILE in $$CHANGED_FILES; do \
		[ -f "$$FILE" ] || continue; \
		for LINE_RANGE in $$(git diff --unified=0 "$$MERGE_BASE"...HEAD -- "$$FILE" | grep '^@@' | sed 's/.*+\([0-9,]*\).*/\1/'); do \
			L=$${LINE_RANGE%%,*}; N=$${LINE_RANGE##*,}; \
			[ "$$N" = "$$L" ] && N=1; \
			for ((i=0; i<N; i++)); do \
				TOTAL=$$((TOTAL + 1)); \
				if awk -v f="$$FILE" -v ln="$$((L + i))" -F'[:,. ]+' '$$1 ~ f && $$2 <= ln && ln <= $$3 {found=1} END {exit !found}' coverage.out; then \
					COVERED=$$((COVERED + 1)); \
				fi; \
			done; \
		done; \
	done; \
	if [ "$$TOTAL" -eq 0 ]; then echo "No executable changed lines detected"; exit 0; fi; \
	PCT=$$((COVERED * 100 / TOTAL)); \
	echo "Patch coverage: $$COVERED/$$TOTAL lines = $$PCT%"; \
	if [ "$$PCT" -lt "$(PATCH_THRESHOLD)" ]; then \
		echo "FAIL: Patch coverage $$PCT% is below threshold $(PATCH_THRESHOLD)%"; \
		exit 1; \
	fi

## Security scanning (tools installed separately, see CLAUDE.md)
security:
	gosec ./...
	govulncheck ./...

## Mutation testing (gremlins)
mutation:
	gremlins unleash --workers 1 --timeout-coefficient 3 --threshold-efficacy $(MUTATION_THRESHOLD)

## Dead code detection
deadcode:
	deadcode ./...

## Benchmarks and profiling
bench:
	go test -bench=. -benchmem -count=3 -run='^$$' ./... | tee bench.txt

profile:
	go test -bench=. -benchmem -cpuprofile=cpu.prof -memprofile=mem.prof -run='^$$' ./...
	@echo "CPU profile: go tool pprof cpu.prof"
	@echo "Memory profile: go tool pprof mem.prof"

## Build validation
build-check:
	go build ./...
	go mod verify

## Cross-compile gate: the portable stat fallback must keep building
cross:
	@for t in $(CROSS_TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; \
		printf "  %s/%s\n" "$$os" "$$arch"; \
		GOOS=$$os GOARCH=$$arch go build ./... || exit 1; \
	done

## Meta-target: everything that must pass before commit
verify: lint test coverage patch-coverage security deadcode build-check cross
	@echo "All verification checks passed."

## Legacy quick gate (vet + gofmt)
check: vet
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "run 'make fmt'"; exit 1; }

race:
	go test -race -timeout 300s ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

clean:
	rm -f $(BIN) coverage.out patch_cov.tmp bench.txt cpu.prof mem.prof
