package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

// musicURL is where the user signs in, and the page whose requests carry the
// cookie the app needs.
const musicURL = "https://music.youtube.com"

// signInState is the sign-in the user is going through, if any.
type signInState struct {
	open bool
	err  string
	// cookie is the text the user pasted.
	cookie string
	// busy is set while a cookie is being imported or tried.
	busy bool
	// browsers is the menu of browsers to import from.
	browsers bool
	// cancel stops the check of the pasted cookie.
	cancel context.CancelFunc
}

// signInWithGoogle opens the dialog that takes the cookie of the user's
// browser session. YouTube Music does not let another app sign in to it: the
// account's home page and library are only served to a request that carries
// the cookie of a signed-in browser.
func (a *app) signInWithGoogle() {
	if a.signIn.open {
		return
	}
	a.signIn = signInState{open: true}
}

// cookieHeader picks the cookie out of what the user pasted: the value of the
// header alone, the header with its name, or a whole line copied as a cURL
// command.
func cookieHeader(text string) string {
	text = strings.TrimSpace(text)
	lower := strings.ToLower(text)
	for _, marker := range []string{"-h 'cookie:", `-h "cookie:`, "cookie:"} {
		if i := strings.Index(lower, marker); i >= 0 {
			text = text[i+len(marker):]
			break
		}
	}
	text = strings.TrimSpace(text)
	if i := strings.IndexAny(text, "'\"\n"); i >= 0 {
		text = text[:i]
	}
	return strings.TrimSpace(text)
}

// submitSignIn tries the pasted cookie.
func (a *app) submitSignIn() {
	if a.signIn.busy {
		return
	}
	cookie := cookieHeader(a.signIn.cookie)
	if _, err := youtube.NewCookieAuth(cookie, youtube.CookieOptions{}); err != nil {
		a.signIn.err = "That is not the cookie of a signed-in session: it has no SAPISID. Copy the whole value of the Cookie header."
		return
	}
	a.signIn.busy, a.signIn.err = true, ""
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	a.signIn.cancel = cancel
	a.run(func() {
		defer cancel()
		a.finishSignIn(ctx, cookie)
	})
}

// importSignIn takes the session of a browser the user is signed in with.
func (a *app) importSignIn(source importSource) {
	if a.signIn.busy {
		return
	}
	a.signIn.busy, a.signIn.err = true, ""
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	a.signIn.cancel = cancel
	a.run(func() {
		defer cancel()
		cookie, err := source.read(ctx)
		if err != nil {
			a.update(func() {
				if a.signIn.open {
					a.signIn.busy, a.signIn.err = false, err.Error()
				}
			})
			return
		}
		a.finishSignIn(ctx, cookie)
	})
}

// finishSignIn asks YouTube whom the cookie signs in and, when it takes it,
// keeps it as the sign-in. It runs off the main thread.
func (a *app) finishSignIn(ctx context.Context, cookie string) {
	auth, err := youtube.NewCookieAuth(cookie, youtube.CookieOptions{})
	var client *youtube.Client
	var details *youtube.AccountDetails
	if err == nil {
		client = youtube.NewClient(youtube.Options{CookieAuth: auth})
		details, err = client.GetAccountDetails(ctx)
	}
	if err == nil && details.Name == "" && details.ChannelID == "" {
		err = errors.New("YouTube did not recognise the cookie as a signed-in session; it may have expired")
	}
	if err == nil && a.store != nil {
		err = a.store.Save(ctx, cookie)
	}
	a.update(func() {
		if !a.signIn.open { // the user gave up meanwhile
			return
		}
		a.signIn.busy = false
		if err != nil {
			a.signIn.err = err.Error()
			return
		}
		a.authed, a.signedIn, a.account = client, true, *details
		a.signIn = signInState{}
		a.onSignedIn()
	})
}

// restoreAccount loads the cookie saved by an earlier run and the account it
// belongs to.
func (a *app) restoreAccount() {
	if a.store == nil {
		return
	}
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		cookie, err := a.store.Load(ctx)
		if err != nil {
			return // not signed in
		}
		auth, err := youtube.NewCookieAuth(cookie, youtube.CookieOptions{})
		if err != nil {
			return
		}
		client := youtube.NewClient(youtube.Options{CookieAuth: auth})
		details, err := client.GetAccountDetails(ctx)
		a.update(func() {
			a.authed, a.signedIn = client, true
			if err == nil {
				a.account = *details
			}
			a.onSignedIn()
		})
	})
}

// signOut forgets the account.
func (a *app) signOut() {
	a.authed, a.signedIn, a.account = nil, false, youtube.AccountDetails{}
	a.onSignedIn()
	if a.store == nil {
		return
	}
	store := a.store
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = store.Delete(ctx)
	})
}

// onSignedIn reloads the page when it is one the account changes. The home
// page is loaded at launch, before the saved sign-in is back, so it needs the
// reload as much as the library does.
func (a *app) onSignedIn() {
	switch path := a.router.Path(); {
	case path == "/search" || path == "/settings":
	default:
		a.onNavigate()
	}
}

