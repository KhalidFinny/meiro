package youtube

import (
	"context"
	"errors"
)

// ErrNoStoredTokens indicates that a token store has no saved OAuth session.
var ErrNoStoredTokens = errors.New("youtube: no stored OAuth tokens")

// TokenStore persists OAuth credentials. Implementations should encrypt tokens
// with the operating system keychain or another secure secret store.
type TokenStore interface {
	Load(context.Context) (Tokens, error)
	Save(context.Context, Tokens) error
	Delete(context.Context) error
}
