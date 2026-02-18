package manifest

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Bundle represents a packaged deployment ready to run.
type Bundle struct {
	// Identity
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Namespace   string    `json:"namespace"`
	Version     string    `json:"version"`
	Fingerprint string    `json:"fingerprint"`
	CreatedAt   time.Time `json:"created_at"`

	// Manifest
	Manifest *Prisnfile `json:"manifest"`

	// Paths
	SourceDir   string `json:"source_dir"`
	BundlePath  string `json:"bundle_path"`  // .prisn bundle file
	ExtractPath string `json:"extract_path"` // Where it's extracted

	// Stats
	FileCount  int   `json:"file_count"`
	TotalBytes int64 `json:"total_bytes"`
}

// BundleOptions controls bundle creation.
type BundleOptions struct {
	// What to include
	IncludeGit    bool     // Include .git directory
	IncludeHidden bool     // Include hidden files
	ExcludeGlobs  []string // Glob patterns to exclude

	// Where to put it
	OutputDir string // Output directory for bundle

	// Compression
	Compress bool // gzip the bundle
}

// DefaultBundleOptions returns sensible defaults.
func DefaultBundleOptions() BundleOptions {
	return BundleOptions{
		IncludeGit:    false,
		IncludeHidden: false,
		ExcludeGlobs: []string{
			"__pycache__",
			"*.pyc",
			".pytest_cache",
			"node_modules",
			".venv",
			"venv",
			".env.local",
			".DS_Store",
			"*.log",
			"*.tmp",
		},
		Compress: true,
	}
}

// CreateBundle packages a directory into a deployable bundle.
func CreateBundle(pf *Prisnfile, opts BundleOptions) (*Bundle, error) {
	if err := pf.Validate(); err != nil {
		return nil, fmt.Errorf("invalid manifest: %w", err)
	}

	bundle := &Bundle{
		Name:      pf.Name,
		Namespace: pf.Namespace,
		Manifest:  pf,
		SourceDir: pf.baseDir,
		CreatedAt: time.Now(),
	}

	// Calculate fingerprint from files
	fingerprint, fileCount, totalBytes, err := calculateFingerprint(pf.baseDir, opts)
	if err != nil {
		return nil, fmt.Errorf("fingerprint failed: %w", err)
	}
	bundle.Fingerprint = fingerprint
	bundle.FileCount = fileCount
	bundle.TotalBytes = totalBytes
	bundle.Version = "v-" + fingerprint[:8]
	bundle.ID = fmt.Sprintf("bundle-%s-%s", pf.Name, bundle.Version)

	// Determine output path
	if opts.OutputDir == "" {
		home, _ := os.UserHomeDir()
		opts.OutputDir = filepath.Join(home, ".prisn", "bundles", pf.Name)
	}

	if err := os.MkdirAll(opts.OutputDir, 0755); err != nil {
		return nil, fmt.Errorf("cannot create output dir: %w", err)
	}

	// Create bundle file
	ext := ".tar"
	if opts.Compress {
		ext = ".tar.gz"
	}
	bundle.BundlePath = filepath.Join(opts.OutputDir, bundle.Version+ext)

	// Check if bundle already exists (same fingerprint)
	if _, err := os.Stat(bundle.BundlePath); err == nil {
		// Bundle exists, reuse it
		return bundle, nil
	}

	// Create the archive
	if err := createArchive(pf.baseDir, bundle.BundlePath, opts); err != nil {
		return nil, fmt.Errorf("archive failed: %w", err)
	}

	return bundle, nil
}

// calculateFingerprint computes a hash of all included files.
func calculateFingerprint(dir string, opts BundleOptions) (string, int, int64, error) {
	hasher := sha256.New()
	var fileCount int
	var totalBytes int64

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, _ := filepath.Rel(dir, path)

		// Skip excluded files
		if shouldExclude(relPath, info, opts) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			return nil
		}

		// Hash file path and content
		hasher.Write([]byte(relPath))

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		n, err := io.Copy(hasher, f)
		if err != nil {
			return err
		}

		fileCount++
		totalBytes += n

		return nil
	})

	if err != nil {
		return "", 0, 0, err
	}

	return hex.EncodeToString(hasher.Sum(nil)), fileCount, totalBytes, nil
}

