package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// encryptChromium is decryptChromium's inverse, for the tests.
func encryptChromium(key []byte, plain, host string, version int) []byte {
	data := []byte(plain)
	if version >= 24 {
		sum := sha256.Sum256([]byte(host))
		data = append(sum[:], data...)
	}
	pad := aes.BlockSize - len(data)%aes.BlockSize
	data = append(data, bytes.Repeat([]byte{byte(pad)}, pad)...)
	block, _ := aes.NewCipher(key)
	out := make([]byte, len(data))
	cipher.NewCBCEncrypter(block, bytes.Repeat([]byte{' '}, aes.BlockSize)).CryptBlocks(out, data)
	return append([]byte("v10"), out...)
}

func TestDecryptChromium(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 16)
	for _, version := range []int{20, 24} {
		encrypted := encryptChromium(key, "secret-value", ".youtube.com", version)
		got, ok := decryptChromium(key, encrypted, ".youtube.com", version)
		if !ok || got != "secret-value" {
			t.Errorf("version %d: decrypted %q, %v", version, got, ok)
		}
	}
	encrypted := encryptChromium(key, "secret-value", ".youtube.com", 24)
	if _, ok := decryptChromium(key, encrypted, ".other.com", 24); ok {
		t.Error("a value of another host decrypted")
	}
	if _, ok := decryptChromium(bytes.Repeat([]byte{8}, 16), encrypted, ".youtube.com", 24); ok {
		t.Error("a value decrypted with the wrong key")
	}
	if _, ok := decryptChromium(key, []byte("plain"), ".youtube.com", 24); ok {
		t.Error("an unencrypted value decrypted")
	}
}

func TestChromiumCookieReadsADatabase(t *testing.T) {
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("no sqlite3")
	}
	// The password a browser keeps in the keychain, and the key Chromium
	// derives from it.
	key, err := deriveChromiumKey("password")
	if err != nil {
		t.Fatal(err)
	}
	profile := t.TempDir()
	database := filepath.Join(profile, "Cookies")
	hexed := func(plain, host string) string {
		return hex.EncodeToString(encryptChromium(key, plain, host, 24))
	}
	script := `
CREATE TABLE meta (key TEXT, value TEXT);
INSERT INTO meta VALUES ('version', '24');
CREATE TABLE cookies (host_key TEXT, name TEXT, value TEXT, encrypted_value BLOB, expires_utc INTEGER);
INSERT INTO cookies VALUES ('.youtube.com', 'SAPISID', '', X'` + hexed("abc", ".youtube.com") + `', 0);
INSERT INTO cookies VALUES ('.youtube.com', 'PREF', 'plain', X'', 0);
INSERT INTO cookies VALUES ('.youtube.com', 'FUTURE', 'kept', X'', 15000000000000000);
INSERT INTO cookies VALUES ('.youtube.com', 'EXPIRED', 'gone', X'', 1);
INSERT INTO cookies VALUES ('.google.com', 'SID', 'other', X'', 0);
`
	if out, err := exec.Command(sqlite, database, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v: %s", err, out)
	}
	got, err := readChromiumCookies(context.Background(), profile, key)
	if err != nil {
		t.Fatal(err)
	}
	if want := "SAPISID=abc; PREF=plain; FUTURE=kept"; got != want {
		t.Errorf("cookies = %q, want %q", got, want)
	}
	if _, err := os.Stat(database + "-journal"); err == nil {
		t.Error("the read left a journal in the browser's profile")
	}
}
