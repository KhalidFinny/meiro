package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/egoist/mygo"

	"github.com/elianiva/meiro/youtube"
)

// Where the sign-in lives in the system's credential store.
const (
	secretService = "com.elianiva.meiro"
	secretAccount = "oauth"
)

var (
	// errSecretNotFound reports that the system store holds no such secret.
	errSecretNotFound = errors.New("secret not found")
	// errNoCredentialStore reports that the system has no credential store
	// the app can reach, so the tokens stay in a file.
	errNoCredentialStore = errors.New("no system credential store")
)

// newTokenStore returns the store the app keeps its sign-in in: the
// operating system's credential store, with a file only its user can read
// as the fallback.
func newTokenStore() (youtube.TokenStore, error) {
	directory, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return nil, err
	}
	system := secret{service: secretService, account: secretAccount}
	return &keychainStore{system: system, file: newFileStore(filepath.Join(directory, "auth.json"))}, nil
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
// command, and the app keeps the tokens in a file there.
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

// keychainStore keeps the OAuth tokens in the system's credential store,
// and in a file only their user can read when the system has no store or
// the store refuses them.
type keychainStore struct {
	system credentialStore
	file   *fileStore
}

func (s *keychainStore) Load(ctx context.Context) (youtube.Tokens, error) {
	if s.system.available() {
		if value, err := s.system.get(); err == nil {
			var tokens youtube.Tokens
			if err := json.Unmarshal([]byte(value), &tokens); err != nil {
				return youtube.Tokens{}, fmt.Errorf("decode the saved sign-in: %w", err)
			}
			return tokens, nil
		}
	}
	// The system store holds nothing, or would not answer, as a locked
	// keychain will not: an earlier version of the app, or one whose store
	// refused it, may have left the tokens in the file. Finding them there
	// moves them into the store.
	tokens, err := s.file.Load(ctx)
	if err != nil {
		return youtube.Tokens{}, err
	}
	if s.system.available() {
		if data, err := json.Marshal(tokens); err == nil {
			if err := s.system.set(string(data)); err == nil {
				_ = s.file.Delete(ctx)
			}
		}
	}
	return tokens, nil
}

func (s *keychainStore) Save(ctx context.Context, tokens youtube.Tokens) error {
	if s.system.available() {
		if data, err := json.Marshal(tokens); err == nil {
			if err := s.system.set(string(data)); err == nil {
				// The tokens do not belong in the file once the system
				// store has them.
				_ = s.file.Delete(ctx)
				return nil
			}
		}
	}
	return s.file.Save(ctx, tokens)
}

func (s *keychainStore) Delete(ctx context.Context) error {
	if s.system.available() {
		s.system.remove()
	}
	return s.file.Delete(ctx)
}

// fileStore keeps the OAuth tokens in a file only their user can read.
type fileStore struct {
	path string
}

func newFileStore(path string) *fileStore { return &fileStore{path: path} }

func (s *fileStore) Load(context.Context) (youtube.Tokens, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return youtube.Tokens{}, youtube.ErrNoStoredTokens
	}
	if err != nil {
		return youtube.Tokens{}, err
	}
	var tokens youtube.Tokens
	if err := json.Unmarshal(data, &tokens); err != nil {
		return youtube.Tokens{}, err
	}
	return tokens, nil
}

func (s *fileStore) Save(_ context.Context, tokens youtube.Tokens) error {
	data, err := json.Marshal(tokens)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	// The refresh token is a credential: keep it readable by this user only.
	return os.WriteFile(s.path, data, 0o600)
}

func (s *fileStore) Delete(context.Context) error {
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
