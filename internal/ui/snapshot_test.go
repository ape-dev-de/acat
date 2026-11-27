package ui_test

import (
	"context"
	"image/png"
	"os"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/ape-dev-de/acat/internal/demo"
	"github.com/ape-dev-de/acat/internal/ui"
)

// TestSnapshot writes a PNG of the demo UI when ACAT_SNAPSHOT=<file> is set.
func TestSnapshot(t *testing.T) {
	out := os.Getenv("ACAT_SNAPSHOT")
	if out == "" {
		t.Skip("set ACAT_SNAPSHOT=<file.png> to render the demo UI")
	}
	a := test.NewTempApp(t)
	u := ui.New(context.Background(), a, demo.NewAccounts())
	u.SetSynchronous()
	w := u.Window()
	w.Resize(fyne.NewSize(1100, 720))
	w.Show()
	u.Start()
	list := findList(t, w.Content())
	fyne.DoAndWait(func() { list.Select(1) })
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := png.Encode(f, w.Canvas().Capture()); err != nil {
		t.Fatal(err)
	}
}
