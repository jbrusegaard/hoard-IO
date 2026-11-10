# TOFIX — hoardio audit, 2026-11-09

Full-repo audit report. One-shot review; nothing in the code was changed by it.
Numbering is stable — say "fix 2 and 5" and everyone knows what you mean.

## What this repo does

`hoardio` is a read-only macOS disk-usage explorer. A parallel scanner walks a
directory tree collecting per-file sizes, `report` rolls them up into a
directory tree, and the user gets either a text/JSON report or an interactive
ncdu-style terminal browser.

## Load assumed

One person, one machine, scanning a whole `$HOME` (~2M files per the README).
That is the load the code itself promises, so scale findings are judged against it.

Verified healthy: full suite passes with `-race -shuffle=on`. The condvar
queue's deadlock reasoning holds (push always happens while the parent task is
in flight). Bubbletea v1.3.10's `Send` selects on ctx, so post-quit sends from
the scan goroutine are no-ops, not panics.

---

## Must fix

### [ ] 1. `--exclude` silently does nothing to files (`internal/scanner/scanner.go:258-280`)

- **What this is:** The `--exclude` flag takes glob patterns ("base name or
  full path", per `--help`) and is meant to skip matching entries.
- **Problem:** The matcher is only consulted in `visitDir` (line 250);
  `visitFile` never checks it. Confirmed live: `hoardio --exclude '*.log'`
  still lists and totals a 200 KiB `big.log`. So `--exclude '*.log'` on a home
  dir prunes nothing a user would notice — except it silently keeps the exact
  noise they tried to remove.
- **Fix:** Call `w.matcher.match(e.Name(), path)` in `visitFile` before
  counting, and add 1 to `Skipped` when it hits. Three lines, plus a test —
  the existing `TestExcludes` only excludes directories, which is why this
  shipped.
- **If we skip it:** Users think patterns are broken for files (they are), and
  file noise keeps inflating the top-files list.

### [ ] 2. Files whose name starts with `..` vanish from the tree (`internal/report/report.go:43`)

- **What this is:** `Build` skips files it thinks are "outside root" by
  checking `strings.HasPrefix(rel, "..")`.
- **Problem:** A real file named `..secret` gives `rel == "..secret"`, which
  passes the prefix test and is dropped. Reproduced live: root stats said
  364 KiB, the tree rendered 312 KiB — the 52 KiB `..secret` appeared in the
  top-files table but not the tree. Same bug hides the legacy `..`-prefixed
  dotfiles some tools leave behind.
- **Fix:** Change the check to
  `rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))`.
  One line.
- **If we skip it:** Tree totals silently disagree with the summary line; the
  difference is invisible and unexplainable.

### [ ] 3. Hardlink dedupe records every file, ~100 MB+ on a full-home scan (`internal/scanner/scanner.go:265`, `internal/scanner/fsutil.go:54-80`)

- **What this is:** A global `map[(dev,inode)]` used so a hardlinked file is
  counted once.
- **Problem:** `seen()` is called for every regular file and inserts every
  identity, even though a file with `nlink == 1` can never be a duplicate. At
  the README's 2M-file home scan the map holds ~2M entries (~100 MB+ of Go map
  overhead) to dedupe maybe a handful of files. It works at this scale, but
  the cost is pure waste, and the README promises "under two minutes" on
  memory-starved laptops.
- **Fix:** In `visitFile`, only call `w.links.seen(...)` when
  `statOf(info).Nlink > 1` (the stat is already in hand). The map then holds
  only genuinely-linked files.
- **If we skip it:** No wrong answers, just a ~100 MB resident set on the
  tool's headline workload, for data no one reads.

---

## Should fix

### [ ] 4. `--top` does not imply `--text`, and `--top`/`--min-size`/`--tree-depth` are silently ignored in TUI mode (`cmd/hoardio/main.go:151-164`)

- **What this is:** README says `hoardio --top 20` gives a plain top-20 report
  "(implies --text)".
- **Problem:** `useTUI` only checks `--json`/`--text` (line 153); `top`,
  `minDisk` and `depth` never reach `runTUI`. A user on a terminal running
  `hoardio --top 20 --min-size 1G` gets the interactive browser with both
  flags ignored, no warning.
- **Fix:** In `run()`, force text mode when any of those three flags were
  explicitly set: `flag.Visit(func(f *flag.Flag){ ... })` and set a bool.
  ~5 lines. (Or fix the README — but the flag-implies-mode behavior is the
  nicer UX.)
- **If we skip it:** Documented behavior doesn't exist; the filter flags lie.

### [ ] 5. A scan the user interrupted shows up as if it were complete (`internal/tui/model.go:112-122`)

- **What this is:** Ctrl+C during a TUI scan cancels the scan; hoardio keeps
  the partial results, which is right.
- **Problem:** `m.interrupted` is set from `doneMsg` (line 114) and then never
  read — grep confirms no render site. The text report warns "results are
  partial" (`main.go:246`); the TUI shows the partial tree with zero
  indication, and the user judges disk usage from half a scan.
- **Fix:** Append `· partial` to `statusLine()` when `m.interrupted` is true.
  One line. (Deleting the field is wrong here — the information matters.)
- **If we skip it:** Someone decides to delete 200 GiB based on a scan that
  covered a third of their home.

### [ ] 6. The report points at a `--errors` flag that doesn't exist (`internal/report/report.go:184`)

