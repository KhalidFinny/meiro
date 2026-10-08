package main

import (
	"os"
	"strings"
	"testing"
)

func TestYtDlpCookieFileUsesPrivateNetscapeFormat(t *testing.T) {
	path, err := ytDlpCookieFile("SID=session; SAPISID=secret=value")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("cookie file permissions = %04o, want 0600", got)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# Netscape HTTP Cookie File",
		".youtube.com\tTRUE\t/\tTRUE\t0\tSID\tsession",
		".youtube.com\tTRUE\t/\tTRUE\t0\tSAPISID\tsecret=value",
	} {
		if !strings.Contains(string(contents), want) {
			t.Errorf("cookie file does not contain %q: %s", want, contents)
		}
	}
}

func TestYtDlpCookieFileRejectsEmptyCookieHeader(t *testing.T) {
	if path, err := ytDlpCookieFile("; = ;"); err == nil {
		_ = os.Remove(path)
		t.Fatal("cookie file was created without any valid cookies")
	}
}
