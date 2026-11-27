package ui_test

import (
	"context"
	"reflect"
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

	// Open the 1:1 chat with Jane (second by activity): it was unread.
	fyne.DoAndWait(func() { list.Select(1) })
	waitFor(t, "jane chat", func() bool { return strings.Contains(allText(w.Content()), "Agenda for tomorrow") })

	// Send via composer: type + Enter.
	entry := findComposer(t, w.Content())
	fyne.DoAndWait(func() {
		test.Type(entry, "Hello from acat")
		entry.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	})
	waitFor(t, "sent message", func() bool { return strings.Contains(allText(w.Content()), "Hello from acat") })
	// The chat moved to the top of the list.
	waitFor(t, "chat reordered", func() bool {
		txt := allText(list)
		i, j := strings.Index(txt, "You: Hello from acat"), strings.Index(txt, "Project acat")
		return i >= 0 && j > i
	})

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

// findComposer returns the message input (unexported type submitEntry).
func findComposer(t *testing.T, root fyne.CanvasObject) fyne.Focusable {
	var e fyne.Focusable
	walk(root, func(o fyne.CanvasObject) {
		if f, ok := o.(fyne.Focusable); ok && e == nil && reflect.TypeOf(o).String() == "*ui.submitEntry" {
			e = f
		}
	})
	if e == nil {
		t.Fatal("no composer")
	}
	return e
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

func TestLiveIncomingMessage(t *testing.T) {
	a := test.NewTempApp(t)
	accts := demo.NewAccounts()
	accts.LiveInterval = 0 // only explicit pushes
	u := ui.New(context.Background(), a, accts)
	u.SetSynchronous()
	w := u.Window()
	w.Show()
	u.Start()
	list := findList(t, w.Content())
	waitFor(t, "chat list", func() bool { return list.Length() == 3 })
	fyne.DoAndWait(func() { list.Select(0) }) // group chat open, not Jane's

	waitFor(t, "title without unread", func() bool { return w.Title() == "acat (1)" || w.Title() == "acat" })
	fyne.DoAndWait(func() { list.Select(1) }) // read Jane's chat first
	waitFor(t, "all read", func() bool { return w.Title() == "acat" })
	fyne.DoAndWait(func() { list.Select(0) })

	fyne.DoAndWait(func() {
		test.AssertNotificationSent(t, &fyne.Notification{Title: "Jane Roe", Content: "Ping from Jane"}, func() {
			accts.Push("Ping from Jane")
		})
	})
	waitFor(t, "unread count in title", func() bool { return w.Title() == "acat (1)" })
	waitFor(t, "jane on top", func() bool {
		txt := allText(list)
		i, j := strings.Index(txt, "Jane Roe: Ping from Jane"), strings.Index(txt, "Project acat")
		return i >= 0 && j > i
	})

	// Looking at Jane's chat: no notification, stays read.
	fyne.DoAndWait(func() { list.Select(0) })
	waitFor(t, "read again", func() bool { return w.Title() == "acat" })
	fyne.DoAndWait(func() {
		test.AssertNotificationSent(t, nil, func() { accts.Push("Second ping") })
	})
	waitFor(t, "second message visible", func() bool { return strings.Contains(allText(w.Content()), "Second ping") })
	if w.Title() != "acat" {
		t.Fatalf("title %q", w.Title())
	}
}
