package main

import (
	"log"
	"os"
	"path/filepath"
)

// writeFileAtomic writes data to path so that a crash leaves either the old
// file or the new one, never half of one: it goes to a temporary file in the
// same directory, is synced, and replaces the old file by a rename. The
// directory must exist.
func writeFileAtomic(path string, data []byte, perm os.FileMode) (err error) {
	file, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(file.Name())
		}
	}()
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Chmod(perm); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// removeAll deletes a directory the app made for a credential, and says so
// when it cannot: a copy of someone's session left in a temporary directory
// is worth knowing about.
func removeAll(directory string) {
	if err := os.RemoveAll(directory); err != nil {
		log.Printf("removing %s: %v", directory, err)
	}
}
