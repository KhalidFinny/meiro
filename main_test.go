package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// The view runs without a window in tests, which click as a user would.
func TestCounterControls(t *testing.T) {
	a := &app{}
	tt := ui.NewTester(a.view, 480, 360)
	if !tt.HasText("0") {
		t.Fatalf("initial count is not visible: texts %q", tt.Texts())
	}
	if err := tt.Click("+"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("+"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("−"); err != nil {
		t.Fatal(err)
	}
	if a.count != 1 || !tt.HasText("1") {
		t.Errorf("count %d after two increments and one decrement, texts %q", a.count, tt.Texts())
	}
}
