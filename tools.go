package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/egoist/mygo"
)

// toolPath finds a helper program the app plays and signs in with: the copy
// that ships inside the app if there is one, so a download runs as it is,
// and otherwise the one on PATH, which is what a run from source uses.
func toolPath(name string) (string, error) {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if resources, err := mygo.App.Path(mygo.PathResources); err == nil {
		bundled := filepath.Join(resources, "bin", name)
		if info, err := os.Stat(bundled); err == nil && !info.IsDir() {
			return bundled, nil
		}
	}
	return exec.LookPath(name)
}
