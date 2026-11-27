package ui_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
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
	if !o.Visible() {
		return
	}
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

func TestCalls(t *testing.T) {
	a := test.NewTempApp(t)
	accts := demo.NewAccounts()
	accts.LiveInterval = 0
	calls := accts.FakeCalls()
	u := ui.New(context.Background(), a, accts)
	u.SetSynchronous()
	w := u.Window()
	w.Show()
	u.Start()
	list := findList(t, w.Content())
	waitFor(t, "chat list", func() bool { return list.Length() == 3 })

	// Outgoing call from the group chat: all members except me.
	fyne.DoAndWait(func() { list.Select(0) })
	waitFor(t, "group open", func() bool { return strings.Contains(allText(w.Content()), "Standup now") })
	var callBtn *widget.Button
	walk(w.Content(), func(o fyne.CanvasObject) {
		if b, ok := o.(*widget.Button); ok && b.Icon != nil && b.Icon.Name() == theme.MediaRecordIcon().Name() && callBtn == nil {
			callBtn = b
		}
	})
	if callBtn == nil || callBtn.Disabled() {
		t.Fatal("call button missing or disabled")
	}
	fyne.DoAndWait(func() { test.Tap(callBtn) })
	waitFor(t, "call bar connected", func() bool { return strings.Contains(allText(w.Content()), "Call · Project acat · Connected") })
	if len(calls.Log) != 1 || !strings.Contains(calls.Log[0], "00000000-0000-0000-0000-00000000000b") || strings.Contains(calls.Log[0], "00000000000a") {
		t.Fatalf("call log %v", calls.Log)
	}
	if !calls.Visible || !callBtn.Disabled() {
		t.Fatal("call window should be shown and call buttons disabled during a call")
	}

	// Hang up via call bar.
	fyne.DoAndWait(func() { test.Tap(findButton(t, w.Content(), "Hang up")) })
	waitFor(t, "call ended", func() bool { return !strings.Contains(allText(w.Content()), "Call · ") })
	if calls.Visible {
		t.Fatal("call window should be hidden after hang-up")
	}

	// Meeting link in a message → Join meeting.
	fyne.DoAndWait(func() { test.Tap(findButton(t, w.Content(), "Join meeting")) })
	waitFor(t, "meeting joined", func() bool { return strings.Contains(allText(w.Content()), "Call · Meeting · Connected") })
	if !strings.HasPrefix(calls.Log[len(calls.Log)-1], "join https://teams.microsoft.com/l/meetup-join/") {
		t.Fatalf("log %v", calls.Log)
	}
	fyne.DoAndWait(func() { test.Tap(findButton(t, w.Content(), "Hang up")) })

	// Incoming call → dialog → accept.
	var id string
	fyne.DoAndWait(func() {
		test.AssertNotificationSent(t, &fyne.Notification{Title: "Incoming call", Content: "Jane Roe is calling"}, func() {
			id = calls.RingFromJane()
		})
	})
	overlay := w.Canvas().Overlays().Top()
	if overlay == nil {
		t.Fatal("no incoming call dialog")
	}
	fyne.DoAndWait(func() { test.Tap(findButton(t, overlay, "Accept")) })
	waitFor(t, "accepted", func() bool { return strings.Contains(allText(w.Content()), "Call · Jane Roe · Connected") })
	if calls.Log[len(calls.Log)-1] != "accept "+id {
		t.Fatalf("log %v", calls.Log)
	}
}
