package ui_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ape-dev-de/acat/internal/demo"
	"github.com/ape-dev-de/acat/internal/ui"
)

// waitFor polls cond on the Fyne main goroutine.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ok := false
		fyne.DoAndWait(func() { ok = cond() })
		if ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestDemoFlow(t *testing.T) {
	a := test.NewTempApp(t)
	u := ui.New(context.Background(), a, demo.NewAccounts())
	u.SetSynchronous()
	w := u.Window()
	w.Show()
	u.Start()

	list := findList(t, w.Content())
	waitFor(t, "chat list", func() bool { return list.Length() == 3 })

	// Open the group chat (most recent activity → first row).
	fyne.DoAndWait(func() { list.Select(0) })
	waitFor(t, "first message page", func() bool {
		return strings.Contains(allText(w.Content()), "can you review the relay PR?")
	})
	text := allText(w.Content())
	for _, want := range []string{"Project acat", "@Jane Roe", "[file: review-notes.docx]", "This message was deleted.", "edited"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in rendered UI", want)
		}
	}
	if strings.Contains(text, "Backlog message 1\n") {
		t.Error("oldest page should not be loaded yet")
	}

	older := findButton(t, w.Content(), "Load older messages")
	for range 3 {
		fyne.DoAndWait(func() {
			if older.Visible() {
				test.Tap(older)
			}
		})
		time.Sleep(50 * time.Millisecond)
	}
	waitFor(t, "oldest message", func() bool { return strings.Contains(allText(w.Content()), "Backlog message 1") })
	waitFor(t, "load-older hidden when complete", func() bool { return !older.Visible() })

	// Second account via "Add account" (demo login).
	add := findButton(t, w.Content(), "Add account")
	fyne.DoAndWait(func() { test.Tap(add) })
	sel := findSelect(t, w.Content())
	waitFor(t, "second account", func() bool { return len(sel.Options) == 2 && strings.HasPrefix(sel.Selected, "guest") })
}

func walk(o fyne.CanvasObject, fn func(fyne.CanvasObject)) {
	fn(o)
	switch x := o.(type) {
	case *fyne.Container:
		for _, c := range x.Objects {
			walk(c, fn)
		}
	case fyne.Widget:
		for _, c := range test.WidgetRenderer(x).Objects() {
			walk(c, fn)
		}
	}
}

func findList(t *testing.T, root fyne.CanvasObject) *widget.List {
	var l *widget.List
	walk(root, func(o fyne.CanvasObject) {
		if x, ok := o.(*widget.List); ok && l == nil {
			l = x
		}
	})
	if l == nil {
		t.Fatal("no list")
	}
	return l
}

func findSelect(t *testing.T, root fyne.CanvasObject) *widget.Select {
	var s *widget.Select
	walk(root, func(o fyne.CanvasObject) {
		if x, ok := o.(*widget.Select); ok && s == nil {
			s = x
		}
	})
	if s == nil {
		t.Fatal("no select")
	}
	return s
}

func findButton(t *testing.T, root fyne.CanvasObject, label string) *widget.Button {
	var b *widget.Button
	walk(root, func(o fyne.CanvasObject) {
		if x, ok := o.(*widget.Button); ok && x.Text == label {
			b = x
		}
	})
	if b == nil {
		t.Fatalf("no button %q", label)
	}
	return b
}

func allText(root fyne.CanvasObject) string {
	var sb strings.Builder
	walk(root, func(o fyne.CanvasObject) {
		switch x := o.(type) {
		case *widget.Label:
			sb.WriteString(x.Text + "\n")
		case *widget.RichText:
			sb.WriteString(x.String() + "\n")
		}
	})
	return sb.String()
}
