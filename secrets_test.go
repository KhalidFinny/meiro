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
	usable    bool
	holds     bool
	value     string
	getErr    error
	setErr    error
	removeErr error
}

func (f *fakeKeychain) available() bool { return f.usable }

func (f *fakeKeychain) set(_ context.Context, value string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.holds, f.value = true, value
	return nil
}

func (f *fakeKeychain) get(context.Context) (string, error) {
	if f.getErr != nil {
		return "", f.getErr
	}
	if !f.holds {
		return "", errSecretNotFound
	}
	return f.value, nil
}

func (f *fakeKeychain) remove(context.Context) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	f.holds, f.value = false, ""
	return nil
}

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
	// A cookie file left by an earlier version is removed even when the
	// system store already has the credential.
	if err := newFileStore(path).Save(ctx, testCookie); err != nil {
		t.Fatal(err)
	}
	cookie, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cookie != testCookie {
		t.Errorf("loaded %q", cookie)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Error("the obsolete file copy remains after loading from the system store")
	}
}

func TestCookieSaveDoesNotFallBackWhenTheSystemStoreFails(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cookie.txt")
	storeErr := errors.New("the keychain is locked")
	system := &fakeKeychain{usable: true, setErr: storeErr}
	store := &keychainStore{system: system, file: newFileStore(path)}

	if err := store.Save(ctx, testCookie); !errors.Is(err, storeErr) {
		t.Fatalf("Save error = %v, want the keychain error", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("failed keychain save wrote a weaker file copy: %v", err)
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

func TestCookieLoadDoesNotFallBackWhenTheSystemStoreWillNotAnswer(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cookie.txt")
	file := newFileStore(path)
	if err := file.Save(ctx, testCookie); err != nil {
		t.Fatal(err)
	}
	// A locked keychain must not silently cause the less-protected file copy
	// to be used instead.
	storeErr := errors.New("the keychain is locked")
	system := &fakeKeychain{usable: true, getErr: storeErr}
	store := &keychainStore{system: system, file: file}

	if _, err := store.Load(ctx); !errors.Is(err, storeErr) {
		t.Fatalf("Load error = %v, want the keychain error", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the untrusted fallback file was unexpectedly removed: %v", err)
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
	ctx := context.Background()
	_ = probe.remove(ctx)
	t.Cleanup(func() { _ = probe.remove(ctx) })

	if _, err := probe.get(ctx); !errors.Is(err, errSecretNotFound) {
		t.Errorf("get of a missing secret = %v", err)
	}
	if err := probe.set(ctx, "hello keychain"); err != nil {
		t.Fatal(err)
	}
	value, err := probe.get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if value != "hello keychain" {
		t.Errorf("get = %q", value)
	}
	if err := probe.set(ctx, "replaced"); err != nil {
		t.Fatal(err)
	}
	if value, _ := probe.get(ctx); value != "replaced" {
		t.Errorf("get after replacing = %q", value)
	}
	if err := probe.remove(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := probe.get(ctx); !errors.Is(err, errSecretNotFound) {
		t.Errorf("get after remove = %v", err)
	}
}
