package actions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/skrunchtech/broom/internal/scanner"
)

// KeepStrategy determines which file in a duplicate group to keep.
type KeepStrategy int

const (
	KeepNewest  KeepStrategy = iota // keep most recently modified
	KeepOldest                      // keep oldest
	KeepLargest                     // keep largest (same size dupes, so rarely differs)
)

// Result records what happened to a file.
type Result struct {
	Action   string `json:"action"` // "kept", "deleted", "archived", "would-delete"
	Path     string `json:"path"`
	Original string `json:"original,omitempty"` // for archived files
	Hash     string `json:"hash"`
}

// ManifestEntry is written to manifest.json in the archive dir.
type ManifestEntry struct {
	RunID    string    `json:"run_id"`
	Time     time.Time `json:"time"`
	Results  []Result  `json:"results"`
}

// SelectKeeper returns the index of the file to keep within a group.
func SelectKeeper(group scanner.DuplicateGroup, strategy KeepStrategy) int {
	files := group.Files
	idx := 0
	for i := 1; i < len(files); i++ {
		switch strategy {
		case KeepNewest:
			if files[i].ModTime.After(files[idx].ModTime) {
				idx = i
			}
		case KeepOldest:
			if files[i].ModTime.Before(files[idx].ModTime) {
				idx = i
			}
		case KeepLargest:
			if files[i].Size > files[idx].Size {
				idx = i
			}
		}
	}
	return idx
}

// DryRun prints what would happen without touching any files.
func DryRun(groups []scanner.DuplicateGroup, strategy KeepStrategy) []Result {
	var results []Result
	for _, g := range groups {
		keepIdx := SelectKeeper(g, strategy)
		for i, f := range g.Files {
			action := "would-delete"
			if i == keepIdx {
				action = "kept"
			}
			results = append(results, Result{
				Action: action,
				Path:   f.Path,
				Hash:   g.Hash,
			})
		}
	}
	return results
}

// Delete removes duplicate files, keeping one per group per strategy.
func Delete(groups []scanner.DuplicateGroup, strategy KeepStrategy) ([]Result, error) {
	var results []Result
	for _, g := range groups {
		keepIdx := SelectKeeper(g, strategy)
		for i, f := range g.Files {
			if i == keepIdx {
				results = append(results, Result{Action: "kept", Path: f.Path, Hash: g.Hash})
				continue
			}
			if err := os.Remove(f.Path); err != nil {
				return results, fmt.Errorf("delete %s: %w", f.Path, err)
			}
			results = append(results, Result{Action: "deleted", Path: f.Path, Hash: g.Hash})
		}
	}
	return results, nil
}

// Archive moves duplicate files to archiveDir, preserving relative paths.
// A manifest.json is written to archiveDir/<runID>/manifest.json.
func Archive(groups []scanner.DuplicateGroup, strategy KeepStrategy, archiveDir string) ([]Result, error) {
	runID := time.Now().Format("2006-01-02T15-04-05")
	runDir := filepath.Join(archiveDir, runID)
	if err := os.MkdirAll(runDir, 0700); err != nil {
		return nil, err
	}

	// Find common root to strip from archived paths.
	root := commonRoot(groups)
	cleanRunDir := filepath.Clean(runDir)

	var results []Result
	for _, g := range groups {
		keepIdx := SelectKeeper(g, strategy)
		for i, f := range g.Files {
			if i == keepIdx {
				results = append(results, Result{Action: "kept", Path: f.Path, Hash: g.Hash})
				continue
			}

			rel, err := filepath.Rel(root, f.Path)
			if err != nil || strings.HasPrefix(rel, "..") {
				rel = filepath.Base(f.Path)
			}
			dest := filepath.Join(runDir, rel)
			// Defence-in-depth: ensure dest is still under runDir after Join.
			cleanDest := filepath.Clean(dest)
			if cleanDest != cleanRunDir && !strings.HasPrefix(cleanDest, cleanRunDir+string(filepath.Separator)) {
				dest = filepath.Join(runDir, filepath.Base(f.Path))
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
				return results, err
			}
			if err := os.Rename(f.Path, dest); err != nil {
				// Cross-device: copy then remove.
				if err2 := copyFile(f.Path, dest); err2 != nil {
					return results, fmt.Errorf("archive %s: %w", f.Path, err2)
				}
				if err2 := os.Remove(f.Path); err2 != nil {
					return results, fmt.Errorf("remove original after cross-device copy %s: %w", f.Path, err2)
				}
			}
			results = append(results, Result{
				Action:   "archived",
				Path:     dest,
				Original: f.Path,
				Hash:     g.Hash,
			})
		}
	}

	// Write manifest.
	manifest := ManifestEntry{RunID: runID, Time: time.Now(), Results: results}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return results, fmt.Errorf("marshal manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "manifest.json"), data, 0644); err != nil {
		return results, fmt.Errorf("write manifest: %w", err)
	}

	return results, nil
}

func commonRoot(groups []scanner.DuplicateGroup) string {
	var paths []string
	for _, g := range groups {
		for _, f := range g.Files {
			paths = append(paths, filepath.Clean(f.Path))
		}
	}
	if len(paths) == 0 {
		return string(filepath.Separator)
	}
	sort.Strings(paths)
	sep := string(filepath.Separator)
	// Compare path components, not raw bytes, to avoid splitting inside a name.
	first := strings.Split(paths[0], sep)
	last := strings.Split(paths[len(paths)-1], sep)
	i := 0
	for i < len(first) && i < len(last) && first[i] == last[i] {
		i++
	}
	if i == 0 {
		return sep
	}
	root := strings.Join(first[:i], sep)
	if root == "" {
		return sep
	}
	return root
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}
