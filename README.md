# hoardio

**Know thy hoard.** A fast, strictly read-only disk-usage explorer for macOS —
find out where your storage actually went, then browse it in a colorful
terminal UI.

```
$ hoardio ~/Downloads

▸ ████████████░░░░  61.3%   57 GiB  installer-archive.7z
  ████████░░░░░░░░  40.2%   37 GiB  games/
  ██░░░░░░░░░░░░░░   9.8%    9 GiB  video-clips/

116 GiB in 4,021 dirs · 88,310 files · 0s   installer-archive.7z: 57 GiB disk · 57 GiB apparent
↑↓ move · enter open · ← up · n sort · f files · ? help · q quit
```

## Why

`du -sh *` is slow and flat; Activity Storage in System Settings tells you
nothing actionable. `hoardio` walks your whole home directory in about a
minute, rolls sizes up per directory, and drops you into an ncdu-style browser
with WinDirStat-flavored heat bars so the culprits are obvious at a glance.

## Features

- **Strictly read-only.** Only `ReadDir`/`Stat` syscalls — it will never
  delete, move, or write anything. You can run it on your whole home folder
  while asleep. (Well, awake is nicer.)
- **Fast.** Parallel walker tuned for wide trees; roughly matches `du` and
  finishes a full `$HOME` (~2M files) in well under two minutes.
- **APFS-aware sizes.** Sorts by real disk usage (`st_blocks`), not apparent
  size — so sparse files, clones, and compressed files report what they
  actually cost you. The status line shows both, so APFS clones are easy to
  spot.
- **Interactive browser.** Descend/ascend the tree, sort by size or name, hide
  files to see pure directory structure, heat-colored bars relative to the
  largest sibling.
- **Scriptable.** `--text` for a plain top-N report, `--json` for everything.
- **Safe by default.** Symlinks are never followed (no cycles), hardlinks are
  counted once, and `--xdev` keeps you on one filesystem.

## Install

```sh
go install github.com/jbrusegaard/hoardio/cmd/hoardio@latest
```

or from a checkout:

```sh
make build   # ./hoardio
make install # into $GOBIN
```

## Usage

```sh
hoardio                  # scan $HOME, open the interactive browser
hoardio ~/Downloads      # scan a specific path
hoardio --top 20         # plain report: top 20 largest entries (implies --text)
hoardio --min-size 1G    # only show entries at least this large
hoardio --exclude .git --exclude node_modules   # glob against base name or full path
hoardio --xdev           # don't cross filesystem boundaries
hoardio --json           # machine-readable output (implies --text)
```

The interactive browser opens when stdin and stdout are terminals; piped
output falls back to the plain report automatically (`--text` forces it).

### Browser keys

| Key                | Action                       |
| ------------------ | ---------------------------- |
| `↑↓` / `k` `j`     | move cursor                  |
| `enter` `→` `l`    | open directory               |
| `←` `h` `bs` `esc` | go up a level                |
| `n`                | toggle size / name sort      |
| `f`                | toggle showing files         |
| `?`                | help overlay                 |
| `q`, `ctrl+c`      | quit (cancels an in-progress scan) |

## Development

```sh
make check   # vet + gofmt gate
make test    # unit + e2e tests
make race    # everything under the race detector
make run     # build and launch (ARGS=... passes a path)
```

Benchmarks live in `*_bench_test.go`:

```sh
go test ./internal/scanner/ ./internal/report/ -run '^$' -bench . -benchmem
```

## Name

*hoardio* = *hoard* + *I/O*. Your disk is a hoard; this tool inventories it.
