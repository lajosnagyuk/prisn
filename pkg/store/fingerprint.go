// Package store provides fingerprinting utilities using xxhash64.
// xxhash64 is ~25x faster than SHA256 and sufficient for change detection.
package store

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cespare/xxhash/v2"
)

// Fingerprint represents a content fingerprint.
type Fingerprint struct {
	Hash      uint64 // xxhash64
	FileCount int
	TotalSize int64
}

// String returns the fingerprint as a string: <hash>-<files>-<size>
func (f Fingerprint) String() string {
	return fmt.Sprintf("%016x-%d-%d", f.Hash, f.FileCount, f.TotalSize)
}

// ShortID returns a short version ID suitable for display.
func (f Fingerprint) ShortID() string {
	return fmt.Sprintf("v-%08x", f.Hash>>32) // First 8 hex chars
}

// FingerprintFile calculates the fingerprint of a single file.
func FingerprintFile(path string) (Fingerprint, error) {
	file, err := os.Open(path)
	if err != nil {
		return Fingerprint{}, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return Fingerprint{}, err
	}

	h := xxhash.New()
	if _, err := io.Copy(h, file); err != nil {
		return Fingerprint{}, err
	}

	return Fingerprint{
		Hash:      h.Sum64(),
		FileCount: 1,
		TotalSize: info.Size(),
	}, nil
}

// FingerprintDir calculates the fingerprint of a directory.
// Files are processed in sorted order for deterministic results.
// Hidden files, __pycache__, node_modules, etc. are skipped.
func FingerprintDir(dir string) (Fingerprint, error) {
	var files []string
	var totalSize int64

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}

		base := filepath.Base(path)

		// Skip hidden and common non-source directories
		if info.IsDir() {
			if shouldSkipDir(base) {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip hidden and non-source files
		if shouldSkipFile(base) {
			return nil
		}

		files = append(files, path)
		totalSize += info.Size()
		return nil
	})

	if err != nil {
		return Fingerprint{}, err
	}

	// Sort for deterministic hashing
	sort.Strings(files)

	h := xxhash.New()
	for _, f := range files {
		// Include relative path in hash for structure sensitivity
		relPath, _ := filepath.Rel(dir, f)
		h.Write([]byte(relPath))
		h.Write([]byte{0}) // Separator

		file, err := os.Open(f)
		if err != nil {
			continue
		}
		io.Copy(h, file)
		file.Close()
	}

	return Fingerprint{
		Hash:      h.Sum64(),
		FileCount: len(files),
		TotalSize: totalSize,
	}, nil
}

// FingerprintPath calculates the fingerprint of a file or directory.
func FingerprintPath(path string) (Fingerprint, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Fingerprint{}, err
	}

	if info.IsDir() {
		return FingerprintDir(path)
	}
	return FingerprintFile(path)
}

// shouldSkipDir returns true if directory should be excluded from fingerprinting.
func shouldSkipDir(name string) bool {
	skip := []string{
		".", "..",
		".git", ".svn", ".hg",
		"__pycache__", ".pytest_cache",
		"node_modules",
		".venv", "venv", "env",
		".tox", ".nox",
		"dist", "build",
		".idea", ".vscode",
		"target", // Rust, Java
	}

	if strings.HasPrefix(name, ".") {
		return true
	}

	for _, s := range skip {
		if name == s {
			return true
		}
	}
	return false
}

// shouldSkipFile returns true if file should be excluded from fingerprinting.
func shouldSkipFile(name string) bool {
	// Hidden files
	if strings.HasPrefix(name, ".") {
		return true
	}

	// Common non-source files
	skip := []string{
		".pyc", ".pyo", ".pyd",
		".so", ".dylib", ".dll",
		".o", ".a",
		".class",
		".log",
		".DS_Store", "Thumbs.db",
	}

	for _, suffix := range skip {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}

	return false
}

// QuickFingerprint does a fast fingerprint using only file metadata.
// Useful for quick change detection before doing full content hash.
func QuickFingerprint(path string) (uint64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}

	h := xxhash.New()

	if !info.IsDir() {
		// For files, hash name + size + mtime
		fmt.Fprintf(h, "%s|%d|%d", info.Name(), info.Size(), info.ModTime().UnixNano())
		return h.Sum64(), nil
	}

	// For directories, walk and hash metadata
	filepath.Walk(path, func(p string, i os.FileInfo, err error) error {
		if err != nil || i.IsDir() {
			return nil
		}

		base := filepath.Base(p)
		if shouldSkipFile(base) {
			return nil
		}

		relPath, _ := filepath.Rel(path, p)
		fmt.Fprintf(h, "%s|%d|%d\n", relPath, i.Size(), i.ModTime().UnixNano())
		return nil
	})

	return h.Sum64(), nil
}
