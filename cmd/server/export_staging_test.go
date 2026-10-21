package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExportStartupScavengesOnlyDedicatedRegularFiles(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "exports")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(directory, "si-account-export-12345")
	unrelated := filepath.Join(directory, "keep.txt")
	outside := filepath.Join(root, "outside")
	for _, p := range []string{orphan, unrelated, outside} {
		if err := os.WriteFile(p, []byte("preserve unless owned export"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(directory, "si-account-export-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(directory, "si-account-export-directory"), 0700); err != nil {
		t.Fatal(err)
	}
	got, err := prepareExportDirectory(root)
	if err != nil || got != directory {
		t.Fatal(got, err)
	}
	if _, err = os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("crash orphan remains", err)
	}
	for _, p := range []string{unrelated, outside, filepath.Join(directory, "si-account-export-link"), filepath.Join(directory, "si-account-export-directory")} {
		if _, err = os.Lstat(p); err != nil {
			t.Fatal("unrelated file modified", p, err)
		}
	}
	info, err := os.Stat(directory)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("export directory not private", info, err)
	}
	// A second startup is idempotent.
	if _, err = prepareExportDirectory(root); err != nil {
		t.Fatal(err)
	}
}
func TestExportStartupRejectsSymlinkDirectory(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	protected := filepath.Join(outside, "si-account-export-12345")
	if err := os.WriteFile(protected, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "exports")); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareExportDirectory(root); err == nil {
		t.Fatal("followed export directory symlink")
	}
	if _, err := os.Stat(protected); err != nil {
		t.Fatal("deleted external data", err)
	}
}
