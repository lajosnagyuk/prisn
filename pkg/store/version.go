package store

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DeploymentVersion represents a versioned snapshot of deployment source.
type DeploymentVersion struct {
	ID           string    `json:"id"`           // e.g., "v-a1b2c3d4"
	DeploymentID string    `json:"deployment_id"`
	Hash         string    `json:"hash"`         // xxhash64 fingerprint
	SourcePath   string    `json:"source_path"`  // Original path
	StoragePath  string    `json:"storage_path"` // Where it's stored
	Size         int64     `json:"size"`
	FileCount    int       `json:"file_count"`
	CreatedAt    time.Time `json:"created_at"`
	Active       bool      `json:"active"`       // Is this the current version?
}

// VersionManager handles deployment versioning.
type VersionManager struct {
	BaseDir string // e.g., ~/.prisn/deployments
}

// NewVersionManager creates a version manager.
func NewVersionManager(baseDir string) (*VersionManager, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create version directory: %w", err)
	}
	return &VersionManager{BaseDir: baseDir}, nil
}

// Package creates a new version from a source path (file or directory).
func (vm *VersionManager) Package(deploymentName, sourcePath string) (*DeploymentVersion, error) {
	absPath, err := filepath.Abs(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("invalid source path: %w", err)
	}

	_, err = os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("source not found: %w", err)
	}

	// Calculate fingerprint using xxhash64 (fast!)
	fp, err := FingerprintPath(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to fingerprint contents: %w", err)
	}

	// Version ID from fingerprint
	versionID := fp.ShortID()

	// Storage path
	storagePath := filepath.Join(vm.BaseDir, deploymentName, versionID)

	// Check if this version already exists
	if _, err := os.Stat(storagePath); err == nil {
		// Already exists, return existing
		return &DeploymentVersion{
			ID:          versionID,
			Hash:        fp.String(),
			SourcePath:  absPath,
			StoragePath: storagePath,
			Size:        fp.TotalSize,
			FileCount:   fp.FileCount,
			CreatedAt:   time.Now(),
		}, nil
	}

	// Create version directory
	if err := os.MkdirAll(storagePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create version directory: %w", err)
	}

	// Copy contents - check if source is directory
	srcInfo, _ := os.Stat(absPath)
	if srcInfo.IsDir() {
		if err := vm.copyDir(absPath, storagePath); err != nil {
			os.RemoveAll(storagePath)
			return nil, fmt.Errorf("failed to copy directory: %w", err)
		}
	} else {
		destFile := filepath.Join(storagePath, filepath.Base(absPath))
		if err := vm.copyFile(absPath, destFile); err != nil {
			os.RemoveAll(storagePath)
			return nil, fmt.Errorf("failed to copy file: %w", err)
		}
	}

	return &DeploymentVersion{
		ID:          versionID,
		Hash:        fp.String(),
		SourcePath:  absPath,
		StoragePath: storagePath,
		Size:        fp.TotalSize,
		FileCount:   fp.FileCount,
		CreatedAt:   time.Now(),
	}, nil
}

// copyDir recursively copies a directory.
func (vm *VersionManager) copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip hidden and cache directories
		base := filepath.Base(path)
		if strings.HasPrefix(base, ".") || base == "__pycache__" || base == "node_modules" || base == ".venv" {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, _ := filepath.Rel(src, path)
		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		return vm.copyFile(path, dstPath)
	})
}

// copyFile copies a single file.
func (vm *VersionManager) copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}

	srcInfo, _ := srcFile.Stat()
	return os.Chmod(dst, srcInfo.Mode())
}

// ListVersions lists all versions for a deployment.
func (vm *VersionManager) ListVersions(deploymentName string) ([]*DeploymentVersion, error) {
	deployDir := filepath.Join(vm.BaseDir, deploymentName)
	entries, err := os.ReadDir(deployDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var versions []*DeploymentVersion
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "v-") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		versions = append(versions, &DeploymentVersion{
			ID:          entry.Name(),
			StoragePath: filepath.Join(deployDir, entry.Name()),
			CreatedAt:   info.ModTime(),
		})
	}

	// Sort by creation time, newest first
	sort.Slice(versions, func(i, j int) bool {
		return versions[i].CreatedAt.After(versions[j].CreatedAt)
	})

	return versions, nil
}

// CreateArchive creates a .tar.gz archive of a version.
func (vm *VersionManager) CreateArchive(version *DeploymentVersion, dest string) error {
	file, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer file.Close()

	gzw := gzip.NewWriter(file)
	defer gzw.Close()

	tw := tar.NewWriter(gzw)
	defer tw.Close()

	return filepath.Walk(version.StoragePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, _ := filepath.Rel(version.StoragePath, path)
		if relPath == "." {
			return nil
		}

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

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		_, err = io.Copy(tw, f)
		return err
	})
}

// Cleanup removes old versions, keeping the N most recent.
func (vm *VersionManager) Cleanup(deploymentName string, keepVersions int) (int, error) {
	versions, err := vm.ListVersions(deploymentName)
	if err != nil {
		return 0, err
	}

	if len(versions) <= keepVersions {
		return 0, nil
	}

	removed := 0
	for i := keepVersions; i < len(versions); i++ {
		if err := os.RemoveAll(versions[i].StoragePath); err == nil {
			removed++
		}
	}

	return removed, nil
}