- **What this is:** When directories are unreadable, the summary prints
  "see --errors or JSON output".
- **Problem:** There is no `--errors` flag anywhere in `main.go` (grepped).
  Told about an escape hatch the user can't find. Note the full list is
  already printed by `writeUnreadable` at the bottom of the same report, so
  even "JSON output" is redundant.
- **Fix:** Drop the parenthetical:
  `"%d directories could not be read (listed below)\n"`. One string.
- **If we skip it:** Users hunt for a flag in `--help` forever.

### [x] 7. Coverage gates are configured but nothing enforces them (repo root) — SKIPPED by decision

> **Decision 2026-11-09:** not a serious enough project to carry CI plus a
> codecov token. `codecov.yml` deleted; no CI workflow added. `make verify`
> stays a local, manual gate.


- **What this is:** `codecov.yml` exists with project/patch targets, and
  CLAUDE.md makes `make verify` mandatory before every commit.
- **Problem:** There is no CI at all — no `.github/` directory, no workflow
  that runs tests or uploads coverage. Every gate (`verify`, the 70%
  threshold, govulncheck, mutation) only exists if the developer remembers to
  run it locally, and codecov has nothing to receive. Also codecov ignores
  `cmd/**/*` while the Makefile counts cmd/ in its 70% — two gates, different
  rules.
- **Fix:** One GitHub Actions workflow: checkout, `go test -race`, upload
  coverage to codecov. A few dozen YAML, no Go changes.
- **If we skip it:** The quality bar in CLAUDE.md is aspirational; the next
  regression merges silently.

---

## Lean

### [ ] 8. `dirTask.dev` is written three times and never read (`internal/scanner/scanner.go:62`)

- **What this is:** The queued task carries the directory's device id, set at
  `scanner.go:116` and `scanner.go:255`.
- **Problem:** The XDev check (line 250) recomputes the child's dev from the
  entry it's examining; nothing ever reads `t.dev`. Grep across all files,
  tests included: zero reads.
- **Fix:** Delete the field and the two struct-literal assignments. Four lines
  gone.
- **If we skip it:** Next reader wastes time hunting which code path uses it;
  one of them is `walk`.

### [ ] 9. `revive.toml` configures a linter this repo never runs (repo root)

- **What this is:** A standalone `revive` config file.
- **Problem:** `make lint` runs `golangci-lint` + `go vet` only; no target,
  script or CI invokes `revive` (checked the Makefile and the golangci
  config reference). The equivalent rules live in `.golangci.yml`'s revive
  settings.
- **Fix:** Delete `revive.toml`. Its own header comment ("for standalone
  revive") is the only thing keeping it alive.
- **If we skip it:** Someone edits it, nothing changes, confusion follows.

### [ ] 10. Duplicate defaults and a duplicated make target (`cmd/hoardio/main.go:120,126` vs `internal/report/report.go:136-141`; `Makefile:120-121`)

- **What this is:** "top 30" and "depth 2" defaults live in both `main.go` and
  `report.Text`; `make race` re-runs exactly what `make test` already does
  (both `-race`).
- **Problem:** Change one default, miss the other, and the flag help disagrees
  with the output. `race` is a stale twin of `test` that only invites
  confusion about which one CI should run.
- **Fix:** Make `main` the single source for the defaults (pass explicit
  values; keep the `report` guard for 0→default only if JSON callers need it),
  and drop the `race` target or make it an alias of `test`.
- **If we skip it:** Small, but it's the kind of drift that ships a README
  that lies.

---

## Nice to have

### [ ] 11. Error prefix "tui:" is applied twice (`cmd/hoardio/main.go:71` and `:156`)

- **What this is:** `runTUI` wraps its errors with `tui: %w`; `run` wraps
  `runTUI`'s return with `tui: %w` again.
- **Problem:** A TUI failure prints
  `hoardio: tui: tui: read stdin: ...`.
- **Fix:** Drop the wrap in one of the two sites — line 156 can just
  `return err` since `runTUI` already labels. One line.
- **If we skip it:** Cosmetic; confused users only.

### [ ] 12. `isTerminal` accepts `/dev/null` (`cmd/hoardio/main.go:252-259`)

- **What this is:** TTY detection via `ModeCharDevice` to choose TUI vs text.
- **Problem:** `/dev/null` is a character device, so `hoardio > /dev/null`
  from a real terminal opens an invisible interactive UI that eats your quit
  keys in a file. `go-isatty` is already in the dependency tree (bubbletea
  pulls it) and does the `tcgetattr` check correctly.
- **Fix:** Swap the mode-bit test for `isatty.IsTerminal(f.Fd())`; promote the
  indirect dep to direct. ~3 lines.
- **If we skip it:** Rare redirection pipelines hang waiting for input nobody
  can see.

---

## Verdict

Healthy core — the concurrency design is right, tested, and race-clean — but
three findings are real bugs or real waste on the exact load the README
promises: fix 1 and 2 (wrong output, confirmed live) and 3 (100 MB for
nothing). (CI was considered after this audit and dropped as not worth it —
see finding 7.)

Lean: -~40 lines, -0 dependencies possible.

## Not checked

`make lint`/gosec/govulncheck/gremlins (tools not invoked here — suite and
race were), real 2M-file scan timing, TUI rendering on a physical terminal
(e2e test covers piping, not pixels), `.golangci.yml` rule-by-rule, whether a
remote CI exists outside this checkout (none is in the repo).
