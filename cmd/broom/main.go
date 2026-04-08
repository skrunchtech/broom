package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/skrunchtech/broom/internal/actions"
	"github.com/skrunchtech/broom/internal/scanner"
	"github.com/skrunchtech/broom/internal/tui"
)

const version = "0.1.0"

var usage = `broom — sweep away duplicate files

USAGE
  broom [flags] [paths...]

  With no flags, launches the interactive TUI.

FLAGS
  --dry-run            Show what would be removed, touch nothing
  --archive=<dir>      Move duplicates to <dir> for verification
  --delete             Permanently delete duplicates (with confirmation)
  --yes                Skip confirmation prompts (for scripting)
  --min-size=<bytes>   Skip files smaller than this (default: 1MB)
  --workers=<n>        Parallel hash workers (default: num CPUs)
  --keep=newest|oldest|largest
                       Which file to keep per group (default: newest)
  --exclude=<name>     Skip directories with this name (repeatable)
  --no-default-excludes
                       Don't skip node_modules, .git, etc. by default
  --include-hidden     Include hidden files and directories
  --json               Emit duplicate groups as JSON (progress goes to stderr)
  --version            Print version and exit

EXAMPLES
  broom ~/Documents ~/Downloads
  broom --dry-run /Volumes/Extreme\ SSD/ls_macbook_backup
  broom --archive=/tmp/broom-archive --yes /Volumes/Extreme\ SSD
`

