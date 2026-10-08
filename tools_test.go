package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo"
)

func TestToolPathPrefersTheBundledCopy(t *testing.T) {
	resources := t.TempDir()
	mygo.App.SetPath(mygo.PathResources, resources)
	t.Cleanup(func() { mygo.App.SetPath(mygo.PathResources, "") })

	if err := os.MkdirAll(filepath.Join(resources, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	bundled := filepath.Join(resources, "bin", "meiro-test-tool")
	if err := os.WriteFile(bundled, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := toolPath("meiro-test-tool")
	if err != nil || got != bundled {
		t.Fatalf("toolPath = %q, %v; want %q", got, err, bundled)
	}
}

func TestToolPathFallsBackToPATH(t *testing.T) {
	mygo.App.SetPath(mygo.PathResources, t.TempDir())
	t.Cleanup(func() { mygo.App.SetPath(mygo.PathResources, "") })

	path := t.TempDir()
	onPath := filepath.Join(path, "meiro-test-tool")
	if err := os.WriteFile(onPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", path)
	got, err := toolPath("meiro-test-tool")
	if err != nil || got != onPath {
		t.Fatalf("toolPath = %q, %v; want %q", got, err, onPath)
	}
	if _, err := toolPath("meiro-no-such-tool"); err == nil {
		t.Fatal("toolPath found a program that does not exist")
	}
}
