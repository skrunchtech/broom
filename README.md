# 🧹 broom

> Sweep away duplicate files — fast, safe, terminal-native.

[![Go](https://img.shields.io/badge/go-1.22+-00ADD8?logo=go)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/license-MIT-F4A460)](LICENSE)

## Features

- **Three-pass scanning** — size filter → quick 4KB hash → full SHA256
- **Beautiful TUI** — built with [Bubbletea](https://github.com/charmbracelet/bubbletea)
- **Safe by default** — archive mode preserves files with a full manifest
- **Parallel hashing** — saturates your SSD using all CPU cores
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
# Interactive TUI (default)
broom ~/Documents ~/Downloads

# Dry run — see what would be removed
broom --dry-run /Volumes/Extreme\ SSD

# Archive duplicates for safe verification
broom --archive=/tmp/broom-archive /Volumes/Extreme\ SSD

# Delete with confirmation
broom --delete --keep=newest ~/backups

# Delete without confirmation (scripting)
broom --delete --yes --min-size=10MB ~/backups
```

## TUI Controls

| Key | Action |
|-----|--------|
| `j/k` | Navigate groups |
| `e` / `space` | Expand/collapse group |
| `K` | Auto-mark all (keep newest) |
| `A` | Select all duplicates |
| `D` | Delete marked files |
| `R` | Archive marked files |
| `P` | Dry-run preview |
| `q` | Quit |

## Build from source

```bash
git clone https://github.com/skrunchtech/broom
cd broom
go build ./cmd/broom
./broom --help
```

## License

MIT © [skrunchtech](https://github.com/skrunchtech)
