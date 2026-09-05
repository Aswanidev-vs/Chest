package filesystem

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Exists checks if path exists
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil || !os.IsNotExist(err)
}

// IsDirectory checks if path is a directory
func IsDirectory(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// EnsureDir creates directory and parents if not exist
func EnsureDir(dir string) error {
	if dir == "" || dir == "." {
		return nil
	}
	return os.MkdirAll(dir, 0755)
}

// MoveFile safely moves a file from src to dst.
// First tries os.Rename. If cross-device error occurs, copies then deletes source.
func MoveFile(src, dst string) error {
	if src == dst {
		return nil
	}

	dstDir := filepath.Dir(dst)
	if err := EnsureDir(dstDir); err != nil {
		return fmt.Errorf("failed to create destination folder %s: %w", dstDir, err)
	}

	// Try atomic rename
	err := os.Rename(src, dst)
	if err == nil {
		return nil
	}

	// Fallback to copy + remove for cross-device moves
	if err := copyFile(src, dst); err != nil {
		return fmt.Errorf("failed to copy file %s to %s: %w", src, dst, err)
	}

	if err := os.Remove(src); err != nil {
		// remove dst to avoid duplicate if src cannot be deleted
		os.Remove(dst)
		return fmt.Errorf("failed to clean up source file %s: %w", src, err)
	}

	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}

	return out.Sync()
}

// ResolveCollision generates a non-conflicting filename by appending (1), (2), etc.
func ResolveCollision(dst string) string {
	if !Exists(dst) {
		return dst
	}

	dir := filepath.Dir(dst)
	base := filepath.Base(dst)
	ext := filepath.Ext(base)
	nameWithoutExt := strings.TrimSuffix(base, ext)

	counter := 1
	for {
		candidateName := fmt.Sprintf("%s (%d)%s", nameWithoutExt, counter, ext)
		candidatePath := filepath.Join(dir, candidateName)
		if !Exists(candidatePath) {
			return candidatePath
		}
		counter++
	}
}
