package main

import (
	"context"
	"errors"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

// signInState is the Google sign-in the user is going through, if any.
type signInState struct {
	open bool
	err  string
	code youtube.DeviceCode
	// cancel stops the wait for the user to authorize the device code.
	cancel context.CancelFunc
}

// signIn asks Google for a device code, shows it, and waits for the user to
// authorize it in their browser.
func (a *app) signInWithGoogle() {
	if a.oauth == nil || a.signIn.open {
		return
	}
	a.signIn = signInState{open: true}
	ctx, cancel := context.WithCancel(context.Background())
	a.signIn.cancel = cancel
	a.run(func() {
		begin, cancelBegin := context.WithTimeout(ctx, time.Minute)
		code, err := a.oauth.BeginDeviceFlow(begin)
		cancelBegin()
		if err != nil {
			a.update(func() { a.signIn.err = err.Error() })
			return
		}
		a.update(func() { a.signIn.code = code })

		wait, cancelWait := context.WithTimeout(ctx, time.Duration(code.ExpiresIn+30)*time.Second)
		defer cancelWait()
		if _, err := a.oauth.PollForTokens(wait, code); err != nil {
			if !errors.Is(err, context.Canceled) {
				a.update(func() { a.signIn.err = err.Error() })
			}
			return
		}
		details, err := a.authed.GetAccountDetails(wait)
		a.update(func() {
			a.signedIn = true
			a.signIn = signInState{}
			if err == nil {
				a.account = *details
			}
			a.onSignedIn()
		})
	})
}

// restoreAccount loads the tokens saved by an earlier run and the account
// they belong to.
func (a *app) restoreAccount() {
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := a.oauth.Restore(ctx); err != nil {
			return // not signed in, or the saved tokens are no longer good
		}
		details, err := a.authed.GetAccountDetails(ctx)
		a.update(func() {
			a.signedIn = true
			if err == nil {
				a.account = *details
			}
			a.onSignedIn()
		})
	})
}

// signOut forgets the account, here and at Google.
func (a *app) signOut() {
	if a.oauth == nil {
		return
	}
	oauth, store := a.oauth, a.store
	a.oauth = youtube.NewOAuth(youtube.OAuthConfig{TokenStore: store})
	a.authed = youtube.NewClient(youtube.Options{OAuth: a.oauth})
	a.signedIn, a.account = false, youtube.AccountDetails{}
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := oauth.Revoke(ctx); err != nil {
			_ = store.Delete(ctx)
		}
	})
}

// onSignedIn reloads the page when it is one that needs the account.
func (a *app) onSignedIn() {
	if a.router.Path() == "/library" {
		a.onNavigate()
	}
}

// signInDialog shows the device code the user types at Google, while the
// sign-in is going on.
func (a *app) signInDialog(c *ui.Context) {
	if !a.signIn.open {
		return
	}
	sc := m3.Active().Scheme
	m3.Dialog(c, &a.signIn.open, 440, func() {
		ui.Box(c).Size(56, 56).Radius(m3.Large).Background(sc.PrimaryContainer).Center().Children(func() {
			ui.Icon(c, m3.IconLogin).FontSize(28).TextColor(sc.OnPrimaryContainer)
		})
		m3.EmphasizedText(c, m3.HeadlineSmall, "Sign in to YouTube Music")
		if a.signIn.code.UserCode == "" {
			ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
				m3.LoadingIndicator(c, 40, false)
				m3.Text(c, m3.BodyMedium, "Asking Google for a code…").TextColor(sc.OnSurfaceVariant)
			})
		} else {
			m3.Text(c, m3.BodyMedium, "Open the page below in a browser and enter this code:").TextColor(sc.OnSurfaceVariant)
			ui.Row(c).Gap(8).Padding(8, 8, 8, 20).AlignItems(ui.Center).Radius(m3.LargeIncreased).Background(sc.SurfaceContainerHighest).Children(func() {
				m3.EmphasizedText(c, m3.HeadlineMedium, a.signIn.code.UserCode).LetterSpacing(3).Grow(1).SelectionColor(sc.PrimaryContainer)
				if m3.IconButton(c, m3.IconButtonSpec{Icon: m3.IconCopy, Label: "Copy the code", Kind: m3.TonalIcon, Key: "copy-code"}).Clicked() {
					c.WriteClipboard(a.signIn.code.UserCode)
					c.Toast("Code copied")
				}
			})
			ui.Link(c, a.signIn.code.VerificationURL, a.signIn.code.VerificationURL).TextColor(sc.Primary)
			ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
				m3.LoadingIndicator(c, 40, false)
				m3.Text(c, m3.BodyMedium, "Waiting for you to approve the sign-in…").TextColor(sc.OnSurfaceVariant)
			})
		}
		if a.signIn.err != "" {
			m3.Text(c, m3.BodyMedium, a.signIn.err).TextColor(sc.Error).MaxLines(4)
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if m3.Button(c, m3.ButtonSpec{Label: "Cancel", Kind: m3.TextOnly, Key: "cancel-sign-in"}).Clicked() {
				a.signIn.open = false
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
		} else if m3.MenuItem(c, "Sign in with Google", m3.IconLogin).Clicked() {
			a.menuOpen = false
			a.signInWithGoogle()
		}
	})
}
