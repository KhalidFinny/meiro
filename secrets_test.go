package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// fakeKeychain is a credential store kept in memory, so the tests never
// write to the machine's own keychain.
type fakeKeychain struct {
	usable bool
	holds  bool
	value  string
	getErr error
	setErr error
}

func (f *fakeKeychain) available() bool { return f.usable }

func (f *fakeKeychain) set(value string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.holds, f.value = true, value
	return nil
}

func (f *fakeKeychain) get() (string, error) {
	if f.getErr != nil {
		return "", f.getErr
	}
	if !f.holds {
		return "", errSecretNotFound
	}
	return f.value, nil
}

func (f *fakeKeychain) remove() { f.holds, f.value = false, "" }

const testCookie = "SAPISID=secret; SID=other"

func TestFileStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cookie.txt")
	store := newFileStore(path)

	if _, err := store.Load(ctx); !errors.Is(err, errNotSignedIn) {
		t.Fatalf("Load of an empty store = %v", err)
	}
	if err := store.Save(ctx, testCookie); err != nil {
		t.Fatal(err)
	}
	cookie, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cookie != testCookie {
		t.Errorf("loaded %q", cookie)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("the token file is %v, want 0600", got)
	}
	if err := store.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(ctx); !errors.Is(err, errNotSignedIn) {
		t.Errorf("Load after Delete = %v", err)
	}
	if err := store.Delete(ctx); err != nil {
		t.Errorf("deleting twice = %v", err)
	}
}

func TestCookieGoToTheSystemStore(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cookie.txt")
	system := &fakeKeychain{usable: true}
	store := &keychainStore{system: system, file: newFileStore(path)}

	if err := store.Save(ctx, testCookie); err != nil {
		t.Fatal(err)
	}
	if !system.holds {
		t.Error("the cookie did not reach the system store")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Error("the cookie were also written to the file")
	}
	cookie, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cookie != testCookie {
		t.Errorf("loaded %q", cookie)
	}
}

func TestCookieFallBackToTheFile(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cookie.txt")
	system := &fakeKeychain{usable: true, setErr: errors.New("the keychain is locked")}
	store := &keychainStore{system: system, file: newFileStore(path)}

	if err := store.Save(ctx, testCookie); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the cookie did not reach the file: %v", err)
	}
	cookie, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cookie != testCookie {
		t.Errorf("loaded %q", cookie)
	}
}

func TestCookieFallBackWithoutAStore(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cookie.txt")
	store := &keychainStore{system: &fakeKeychain{}, file: newFileStore(path)}

	if err := store.Save(ctx, testCookie); err != nil {
		t.Fatal(err)
	}
	cookie, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cookie != testCookie {
		t.Errorf("loaded %q", cookie)
	}
}

func TestCookieFallBackToTheFileWhenTheStoreWillNotAnswer(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cookie.txt")
	file := newFileStore(path)
	if err := file.Save(ctx, testCookie); err != nil {
		t.Fatal(err)
	}
	// A locked keychain refuses the read, which must not sign the user out
	// of the cookie the file still holds.
	system := &fakeKeychain{usable: true, getErr: errors.New("the keychain is locked")}
	store := &keychainStore{system: system, file: file}

	cookie, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cookie != testCookie {
		t.Errorf("loaded %q", cookie)
	}
}

func TestCookieMoveFromTheFileToTheSystemStore(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cookie.txt")
	file := newFileStore(path)
	if err := file.Save(ctx, testCookie); err != nil {
		t.Fatal(err)
	}
	system := &fakeKeychain{usable: true}
	store := &keychainStore{system: system, file: file}

	cookie, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cookie != testCookie {
		t.Errorf("loaded %q", cookie)
	}
	if !system.holds {
		t.Error("the cookie did not move into the system store")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Error("the file is still there after the move")
	}
}

func TestSignOutForgetsTheCookieEverywhere(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cookie.txt")
	file := newFileStore(path)
	system := &fakeKeychain{usable: true}
	store := &keychainStore{system: system, file: file}
	if err := file.Save(ctx, testCookie); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, testCookie); err != nil {
		t.Fatal(err)
	}

	if err := store.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	if system.holds {
		t.Error("the system store still holds the cookie")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Error("the file is still there")
	}
	if _, err := store.Load(ctx); !errors.Is(err, errNotSignedIn) {
		t.Errorf("Load after Delete = %v", err)
	}
}

// TestLiveKeychain uses the machine's own credential store. It needs one
// that is unlocked:
//
//	MEIRO_LIVE_KEYCHAIN=1 go test -run TestLiveKeychain -v .
func TestLiveKeychain(t *testing.T) {
	if os.Getenv("MEIRO_LIVE_KEYCHAIN") == "" {
		t.Skip("set MEIRO_LIVE_KEYCHAIN=1 to use the system credential store")
	}
	probe := secret{service: "com.elianiva.meiro.test", account: "probe"}
	if !probe.available() {
		t.Skip("this system has no credential store command")
	}
	probe.remove()
	t.Cleanup(probe.remove)

	if _, err := probe.get(); !errors.Is(err, errSecretNotFound) {
		t.Errorf("get of a missing secret = %v", err)
	}
	if err := probe.set("hello keychain"); err != nil {
		t.Fatal(err)
	}
	value, err := probe.get()
	if err != nil {
		t.Fatal(err)
	}
	if value != "hello keychain" {
		t.Errorf("get = %q", value)
	}
	if err := probe.set("replaced"); err != nil {
		t.Fatal(err)
	}
	if value, _ := probe.get(); value != "replaced" {
		t.Errorf("get after replacing = %q", value)
	}
	probe.remove()
	if _, err := probe.get(); !errors.Is(err, errSecretNotFound) {
		t.Errorf("get after remove = %v", err)
	}
}
