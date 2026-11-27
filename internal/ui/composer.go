package ui

import (
	"strings"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/ape-dev-de/acat/internal/compose"
	"github.com/ape-dev-de/acat/internal/graph"
)

// submitEntry is a multi-line entry where Enter submits and Shift+Enter
// inserts a line break.
type submitEntry struct {
	widget.Entry
	shift    bool
	onSubmit func()
}

func newSubmitEntry() *submitEntry {
	e := &submitEntry{}
	e.MultiLine = true
	e.Wrapping = fyne.TextWrapWord
	e.SetMinRowsVisible(2)
	e.ExtendBaseWidget(e)
	return e
}

func (e *submitEntry) KeyDown(ev *fyne.KeyEvent) {
	if ev.Name == desktop.KeyShiftLeft || ev.Name == desktop.KeyShiftRight {
		e.shift = true
	}
	e.Entry.KeyDown(ev)
}

func (e *submitEntry) KeyUp(ev *fyne.KeyEvent) {
	if ev.Name == desktop.KeyShiftLeft || ev.Name == desktop.KeyShiftRight {
		e.shift = false
	}
	e.Entry.KeyUp(ev)
}

func (e *submitEntry) TypedKey(ev *fyne.KeyEvent) {
	if (ev.Name == fyne.KeyReturn || ev.Name == fyne.KeyEnter) && !e.shift && e.onSubmit != nil {
		e.onSubmit()
		return
	}
	e.Entry.TypedKey(ev)
}

// textBeforeCursor returns the entry text up to the cursor.
func (e *submitEntry) textBeforeCursor() string {
	lines := strings.Split(e.Text, "\n")
	row := min(e.CursorRow, len(lines)-1)
	var sb strings.Builder
	for i := 0; i < row; i++ {
		sb.WriteString(lines[i])
		sb.WriteByte('\n')
	}
	line := lines[row]
	col := min(e.CursorColumn, utf8.RuneCountInString(line))
	sb.WriteString(string([]rune(line)[:col]))
	return sb.String()
}

// setTextCursorAfter sets the text and puts the cursor after prefix.
func (e *submitEntry) setTextCursorAfter(text, prefix string) {
	e.SetText(text)
	lines := strings.Split(prefix, "\n")
	e.CursorRow = len(lines) - 1
	e.CursorColumn = utf8.RuneCountInString(lines[len(lines)-1])
	e.Refresh()
}

// composeMode is what the composer is doing.
type composeMode int

const (
	modeNew composeMode = iota
	modeReply
	modeEdit
)

// composer is the message input area.
type composer struct {
	u        *UI
	entry    *submitEntry
	send     *widget.Button
	ctxLabel *widget.Label
	ctxBar   *fyne.Container
	popup    *widget.PopUpMenu

	mode     composeMode
	target   *graph.Message // quoted or edited message
	mentions []compose.Person
	box      *fyne.Container
}

func newComposer(u *UI) *composer {
	c := &composer{u: u, entry: newSubmitEntry()}
	c.entry.SetPlaceHolder("Type a message (Enter to send, Shift+Enter for a new line)")
	c.entry.onSubmit = c.submit
	c.entry.OnChanged = func(string) { c.updateSuggestions() }
	c.send = widget.NewButtonWithIcon("", theme.MailSendIcon(), c.submit)
	c.ctxLabel = widget.NewLabel("")
	c.ctxLabel.Truncation = fyne.TextTruncateEllipsis
	cancel := widget.NewButtonWithIcon("", theme.CancelIcon(), c.reset)
	cancel.Importance = widget.LowImportance
	c.ctxBar = container.NewBorder(nil, nil, nil, cancel, c.ctxLabel)
	c.ctxBar.Hide()
	c.box = container.NewBorder(c.ctxBar, nil, nil, c.send, c.entry)
	return c
}

func (c *composer) object() fyne.CanvasObject { return c.box }

func (c *composer) setEnabled(on bool) {
	if on {
		c.entry.Enable()
		c.send.Enable()
	} else {
		c.entry.Disable()
		c.send.Disable()
	}
}

func (c *composer) reset() {
	c.mode, c.target, c.mentions = modeNew, nil, nil
	c.entry.SetText("")
	c.ctxBar.Hide()
	c.hidePopup()
}

func (c *composer) reply(m graph.Message) {
	c.mode, c.target = modeReply, &m
	c.ctxLabel.SetText("Replying to " + m.From.DisplayName() + ": " + firstLine(plainBody(m)))
	c.ctxBar.Show()
	c.focus()
}

func (c *composer) edit(m graph.Message) {
	in := compose.ToEditText(m, plainBody(m))
	c.mode, c.target, c.mentions = modeEdit, &m, in.Mentions
	c.entry.SetText(in.Text)
	c.ctxLabel.SetText("Editing message")
	c.ctxBar.Show()
	c.focus()
}

func (c *composer) focus() {
	if cv := fyne.CurrentApp().Driver().CanvasForObject(c.entry); cv != nil {
		cv.Focus(c.entry)
	}
}

func (c *composer) submit() {
	in := compose.Input{Text: c.entry.Text, Mentions: c.mentions}
	if c.mode == modeReply {
		in.Quote = c.target
	}
	if in.Empty() {
		return
	}
	mode, target := c.mode, c.target
	c.reset()
	c.u.sendMessage(mode, target, compose.Build(in))
}

// candidates are the people of the open chat (without the user).
func (c *composer) candidates() []compose.Person {
	if c.u.current == nil || c.u.current.store == nil {
		return nil
	}
	ch, ok := c.u.current.store.Chat(c.u.selected)
	if !ok {
		return nil
	}
	var out []compose.Person
	for _, m := range ch.Members {
		if m.UserID != c.u.current.store.Self() && m.DisplayName != "" {
			out = append(out, compose.Person{ID: m.UserID, DisplayName: m.DisplayName})
		}
	}
	return out
}

func (c *composer) updateSuggestions() {
	before := c.entry.textBeforeCursor()
	q, ok := compose.MentionQuery(before)
	if !ok || strings.HasSuffix(q, " ") { // trailing space: mention done
		c.hidePopup()
		return
	}
	people := compose.Suggest(c.candidates(), q, 8)
	if len(people) == 0 {
		c.hidePopup()
		return
	}
	items := make([]*fyne.MenuItem, 0, len(people))
	for _, p := range people {
		items = append(items, fyne.NewMenuItem(p.DisplayName, func() { c.pickMention(p, q) }))
	}
	cv := fyne.CurrentApp().Driver().CanvasForObject(c.entry)
	if cv == nil {
		return
	}
	c.hidePopup()
	c.popup = widget.NewPopUpMenu(fyne.NewMenu("", items...), cv)
	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(c.entry)
	c.popup.ShowAtPosition(pos.SubtractXY(0, c.popup.MinSize().Height))
}

func (c *composer) pickMention(p compose.Person, query string) {
	c.hidePopup()
	before := c.entry.textBeforeCursor()
	after := c.entry.Text[len(before):]
	prefix := before[:len(before)-len(query)-1] + "@" + p.DisplayName + " "
	c.mentions = append(c.mentions, p)
	c.entry.setTextCursorAfter(prefix+after, prefix)
	c.focus()
}

func (c *composer) hidePopup() {
	if c.popup != nil {
		c.popup.Hide()
		c.popup = nil
	}
}