// shouldExclude checks if a file should be excluded from the bundle.
func shouldExclude(relPath string, info os.FileInfo, opts BundleOptions) bool {
	name := info.Name()

	// Hidden files
	if !opts.IncludeHidden && strings.HasPrefix(name, ".") {
		// Exception: keep .env (but not .env.local)
		if name == ".env" {
			return false
		}
		return true
	}

	// Git directory
	if !opts.IncludeGit && name == ".git" {
		return true
	}

	// Check glob patterns
	for _, pattern := range opts.ExcludeGlobs {
		if matched, _ := filepath.Match(pattern, name); matched {
			return true
		}
		// Also check full relative path
		if matched, _ := filepath.Match(pattern, relPath); matched {
			return true
		}
	}

	return false
}

// createArchive creates a tar (or tar.gz) archive.
func createArchive(srcDir, destPath string, opts BundleOptions) error {
	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	var tw *tar.Writer

	if opts.Compress {
		gw := gzip.NewWriter(out)
		defer gw.Close()
		tw = tar.NewWriter(gw)
	} else {
		tw = tar.NewWriter(out)
	}
	defer tw.Close()

	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, _ := filepath.Rel(srcDir, path)
		if relPath == "." {
			return nil
		}

		// Skip excluded files
		if shouldExclude(relPath, info, opts) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Create tar header
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = relPath

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		// Write file content
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		_, err = io.Copy(tw, f)
		return err
	})
}

// Extract extracts a bundle to a directory.
func (b *Bundle) Extract(destDir string) error {
	if destDir == "" {
		home, _ := os.UserHomeDir()
		destDir = filepath.Join(home, ".prisn", "deployments", b.Name, b.Version)
	}

	b.ExtractPath = destDir

	// Check if already extracted
	if _, err := os.Stat(destDir); err == nil {
		return nil // Already extracted
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("cannot create dest dir: %w", err)
	}

	f, err := os.Open(b.BundlePath)
	if err != nil {
		return fmt.Errorf("cannot open bundle: %w", err)
	}
	defer f.Close()

	var tr *tar.Reader

	// Check if compressed
	if strings.HasSuffix(b.BundlePath, ".gz") {
		gr, err := gzip.NewReader(f)
		if err != nil {
			return fmt.Errorf("invalid gzip: %w", err)
		}
		defer gr.Close()
		tr = tar.NewReader(gr)
	} else {
		tr = tar.NewReader(f)
	}

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read error: %w", err)
		}

		target := filepath.Join(destDir, header.Name)

		// Security: prevent path traversal
		if !strings.HasPrefix(target, destDir) {
			return fmt.Errorf("invalid path in archive: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			out, err := os.Create(target)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()

			// Preserve executable bit
			if header.Mode&0111 != 0 {
				os.Chmod(target, 0755)
			}
		}
	}

	return nil
}

// Summary returns a human-readable summary of the bundle.
func (b *Bundle) Summary() string {
	var s strings.Builder

	fmt.Fprintf(&s, "Bundle: %s\n", b.Name)
	fmt.Fprintf(&s, "  Version:     %s\n", b.Version)
	fmt.Fprintf(&s, "  Type:        %s\n", b.Manifest.Type)
	fmt.Fprintf(&s, "  Entrypoint:  %s\n", b.Manifest.Entrypoint)
	fmt.Fprintf(&s, "  Runtime:     %s\n", b.Manifest.Runtime)
	fmt.Fprintf(&s, "  Files:       %d (%s)\n", b.FileCount, formatBytes(b.TotalBytes))
	fmt.Fprintf(&s, "  Fingerprint: %s\n", b.Fingerprint[:16]+"...")

	return s.String()
}

func formatBytes(b int64) string {
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
