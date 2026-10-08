package main

import (
	"context"
	"errors"
	"time"

	"github.com/egoist/mygo/ui"

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

// signInModal shows the device code the user types at Google, while the
// sign-in is going on.
func (a *app) signInModal(c *ui.Context) {
	if !a.signIn.open {
		return
	}
	t := c.Theme()
	ui.Modal(c, &a.signIn.open, func() {
		ui.Column(c).Width(420).Gap(14).Padding(24).Radius(14).
			Background(t.Background).Border(1, t.Border).Children(func() {
			ui.Text(c, "Sign in to YouTube Music").FontSize(18).Bold()
			if a.signIn.code.UserCode == "" {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c)
					ui.Text(c, "Asking Google for a code…").TextColor(t.TextMuted)
				})
			} else {
				ui.Text(c, "Open the page below in a browser and enter this code:").TextColor(t.TextMuted)
				ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Text(c, a.signIn.code.UserCode).FontSize(26).Bold().LetterSpacing(2)
					if ui.Button(c, "Copy").Clicked() {
						c.WriteClipboard(a.signIn.code.UserCode)
						c.Toast("Code copied")
					}
				})
				ui.Link(c, a.signIn.code.VerificationURL, a.signIn.code.VerificationURL)
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c)
					ui.Text(c, "Waiting for you to approve the sign-in…").TextColor(t.TextMuted)
				})
			}
			if a.signIn.err != "" {
				ui.Text(c, a.signIn.err).TextColor(t.Danger).MaxLines(4)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					a.signIn.open = false
				}
			})
		})
	})
}

// accountPanel shows who is signed in, or the button that starts a sign-in.
func (a *app) accountPanel(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Padding(12, 14).Gap(8).Children(func() {
		if a.signedIn {
			name := a.account.Name
			if name == "" {
				name = "Signed in"
			}
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Image(c, a.thumbs.bitmap(a.account.Thumbnail, 64)).Size(30, 30).Fit(ui.Cover).Radius(15).Background(t.SurfaceHover)
				ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
					ui.Text(c, name).SingleLine().FontSize(12).Bold()
					if a.account.Email != "" {
						ui.Text(c, a.account.Email).SingleLine().FontSize(11).TextColor(t.TextMuted)
					}
				})
			})
			if ui.Button(c, "Sign out").FillWidth().FontSize(11).Clicked() {
				a.signOut()
			}
			return
		}
		if ui.PrimaryButton(c, "Sign in with Google").Clicked() {
			a.signInWithGoogle()
		}
		if a.signIn.err != "" && !a.signIn.open {
			ui.Text(c, a.signIn.err).FontSize(11).TextColor(t.Danger).MaxLines(3)
		}
	})
}
