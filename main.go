package main

import (
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// app is the state the window shows. Its view builds the interface from
// it, on the main thread, whenever the window needs a frame: after input,
// after Window.Update, and while something animates.
type app struct {
	count int
}

func (a *app) view(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Center().Gap(16).Padding(24).Children(func() {
		ui.Text(c, "Meiro Counter").FontSize(26).Bold()
		ui.Text(c, "A tiny native counter").TextColor(t.TextMuted)
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			if ui.Button(c, "−").Width(48).Clicked() {
				a.count--
			}
			ui.Textf(c, "%d", a.count).FontSize(24).Width(64).TextAlign(ui.Center)
			if ui.PrimaryButton(c, "+").Width(48).Clicked() {
				a.count++
			}
		})
	})
}

func main() {
	a := &app{}
	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:     "Meiro Counter",
			Width:     480,
			Height:    360,
			MinWidth:  320,
			MinHeight: 280,
			// Opens where the user left it last time.
			StateKey: "main",
			// The window shows the interface MyGo draws, not a web page.
			Content: ui.View(a.view),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
