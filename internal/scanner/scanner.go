package scanner

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// FileInfo holds metadata about a scanned file.
type FileInfo struct {
	Path    string
	Size    int64
	ModTime time.Time
}

// DuplicateGroup is a set of files with identical content.
type DuplicateGroup struct {
	Hash  string
	Size  int64
	Files []FileInfo
}

// Options configures a scan.
type Options struct {
	MinSize       int64
	Workers       int
	IncludeHidden bool
}

// DefaultOptions returns sensible defaults.
func DefaultOptions() Options {
	return Options{
		MinSize: 1 * 1024 * 1024, // 1 MB
		Workers: runtime.NumCPU(),
	}
}

// ProgressFunc is called periodically during scanning.
// pass: 1 (walk), 2 (quick hash), 3 (full hash)
// done/total: progress within the current pass.
type ProgressFunc func(pass int, done, total int)

// Scan walks the given paths and returns duplicate groups.
func Scan(paths []string, opts Options, progress ProgressFunc) ([]DuplicateGroup, error) {
	// Pass 1: walk and group by size.
	bySize, err := walkAndGroup(paths, opts, progress)
	if err != nil {
		return nil, err
	}

	// Collect candidates (size groups with >1 file).
	var candidates []FileInfo
	for _, files := range bySize {
		if len(files) > 1 {
			candidates = append(candidates, files...)
		}
	}

	// Pass 2: quick hash (first 4KB).
	quickGroups, err := hashGroup(candidates, opts.Workers, true, func(done, total int) {
		if progress != nil {
			progress(2, done, total)
		}
	})
	if err != nil {
		return nil, err
	}

	// Collect pass-2 candidates.
	var fullCandidates []FileInfo
	for _, files := range quickGroups {
		if len(files) > 1 {
			fullCandidates = append(fullCandidates, files...)
		}
	}

	// Pass 3: full SHA256.
	fullGroups, err := hashGroup(fullCandidates, opts.Workers, false, func(done, total int) {
		if progress != nil {
			progress(3, done, total)
		}
	})
	if err != nil {
		return nil, err
	}

	var result []DuplicateGroup
	for hash, files := range fullGroups {
		if len(files) > 1 {
			result = append(result, DuplicateGroup{
				Hash:  hash,
				Size:  files[0].Size,
				Files: files,
			})
		}
	}
	return result, nil
}

func walkAndGroup(paths []string, opts Options, progress ProgressFunc) (map[int64][]FileInfo, error) {
	bySize := make(map[int64][]FileInfo)
	seen := make(map[uint64]bool) // inode dedup

	for _, root := range paths {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // skip unreadable
			}
			if d.IsDir() {
				if !opts.IncludeHidden && isHidden(d.Name()) && d.Name() != "." {
					return filepath.SkipDir
				}
				return nil
			}
			if !opts.IncludeHidden && isHidden(d.Name()) {
				return nil
			}

			info, err := d.Info()
			if err != nil {
				return nil
			}
			if info.Size() < opts.MinSize {
				return nil
			}

			// Skip hardlinks we've already seen.
			inode := getInode(info)
			if inode != 0 {
				if seen[inode] {
					return nil
				}
				seen[inode] = true
			}

			fi := FileInfo{
				Path:    path,
				Size:    info.Size(),
				ModTime: info.ModTime(),
			}
			bySize[fi.Size] = append(bySize[fi.Size], fi)

			if progress != nil {
				progress(1, len(bySize), 0)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return bySize, nil
}

type hashJob struct {
	file  FileInfo
	quick bool
}

type hashResult struct {
	file FileInfo
	hash string
	err  error
}

func hashGroup(files []FileInfo, workers int, quick bool, progress ProgressFunc) (map[string][]FileInfo, error) {
	jobs := make(chan hashJob, len(files))
	results := make(chan hashResult, len(files))

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				h, err := hashFile(j.file.Path, j.quick)
				results <- hashResult{file: j.file, hash: h, err: err}
			}
		}()
	}

	for _, f := range files {
		jobs <- hashJob{file: f, quick: quick}
	}
	close(jobs)

	go func() {
		wg.Wait()
		close(results)
	}()

	groups := make(map[string][]FileInfo)
	done := 0
	for r := range results {
		done++
		if progress != nil {
			progress(done, len(files))
		}
		if r.err != nil {
			continue
		}
		groups[r.hash] = append(groups[r.hash], r.file)
	}
	return groups, nil
}

func hashFile(path string, quick bool) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if quick {
		buf := make([]byte, 4096)
		_, err = io.ReadFull(f, buf)
		if err != nil && err != io.ErrUnexpectedEOF {
			return "", err
		}
		h.Write(buf)
	} else {
		if _, err = io.Copy(h, f); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func isHidden(name string) bool {
	return len(name) > 0 && name[0] == '.'
}
