package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/egoist/mygo"
)

// Where the sign-in lives in the system's credential store.
const (
	secretService = "com.elianiva.meiro"
	secretAccount = "cookie"
	// legacyAccount held the OAuth tokens of earlier versions.
	legacyAccount = "oauth"
)

var (
	// errNotSignedIn reports that no cookie is saved.
	errNotSignedIn = errors.New("not signed in")
	// errSecretNotFound reports that the system store holds no such secret.
	errSecretNotFound = errors.New("secret not found")
	// errNoCredentialStore reports that the system has no credential store
	// the app can reach, so the cookie stays in a file.
	errNoCredentialStore = errors.New("no system credential store")
)

// newCookieStore returns the store the app keeps its sign-in in: the
// operating system's credential store, with a file only its user can read
// as the fallback. It also forgets what earlier versions kept there, the
// OAuth tokens, which nothing reads any more.
func newCookieStore() (*keychainStore, error) {
	directory, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return nil, err
	}
	secret{service: secretService, account: legacyAccount}.remove()
	_ = os.Remove(filepath.Join(directory, "auth.json"))
	system := secret{service: secretService, account: secretAccount}
	return &keychainStore{system: system, file: newFileStore(filepath.Join(directory, "cookie.txt"))}, nil
}

// credentialStore is one secret of the operating system's credential store.
type credentialStore interface {
	// available reports whether the platform has a store to use.
	available() bool
	set(value string) error
	get() (string, error)
	remove()
}

// secret is a credential in the store the platform provides: the login
// keychain on macOS, reached through the security tool, and the Secret
// Service on Linux, reached through secret-tool. Other systems have neither
// command, and the app keeps the cookie in a file there.
type secret struct {
	service string
	account string
}

func (s secret) available() bool { return s.program() != "" }

// program returns the command the platform's store is reached through, and
// the empty string when it has none.
func (s secret) program() string {
	switch runtime.GOOS {
	case "darwin", "linux":
	default:
		return ""
	}
	name := "secret-tool"
	if runtime.GOOS == "darwin" {
		name = "security"
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return path
}

func (s secret) set(value string) error {
	program := s.program()
	if program == "" {
		return errNoCredentialStore
	}
	var command *exec.Cmd
	if runtime.GOOS == "darwin" {
		// The value is an argument because the tool reads it from a
		// terminal otherwise, and the app has none. It is briefly visible
		// in the process list, as it is with every tool that does this.
		command = exec.Command(program, "add-generic-password", "-U", "-s", s.service, "-a", s.account, "-w", value)
	} else {
		command = exec.Command(program, "store", "--label="+s.service, "service", s.service, "account", s.account)
		command.Stdin = strings.NewReader(value)
	}
	if err := command.Run(); err != nil {
		return fmt.Errorf("save the sign-in in the system keychain: %w", err)
	}
	return nil
}

func (s secret) get() (string, error) {
	program := s.program()
	if program == "" {
		return "", errNoCredentialStore
	}
	var command *exec.Cmd
	if runtime.GOOS == "darwin" {
		command = exec.Command(program, "find-generic-password", "-s", s.service, "-a", s.account, "-w")
	} else {
		command = exec.Command(program, "lookup", "service", s.service, "account", s.account)
	}
	output, err := command.Output()
	if err != nil {
		if missingSecret(err) {
			return "", errSecretNotFound
		}
		return "", fmt.Errorf("read the sign-in from the system keychain: %w", err)
	}
	value := strings.TrimRight(string(output), "\n")
	if value == "" {
		return "", errSecretNotFound
	}
	return value, nil
}

// remove forgets the secret. A secret that is not there is not a failure.
func (s secret) remove() {
	program := s.program()
	if program == "" {
		return
	}
	if runtime.GOOS == "darwin" {
		_ = exec.Command(program, "delete-generic-password", "-s", s.service, "-a", s.account).Run()
		return
	}
	_ = exec.Command(program, "clear", "service", s.service, "account", s.account).Run()
}

// missingSecret reports whether a store command failed because it holds no
// such secret: the security tool exits 44, errSecItemNotFound, and
// secret-tool exits 1.
func missingSecret(err error) bool {
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return false
	}
	return exit.ExitCode() == 44 || exit.ExitCode() == 1
}

// cookieStore keeps the sign-in between runs.
type cookieStore interface {
	Load(ctx context.Context) (string, error)
	Save(ctx context.Context, cookie string) error
	Delete(ctx context.Context) error
}

// keychainStore keeps the sign-in, the Cookie header of the account's
// browser session, in the system's credential store, and in a file only its
// user can read when the system has no store or the store refuses it.
type keychainStore struct {
	system credentialStore
	file   *fileStore
}

func (s *keychainStore) Load(ctx context.Context) (string, error) {
	if s.system.available() {
		if value, err := s.system.get(); err == nil {
			return value, nil
		}
	}
	// The system store holds nothing, or would not answer, as a locked
	// keychain will not: a run whose store refused the cookie may have left
	// it in the file. Finding it there moves it into the store.
	cookie, err := s.file.Load(ctx)
	if err != nil {
		return "", err
	}
	if s.system.available() {
		if err := s.system.set(cookie); err == nil {
			_ = s.file.Delete(ctx)
		}
	}
	return cookie, nil
}

func (s *keychainStore) Save(ctx context.Context, cookie string) error {
	if s.system.available() {
		if err := s.system.set(cookie); err == nil {
			// The cookie does not belong in the file once the system
			// store has it.
			_ = s.file.Delete(ctx)
			return nil
		}
	}
	return s.file.Save(ctx, cookie)
}

func (s *keychainStore) Delete(ctx context.Context) error {
	if s.system.available() {
		s.system.remove()
	}
	return s.file.Delete(ctx)
}

// fileStore keeps the cookie in a file only its user can read.
type fileStore struct {
	path string
}

func newFileStore(path string) *fileStore { return &fileStore{path: path} }

func (s *fileStore) Load(context.Context) (string, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return "", errNotSignedIn
	}
	if err != nil {
		return "", err
	}
	cookie := strings.TrimSpace(string(data))
	if cookie == "" {
		return "", errNotSignedIn
	}
	return cookie, nil
}

func (s *fileStore) Save(_ context.Context, cookie string) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	// The cookie is a credential: keep it readable by this user only.
	return os.WriteFile(s.path, []byte(cookie), 0o600)
}

func (s *fileStore) Delete(context.Context) error {
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
