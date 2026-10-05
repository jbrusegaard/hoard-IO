# CLAUDE.md - Go AI-Verified Development

## Project: hoardio

Read-only disk-usage explorer for macOS (`github.com/jbrusegaard/hoardio`).
Walks a filesystem tree with a worker pool, rolls sizes up per directory, and
renders text/JSON or an interactive bubbletea browser.

## Project Architecture

- `cmd/hoardio/` - the single application entrypoint (main package)
- `internal/scanner/` - parallel filesystem walker; hardlink dedupe, excludes, progress callbacks
  - `stat_unix.go` / `stat_other.go` are the platform split: `fstatat` against the
    open dir fd on unix, `os.Lstat` by path elsewhere. Keep every syscall behind
    that seam (`entryInfo`, `statEntry`, `statRoot`, `dirFD`) so Windows and the
    BSDs keep building; tag platform-specific tests the same way.
- `internal/report/` - tree rollup from scan results; text and JSON renderers
- `internal/tui/` - bubbletea model: scan-progress view + ncdu-style browser
- No `pkg/`: nothing here is public API. Keep new code in `internal/`.
- Tests live next to the code they test: `foo.go` -> `foo_test.go`; benchmarks in `*_bench_test.go`

## The Cardinal Rule

**hoardio is strictly read-only.** Only `ReadDir`/`Lstat`/`Stat` syscalls
against scanned paths. Never introduce file writes, deletes, moves, chmods,
or xattr mutations — not in production code, not as a "nice feature". This
promise is the product.

## Verification (Required Before Every Commit)

Run the full verification suite:

```
make verify
```

Individual checks (all must pass):

```
make lint            # golangci-lint v2 (40 linters) + go vet
make test            # go test -race -shuffle=on ./...
make coverage        # coverage gate (threshold: 70%, raise to 80% as cmd/ matures)
make patch-coverage  # changed-lines coverage (threshold: 80%; skips gracefully without git)
make security        # gosec + govulncheck (tools installed separately)
make mutation        # gremlins (threshold: 60%)
make deadcode        # deadcode (unreachable functions)
make build-check     # go build + go mod verify
make cross           # cross-compile gate: 11 GOOS/GOARCH pairs (unix + fallback)
```

Performance diagnostics (not part of verify, use when investigating):

```
make bench           # benchmarks with memory stats (fixtures auto-build under /tmp)
make profile         # CPU + memory profiles for pprof
```

## Code Quality Thresholds

- Test coverage: >=70% overall (internal/* already at ~82%; raise the gate to 80% once cmd/ is tested)
- Mutation score: >=60%
- Cyclomatic complexity: <=10 per function
- Cognitive complexity: <=15 per function
- Function length: <=80 lines, <=50 statements
- Function arguments: <=5
- Function return values: <=3

## Go Code Standards

1. **Error handling**: wrap with context: `fmt.Errorf("operation failed: %w", err)`
2. **Naming**: MixedCaps, no underscores. Acronyms all-caps (HTTP, URL, ID).
3. **Interfaces**: accept interfaces, return structs; define at the consumer.
4. **Context**: first parameter when needed; never stored in structs.
5. **Concurrency**: tests always run with `-race`. The scanner's unbounded
   condvar task queue (`internal/scanner/queue.go`) replaced a bounded-channel
   design that DEADLOCKED on wide trees — do not regress it.
6. **Dependencies**: minimal. bubbletea/lipgloss/go-runewidth for the TUI only;
   `golang.org/x/sys/unix` only inside `stat_unix.go` (never in portable files).
   depguard blocks `io/ioutil` and `github.com/pkg/errors`.
7. **Testing**: table-driven; property-style checks for pure functions; e2e
   test drives the real TUI through pipes (`cmd/hoardio/tui_e2e_test.go`).

## Dependency Policy

- All dependencies pinned in `go.sum`; run `go mod tidy` before commits
- No deprecated packages (enforced by depguard in `.golangci.yml`)
- `govulncheck` must pass clean

## AI-Specific Rules

1. **No tautological tests**: encode expected outputs, don't reimplement logic
2. **No hallucinated imports**: verify every dependency exists before adding it
3. **Human review required**: all code requires human review before merge
4. **Acceptance criteria first**: no code without Given/When/Then criteria
5. **Explain non-obvious decisions**: comment WHY, not WHAT
6. **Integration tests for multi-component features**: wire real objects and
   prove data flows through the actual call chain (see the TUI e2e test)
7. **No vaporware**: every package must be imported by non-test code; unwired
   code is dead code
8. **No lint suppression without permission**: never add `//nolint`,
    `//nolint:lintername`, `//revive:disable`, `#nosec`, or any equivalent
    directive. If a linter flags code, fix the code. If suppression seems
    genuinely warranted, explain why and ask the developer first.

## Git Policy

- Conventional commits: `type(scope): description` (feat, fix, refactor, test, docs, chore, ci)
- Atomic commits: one logical change per commit
- No force-pushing to shared branches
- Branch names: `type/short-description`
- Never add Co-authored-by trailers
