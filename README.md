# 🧹 broom

> Sweep away duplicate files — fast, safe, terminal-native.

[![Go](https://img.shields.io/badge/go-1.22+-00ADD8?logo=go)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/license-MIT-F4A460)](LICENSE)

## Features

- **Three-pass scanning** — size filter → quick 4KB hash → full SHA256
- **Beautiful TUI** — built with [Bubbletea](https://github.com/charmbracelet/bubbletea)
- **Safe by default** — archive mode preserves files with a full manifest
- **Parallel hashing** — saturates your SSD using all CPU cores
- **Smart excludes** — skips `node_modules`, `.git`, `dist`, `build` etc. by default
- **Cancellable** — press `q` at any time to stop immediately
- **Scriptable** — `--dry-run`, `--archive`, `--delete` flags for automation
- **Hardlink aware** — skips duplicate inodes

## Install

```bash
# Go install
go install github.com/skrunchtech/broom/cmd/broom@latest

# Or download from releases
# https://github.com/skrunchtech/broom/releases
```

## Usage

```bash
# Interactive TUI — scan one or more paths
broom ~/Documents
broom ~/Documents ~/Downloads
broom "/Volumes/Extreme SSD/ls_macbook_backup" "/Volumes/Extreme SSD/macbook-pro-m1"

# Dry run — show what would be removed, touch nothing
broom --dry-run ~/Documents

# Archive duplicates to a safe folder before removing (recommended for first run)
broom --archive=/tmp/broom-archive ~/Documents

# Archive against a real backup drive (run overnight for large drives)
broom --archive="/Volumes/Extreme SSD/_broom-archive" \
      "/Volumes/Extreme SSD/ls_macbook_backup" \
      "/Volumes/Extreme SSD/macbook-pro-m1"

# Delete duplicates with confirmation prompt
broom --delete ~/Documents

# Delete without confirmation (for scripting/automation)
broom --delete --yes ~/Documents

# Only consider files larger than 10MB
broom --min-size=10MB ~/Documents

# Choose which file to keep per group (default: newest)
broom --keep=newest ~/Documents   # keep most recently modified
broom --keep=oldest ~/Documents   # keep oldest
broom --keep=largest ~/Documents  # keep largest

# Add extra directories to exclude
broom --exclude=vendor --exclude=target ~/Documents

# Disable default excludes (scan everything including node_modules, .git etc.)
broom --no-default-excludes ~/Documents

# Include hidden files and directories
broom --include-hidden ~/Documents

# Control parallelism (default: number of CPU cores)
broom --workers=4 ~/Documents

# Print version
broom --version
```

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--dry-run` | false | Show what would be removed, touch nothing |
| `--archive=<dir>` | — | Move duplicates to `<dir>` with a manifest for verification |
| `--delete` | false | Permanently delete duplicates |
| `--yes` | false | Skip confirmation prompts (use with `--delete`) |
| `--min-size=<size>` | `1MB` | Skip files smaller than this (e.g. `500KB`, `10MB`, `1GB`) |
| `--keep=<strategy>` | `newest` | Which file to keep: `newest`, `oldest`, or `largest` |
| `--exclude=<name>` | — | Skip directories with this name (repeatable) |
| `--no-default-excludes` | false | Don't skip `node_modules`, `.git`, `dist` etc. |
| `--include-hidden` | false | Include hidden files and directories |
| `--workers=<n>` | num CPUs | Number of parallel hash workers |
| `--version` | — | Print version and exit |

## Default excluded directories

These are skipped automatically to avoid noise from build artifacts and version control:

`node_modules` · `.git` · `.hg` · `.svn` · `__pycache__` · `.venv` · `venv` · `dist` · `build` · `.next` · `.nuxt` · `.Trash` · `.Spotlight-V100` · `.fseventsd`

Use `--no-default-excludes` to scan everything, or `--exclude=<name>` to add more.

## TUI Controls

| Key | Action |
|-----|--------|
| `j` / `k` | Navigate groups |
| `e` / `space` | Expand / collapse group |
| `K` | Auto-mark all groups (keep newest in each) |
| `A` | Select all duplicates across all groups |
| `u` | Unmark / skip current group entirely |
| `D` | Delete all marked files |
| `R` | Archive all marked files |
| `P` | Dry-run preview (show what would happen) |
| `q` | Quit (cancels scan immediately if in progress) |

## Archive mode

The safest way to clean up. Instead of deleting, broom moves duplicates to a
timestamped folder and writes a `manifest.json` so you can verify before
permanently removing anything:

```
/tmp/broom-archive/
  2026-04-05T14-32-01/
    Documents/old-copy.pdf
    Downloads/duplicate.zip
    manifest.json              ← full log of every move, original paths preserved
```

To restore, just move files back using the paths in `manifest.json`.

## Build from source

```bash
git clone https://github.com/skrunchtech/broom
cd broom
go build -o broom ./cmd/broom
./broom --help
```

## License

MIT © [skrunchtech](https://github.com/skrunchtech)
