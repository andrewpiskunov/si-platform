package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Called only after acquiring exclusive engine ownership, before HTTP serving.
// No earlier process can still have an authorized export in this data directory.
func prepareExportDirectory(dataDir string) (string, error) {
	directory, err := filepath.Abs(filepath.Join(dataDir, "exports"))
	if err != nil {
		return "", errors.New("invalid export staging directory")
	}
	if err = os.MkdirAll(directory, 0700); err != nil {
		return "", errors.New("export staging directory unavailable")
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("export staging directory must be a private directory")
	}
	if err = os.Chmod(directory, 0700); err != nil {
		return "", errors.New("export staging permissions unavailable")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", errors.New("export staging directory unavailable")
	}
	defer root.Close()
	entries, err := root.Open(".")
	if err != nil {
		return "", errors.New("export staging directory unavailable")
	}
	defer entries.Close()
	for {
		batch, readErr := entries.ReadDir(100)
		for _, entry := range batch {
			if !entry.Type().IsRegular() || !strings.HasPrefix(entry.Name(), "si-account-export-") {
				continue
			}
			if err = root.Remove(entry.Name()); err != nil && !errors.Is(err, os.ErrNotExist) {
				return "", errors.New("export staging cleanup failed")
			}
		}
		if errors.Is(readErr, io.EOF) {
			return directory, nil
		}
		if readErr != nil {
			return "", errors.New("export staging cleanup failed")
		}
	}
}