// signInDialog asks for the cookie of the user's browser session.
func (a *app) signInDialog(c *ui.Context) {
	if !a.signIn.open {
		return
	}
	sc := m3.Active().Scheme
	m3.Dialog(c, &a.signIn.open, 520, func() {
		ui.Box(c).Size(56, 56).Radius(m3.Large).Background(sc.PrimaryContainer).Center().Children(func() {
			ui.Icon(c, m3.IconLogin).FontSize(28).TextColor(sc.OnPrimaryContainer)
		})
		m3.EmphasizedText(c, m3.HeadlineSmall, "Sign in to YouTube Music")
		m3.Text(c, m3.BodyMedium, "YouTube Music serves your home page and library to a browser that is signed in, so Meiro borrows its session. Sign in at music.youtube.com in your browser first, then pick it:").TextColor(sc.OnSurfaceVariant)
		ui.Link(c, musicURL, "Open YouTube Music").TextColor(sc.Primary)
		picker := m3.Button(c, m3.ButtonSpec{Label: "Import from a browser", Icon: m3.IconExpandMore, Kind: m3.Tonal, Size: m3.Medium56, Disabled: a.signIn.busy, Key: "import-from"})
		if picker.Clicked() {
			a.signIn.browsers = !a.signIn.browsers
		}
		m3.Menu(c, picker, &a.signIn.browsers, 240, func() {
			for _, source := range importSources() {
				if m3.MenuItem(c, source.label, nil).Clicked() {
					a.signIn.browsers = false
					a.importSignIn(source)
				}
			}
		})
		m3.Text(c, m3.BodyMedium, "Or paste the value of the Cookie request header of a request to music.youtube.com, from your browser's developer tools. The session stays in your system keychain.").TextColor(sc.OnSurfaceVariant)
		box := ui.Column(c.Key("cookie-box")).Padding(12, 16).Radius(m3.Large).Background(sc.SurfaceContainerHighest)
		if box.FocusWithin() {
			box.Border(2, sc.Primary)
		}
		box.Children(func() {
			ui.TextAreaBase(c.Key("cookie"), &a.signIn.cookie).Placeholder("Paste the Cookie header").Label("Cookie header").
				Password().Height(96).FontSize(14).TextColor(sc.OnSurface).AutoFocus()
		})
		if a.signIn.err != "" {
			m3.Text(c, m3.BodyMedium, a.signIn.err).TextColor(sc.Error).MaxLines(4)
		}
		ui.Row(c).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			if a.signIn.busy {
				m3.LoadingIndicator(c, 40, false)
			}
			if m3.Button(c, m3.ButtonSpec{Label: "Cancel", Kind: m3.TextOnly, Key: "cancel-sign-in"}).Clicked() {
				a.signIn.open = false
			}
			if m3.Button(c, m3.ButtonSpec{Label: "Sign in", Disabled: a.signIn.busy || strings.TrimSpace(a.signIn.cookie) == "", Key: "submit-sign-in"}).Clicked() {
				a.submitSignIn()
			}
		})
	})
}

// accountButton is the account's picture in the top bar, which opens a menu
// of the account, the settings and the sign-in.
func (a *app) accountButton(c *ui.Context) {
	sc := m3.Active().Scheme
	button := ui.ButtonBase(c.Key("account"))
	button.Size(48, 48).Radius(m3.Full).Center().Cursor(ui.CursorPointer).Label("Account").Tooltip("Account")
	if button.Hovered() || a.menuOpen {
		button.Background(sc.OnSurface.Alpha(m3.StateHover))
	}
	button.Children(func() {
		if a.signedIn {
			name := a.account.Name
			if name == "" {
				name = "Signed in"
			}
			m3.Avatar(c, name, a.thumbs.bitmap(a.account.Thumbnail, 96), 36)
			return
		}
		ui.Box(c).Size(36, 36).Radius(m3.Full).Background(sc.SurfaceContainerHighest).Center().Children(func() {
			ui.Icon(c, m3.IconPerson).FontSize(22).TextColor(sc.OnSurfaceVariant)
		})
	})
	if button.Clicked() {
		a.menuOpen = !a.menuOpen
	}
	m3.Menu(c, button, &a.menuOpen, 280, func() {
		if a.signedIn {
			ui.Column(c).Padding(12, 12, 8).Gap(2).Children(func() {
				name := a.account.Name
				if name == "" {
					name = "Signed in"
				}
				m3.EmphasizedText(c, m3.TitleSmall, name).SingleLine()
				if a.account.Email != "" {
					m3.Text(c, m3.BodySmall, a.account.Email).SingleLine().TextColor(sc.OnSurfaceVariant)
				}
			})
			ui.Divider(c)
		}
		if m3.MenuItem(c, "Settings", m3.IconSettings).Clicked() {
			a.menuOpen = false
			a.router.Push("/settings")
		}
		if a.signedIn {
			if m3.MenuItem(c, "Sign out", m3.IconLogout).Clicked() {
				a.menuOpen = false
				a.signOut()
			}
		} else if m3.MenuItem(c, "Sign in", m3.IconLogin).Clicked() {
			a.menuOpen = false
			a.signInWithGoogle()
		}
	})
}