func main() {
	var excludeFlags multiFlag
	var (
		dryRun            = flag.Bool("dry-run", false, "")
		archiveDir        = flag.String("archive", "", "")
		doDelete          = flag.Bool("delete", false, "")
		yes               = flag.Bool("yes", false, "")
		minSizeStr        = flag.String("min-size", "1MB", "")
		workers           = flag.Int("workers", 0, "")
		keepStr           = flag.String("keep", "newest", "")
		noDefaultExcludes = flag.Bool("no-default-excludes", false, "")
		includeHidden     = flag.Bool("include-hidden", false, "")
		folderMode        = flag.Bool("folders", false, "")
		jsonOut           = flag.Bool("json", false, "")
		showVersion       = flag.Bool("version", false, "")
	)
	flag.Var(&excludeFlags, "exclude", "")
	flag.Usage = func() { fmt.Print(usage) }
	flag.Parse()

	if *showVersion {
		fmt.Println("broom", version)
		os.Exit(0)
	}

	paths := flag.Args()

	opts := scanner.DefaultOptions()
	if *workers > 0 {
		opts.Workers = *workers
	}
	opts.IncludeHidden = *includeHidden
	minSize, err := parseSize(*minSizeStr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	opts.MinSize = minSize
	if *noDefaultExcludes {
		opts.ExcludeDirs = nil
	}
	opts.ExcludeDirs = append(opts.ExcludeDirs, excludeFlags...)

	strategy := actions.KeepNewest
	switch strings.ToLower(*keepStr) {
	case "oldest":
		strategy = actions.KeepOldest
	case "largest":
		strategy = actions.KeepLargest
	}

	// JSON output mode.
	if *jsonOut {
		if len(paths) == 0 {
			fmt.Fprintln(os.Stderr, "error: provide at least one path")
			os.Exit(1)
		}
		runJSON(paths, opts, strategy)
		return
	}

	// Scripting modes (no TUI).
	if *dryRun || *archiveDir != "" || *doDelete {
		if len(paths) == 0 {
			fmt.Fprintln(os.Stderr, "error: provide at least one path")
			os.Exit(1)
		}
		runHeadless(paths, opts, strategy, *dryRun, *archiveDir, *doDelete, *yes)
		return
	}

	// Interactive TUI.
	m := tui.NewModel(paths, opts, strategy, *archiveDir, *folderMode)
	p := tea.NewProgram(&m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// jsonFile is a single file entry in the JSON output.
type jsonFile struct {
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

// jsonGroup is one duplicate group in the JSON output.
type jsonGroup struct {
	Hash             string     `json:"hash"`
	Size             int64      `json:"size"`
	Keep             jsonFile   `json:"keep"`
	Duplicates       []jsonFile `json:"duplicates"`
	RecoverableBytes int64      `json:"recoverable_bytes"`
}

func runJSON(paths []string, opts scanner.Options, strategy actions.KeepStrategy) {
	passNames := []string{"", "Walking", "Quick hash", "Full hash"}
	groups, err := scanner.Scan(context.Background(), paths, opts, func(pass, done, total int) {
		if total > 0 {
			fmt.Fprintf(os.Stderr, "\r  %s  %d/%d", passNames[pass], done, total)
		}
	})
	fmt.Fprintln(os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "scan error:", err)
		os.Exit(1)
	}

	out := make([]jsonGroup, 0, len(groups))
	for _, g := range groups {
		keepIdx := actions.SelectKeeper(g, strategy)
		keep := g.Files[keepIdx]
		var dupes []jsonFile
		var recoverableBytes int64
		for i, f := range g.Files {
			if i == keepIdx {
				continue
			}
			dupes = append(dupes, jsonFile{Path: f.Path, Size: f.Size, ModTime: f.ModTime})
			recoverableBytes += f.Size
		}
		out = append(out, jsonGroup{
			Hash:             g.Hash,
			Size:             g.Size,
			Keep:             jsonFile{Path: keep.Path, Size: keep.Size, ModTime: keep.ModTime},
			Duplicates:       dupes,
			RecoverableBytes: recoverableBytes,
		})
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, "json error:", err)
		os.Exit(1)
	}
}

func runHeadless(paths []string, opts scanner.Options, strategy actions.KeepStrategy,
	dryRun bool, archiveDir string, doDelete bool, yes bool) {

	fmt.Println("Scanning...")
	groups, err := scanner.Scan(context.Background(), paths, opts, func(pass, done, total int) {
		passNames := []string{"", "Walking", "Quick hash", "Full hash"}
		if total > 0 {
			fmt.Printf("\r  Pass %d/%s  %d/%d", pass, passNames[pass], done, total)
		}
	})
	fmt.Println()
	if err != nil {
		fmt.Fprintln(os.Stderr, "scan error:", err)
		os.Exit(1)
	}

	if len(groups) == 0 {
		fmt.Println("No duplicates found.")
		return
	}

	var totalBytes int64
	for _, g := range groups {
		keepIdx := actions.SelectKeeper(g, strategy)
		for i, f := range g.Files {
			if i != keepIdx {
				totalBytes += f.Size
			}
		}
	}
	fmt.Printf("Found %d duplicate groups (%s recoverable)\n", len(groups), humanBytes(totalBytes))

	if dryRun {
		results := actions.DryRun(groups, strategy)
		for _, r := range results {
			fmt.Printf("%-12s %s\n", r.Action, r.Path)
		}
		return
	}

	if !yes {
		fmt.Print("Proceed? [y/N] ")
		var input string
		fmt.Scanln(&input)
		if strings.ToLower(input) != "y" {
			fmt.Println("Aborted.")
			return
		}
	}

	if archiveDir != "" {
		results, err := actions.Archive(groups, strategy, archiveDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, "archive error:", err)
			os.Exit(1)
		}
		archived := 0
		for _, r := range results {
			if r.Action == "archived" {
				archived++
			}
		}
		fmt.Printf("Archived %d files to %s\n", archived, archiveDir)
		return
	}

	if doDelete {
		results, err := actions.Delete(groups, strategy)
		if err != nil {
			fmt.Fprintln(os.Stderr, "delete error:", err)
			os.Exit(1)
		}
		deleted := 0
		for _, r := range results {
			if r.Action == "deleted" {
				deleted++
			}
		}
		fmt.Printf("Deleted %d files\n", deleted)
	}
}

func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	multipliers := map[string]int64{
		"KB": 1024,
		"MB": 1024 * 1024,
		"GB": 1024 * 1024 * 1024,
	}
	for suffix, mult := range multipliers {
		if strings.HasSuffix(s, suffix) {
			n, err := strconv.ParseInt(strings.TrimSuffix(s, suffix), 10, 64)
			if err != nil || n <= 0 {
				return 0, fmt.Errorf("invalid size %q", s)
			}
			if n > math.MaxInt64/mult {
				return 0, fmt.Errorf("size %q overflows int64", s)
			}
			return n * mult, nil
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	return n, nil
}

// multiFlag allows a flag to be specified multiple times.
type multiFlag []string

func (f *multiFlag) String() string  { return strings.Join(*f, ",") }
func (f *multiFlag) Set(v string) error { *f = append(*f, v); return nil }

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
