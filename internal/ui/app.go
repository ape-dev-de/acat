// Package ui is the Fyne desktop UI. All widget access happens on the Fyne
// main goroutine; network calls run in goroutines and hand results back via
// fyne.Do.
package ui

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/ape-dev-de/acat/internal/graph"
	"github.com/ape-dev-de/acat/internal/render"
	"github.com/ape-dev-de/acat/internal/store"
)

// ChatAPI is what the UI needs from Graph (graph.Client implements it).
type ChatAPI interface {
	Me(ctx context.Context) (graph.User, error)
	ListChats(ctx context.Context, limit int) ([]graph.Chat, error)
	ListMessages(ctx context.Context, chatID string, top int) (graph.Page[graph.Message], error)
	MoreMessages(ctx context.Context, nextLink string) (graph.Page[graph.Message], error)
	GetChat(ctx context.Context, chatID string) (graph.Chat, error)
	GetMessage(ctx context.Context, chatID, messageID string) (graph.Message, error)
	SendMessage(ctx context.Context, chatID string, m graph.NewMessage) (graph.Message, error)
	EditMessage(ctx context.Context, chatID, messageID string, m graph.NewMessage) error
	DeleteMessage(ctx context.Context, chatID, messageID string) error
	CreateChat(ctx context.Context, selfID string, userIDs []string, topic string) (graph.Chat, error)
	SearchUsers(ctx context.Context, query string, top int) ([]graph.User, error)
	MarkChatRead(ctx context.Context, chatID, userID, tenantID string) error
	GetPresences(ctx context.Context, ids []string) (map[string]graph.Presence, error)
	SetPreferredPresence(ctx context.Context, userID string, p graph.PreferredPresence, d time.Duration) error
	ClearPreferredPresence(ctx context.Context, userID string) error
}

// AccountInfo identifies a signed-in account.
type AccountInfo struct {
	ID       string
	Username string
	TenantID string
}

// Accounts manages sign-in and hands out a ChatAPI per account.
type Accounts interface {
	List(ctx context.Context) ([]AccountInfo, error)
	Login(ctx context.Context) (AccountInfo, error)
	Logout(ctx context.Context, id string) error
	API(id string) ChatAPI
}

const requestTimeout = 60 * time.Second

type session struct {
	info     AccountInfo
	api      ChatAPI
	store    *store.Store
	stopLive func()

	// main goroutine only
	presence     map[string]graph.Presence
	stopPresence func()
	statusLabel  string
	calls        Calls
}

// UI is the main window.
type UI struct {
	app   fyne.App
	win   fyne.Window
	accts Accounts
	ctx   context.Context
	spawn func(func()) // runs background work; synchronous in tests
	sync  bool         // SetSynchronous was called

	updatingStatus bool // suppress onStatusSelected while syncing the selector

	accounts []AccountInfo
	sessions map[string]*session
	current  *session
	chats    []graph.Chat // snapshot shown in chatList
	selected string       // selected chat ID
	focused  bool         // window has focus (for notifications)

	accountSelect *widget.Select
	statusSelect  *widget.Select
	addAccount    *widget.Button
	signOut       *widget.Button
	newChat       *widget.Button
	chatList      *widget.List
	composer      *composer
	callAudio     *widget.Button
	callVideo     *widget.Button
	callLabel     *widget.Label
	callBar       *fyne.Container

	activeCall       *callState
	callsUnavailable string
	header           *widget.Label
	olderBtn         *widget.Button
	msgBox           *fyne.Container
	msgScroll        *container.Scroll
	status           *widget.Label
}

// New builds the main window. Call Start to load accounts, then
// Window().ShowAndRun().
func New(ctx context.Context, app fyne.App, accts Accounts) *UI {
	u := &UI{app: app, accts: accts, ctx: ctx, sessions: map[string]*session{}, spawn: func(f func()) { go f() }}
	u.win = app.NewWindow("acat")
	u.win.Resize(fyne.NewSize(1100, 720))

	u.accountSelect = widget.NewSelect(nil, u.onAccountSelected)
	u.accountSelect.PlaceHolder = "No account"
	u.statusSelect = widget.NewSelect(statusOptions(), u.onStatusSelected)
	u.statusSelect.PlaceHolder = "Status"
	u.statusSelect.Disable()
	u.addAccount = widget.NewButtonWithIcon("Add account", theme.ContentAddIcon(), u.onLogin)
	u.signOut = widget.NewButtonWithIcon("Sign out", theme.LogoutIcon(), u.onLogout)
	u.signOut.Disable()

	u.chatList = widget.NewList(
		func() int { return len(u.chats) },
		func() fyne.CanvasObject {
			title := widget.NewLabel("title")
			title.Truncation = fyne.TextTruncateEllipsis
			preview := widget.NewLabel("preview")
			preview.Truncation = fyne.TextTruncateEllipsis
			preview.Importance = widget.LowImportance
			dot := canvas.NewCircle(color.Transparent)
			dotBox := container.NewGridWrap(fyne.NewSquareSize(10), dot)
			return container.NewBorder(nil, nil, container.NewCenter(dotBox), nil, container.NewVBox(title, preview))
		},
		func(id widget.ListItemID, o fyne.CanvasObject) {
			if id >= len(u.chats) || u.current == nil || u.current.store == nil {
				return
			}
			c := u.chats[id]
			row := o.(*fyne.Container)
			box := row.Objects[0].(*fyne.Container)
			dot := row.Objects[1].(*fyne.Container).Objects[0].(*fyne.Container).Objects[0].(*canvas.Circle)
			title := box.Objects[0].(*widget.Label)
			preview := box.Objects[1].(*widget.Label)
			pid, isOneOnOne := partner(c, u.current.store.Self())
			p, ok := u.current.presence[pid]
			dot.FillColor = presenceColor(p, ok && isOneOnOne)
			dot.Refresh()
			title.SetText(c.Title(u.current.store.Self()))
			title.TextStyle = fyne.TextStyle{Bold: c.Unread(u.current.store.Self())}
			title.Refresh()
			preview.SetText(previewText(c))
		},
	)
	u.chatList.OnSelected = func(id widget.ListItemID) {
		if id < len(u.chats) {
			u.openChat(u.chats[id].ID)
		}
	}

	u.header = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	u.olderBtn = widget.NewButton("Load older messages", u.onLoadOlder)
	u.olderBtn.Hide()
	u.msgBox = container.NewVBox()
	u.msgScroll = container.NewVScroll(u.msgBox)
	u.newChat = widget.NewButtonWithIcon("New chat", theme.DocumentCreateIcon(), u.showNewChat)
	u.newChat.Disable()
	u.buildCallUI()
	u.composer = newComposer(u)
	u.composer.setEnabled(false)
	u.status = widget.NewLabel("")
	u.status.Importance = widget.LowImportance
	u.status.Wrapping = fyne.TextWrapWord

	top := container.NewBorder(nil, nil, nil, container.NewHBox(u.statusSelect, u.addAccount, u.signOut), u.accountSelect)
	headerRow := container.NewBorder(nil, nil, nil, container.NewHBox(u.callAudio, u.callVideo), u.header)
	right := container.NewBorder(
		container.NewVBox(u.callBar, headerRow, widget.NewSeparator()),
		u.composer.object(), nil, nil,
		container.NewBorder(u.olderBtn, nil, nil, nil, u.msgScroll),
	)
	left := container.NewBorder(u.newChat, nil, nil, nil, u.chatList)
	split := container.NewHSplit(left, right)
	split.Offset = 0.3
	u.win.SetContent(container.NewBorder(container.NewVBox(top, widget.NewSeparator()), u.status, nil, nil, split))
	u.setupDesktop()
	return u
}

// SetSynchronous runs background work inline. Only for tests: the Fyne
// test driver executes fyne.Do on the calling goroutine.
func (u *UI) SetSynchronous() { u.spawn = func(f func()) { f() }; u.sync = true }

// Window returns the main window.
func (u *UI) Window() fyne.Window { return u.win }

// Start loads the signed-in accounts and opens the first one.
func (u *UI) Start() {
	u.setStatus("Loading accounts…")
	u.spawn(func() {
		ctx, cancel := context.WithTimeout(u.ctx, requestTimeout)
		defer cancel()
		accs, err := u.accts.List(ctx)
		fyne.Do(func() {
			if err != nil {
				u.setStatus("Could not load accounts: " + err.Error())
				return
			}
			u.setAccounts(accs, "")
		})
	})
}

func (u *UI) setAccounts(accs []AccountInfo, selectID string) {
	u.accounts = accs
	opts := make([]string, len(accs))
	sel := ""
	for i, a := range accs {
		opts[i] = accountLabel(a)
		if a.ID == selectID || (selectID == "" && i == 0) {
			sel = opts[i]
		}
	}
	u.accountSelect.SetOptions(opts)
	if len(accs) == 0 {
		u.current = nil
		u.chats = nil
		u.chatList.Refresh()
		u.clearMessages()
		u.signOut.Disable()
		u.newChat.Disable()
		u.statusSelect.Disable()
		u.accountSelect.ClearSelected()
		u.setStatus("Not signed in. Use “Add account” to sign in with your work account.")
		return
	}
	u.signOut.Enable()
	u.accountSelect.SetSelected(sel) // triggers onAccountSelected
}

func accountLabel(a AccountInfo) string { return a.Username }

func (u *UI) onAccountSelected(label string) {
	for _, a := range u.accounts {
		if accountLabel(a) == label {
			u.switchAccount(a)
			return
		}
	}
}

func (u *UI) switchAccount(a AccountInfo) {
	s := u.sessions[a.ID]
	if s == nil {
		s = &session{info: a, api: u.accts.API(a.ID)}
		u.sessions[a.ID] = s
	}
	u.current = s
	u.selected = ""
	u.chats = nil // drop the previous account's rows before any refresh
	u.chatList.UnselectAll()
	u.clearMessages()
	u.showOwnStatus(s)
	if s.store != nil {
		u.newChat.Enable()
		u.statusSelect.Enable()
		u.attachCalls(s)
		u.refreshChats()
		return
	}
	u.newChat.Disable()
	u.statusSelect.Disable()
	u.setStatus("Loading chats for " + a.Username + "…")
	u.spawn(func() { u.loadSession(s) })
}

func (u *UI) loadSession(s *session) {
	ctx, cancel := context.WithTimeout(u.ctx, requestTimeout)
	defer cancel()
	me, err := s.api.Me(ctx)
	if err != nil {
		fyne.Do(func() { u.setStatus(errorText("Sign-in check failed", err)) })
		return
	}
	st := store.New(me.ID)
	st.Subscribe(func(e store.Event) {
		fyne.Do(func() {
			if u.current != s {
				return
			}
			switch e.Kind {
			case store.ChatsChanged:
				u.refreshChats()
			case store.MessagesChanged:
				if e.ChatID == u.selected {
					u.renderMessages(false)
				}
			}
		})
	})
	chats, err := s.api.ListChats(ctx, 0)
	fyne.Do(func() {
		s.store = st
		if u.current == s {
			u.newChat.Enable()
			u.statusSelect.Enable()
		}
		if err != nil {
			u.setStatus(errorText("Could not load chats", err))
			return
		}
		u.setStatus("")
	})
	if err == nil {
		st.SetChats(chats) // emits ChatsChanged → refreshChats on main goroutine
		// fyne.Do is asynchronous and FIFO: this runs after s.store is set
		// above, and keeps stopLive/stopPresence on the main goroutine.
		fyne.Do(func() {
			u.startLive(s)
			u.startPresence(s)
			if u.current == s {
				u.attachCalls(s)
			}
		})
	}
}

func (u *UI) refreshChats() {
	if u.current == nil || u.current.store == nil {
		return
	}
	u.chats = u.current.store.Chats()
	u.chatList.Refresh()
	u.updateUnread()
	for i, c := range u.chats {
		if c.ID == u.selected {
			u.chatList.Select(i)
			break
		}
	}
}

func (u *UI) openChat(chatID string) {
	if u.current == nil || u.current.store == nil || chatID == u.selected {
		return
	}
	s := u.current
	u.selected = chatID
	c, _ := s.store.Chat(chatID)
	u.header.SetText(c.Title(s.store.Self()))
	u.composer.reset()
	u.composer.setEnabled(true)
	u.updateCallButtons()
	u.renderMessages(true)
	if c.Unread(s.store.Self()) {
		u.markRead(s, c)
	}

	if loaded, _ := s.store.State(chatID); loaded {
		return
	}
	u.setStatus("Loading messages…")
	u.spawn(func() {
		ctx, cancel := context.WithTimeout(u.ctx, requestTimeout)
		defer cancel()
		p, err := s.api.ListMessages(ctx, chatID, 50)
		if err != nil {
			fyne.Do(func() { u.setStatus(errorText("Could not load messages", err)) })
			return
		}
		fyne.Do(func() { u.setStatus("") })
		s.store.SetNewest(chatID, p)
		fyne.Do(func() {
			if u.selected == chatID && u.current == s {
				u.msgScroll.ScrollToBottom()
			}
		})
	})
}

func (u *UI) onLoadOlder() {
	s := u.current
	if s == nil || s.store == nil || u.selected == "" {
		return
	}
	chatID := u.selected
	_, older := s.store.State(chatID)
	if older == "" {
		return
	}
	u.olderBtn.Disable()
	u.spawn(func() {
		ctx, cancel := context.WithTimeout(u.ctx, requestTimeout)
		defer cancel()
		p, err := s.api.MoreMessages(ctx, older)
		fyne.Do(func() {
			u.olderBtn.Enable()
			if err != nil {
				u.setStatus(errorText("Could not load older messages", err))
			}
		})
		if err == nil {
			s.store.AddOlder(chatID, p)
		}
	})
}

func (u *UI) onLogin() {
	u.addAccount.Disable()
	u.setStatus("Complete the sign-in in your browser…")
	u.spawn(func() {
		ctx, cancel := context.WithTimeout(u.ctx, 5*time.Minute)
		defer cancel()
		acc, err := u.accts.Login(ctx)
		var accs []AccountInfo
		if err == nil {
			accs, err = u.accts.List(ctx)
		}
		fyne.Do(func() {
			u.addAccount.Enable()
			if err != nil {
				u.setStatus(errorText("Sign-in failed", err))
				return
			}
			u.setStatus("")
			u.setAccounts(accs, acc.ID)
		})
	})
}

func (u *UI) onLogout() {
	if u.current == nil {
		return
	}
	id := u.current.info.ID
	u.spawn(func() {
		ctx, cancel := context.WithTimeout(u.ctx, requestTimeout)
		defer cancel()
		err := u.accts.Logout(ctx, id)
		var accs []AccountInfo
		if err == nil {
			accs, err = u.accts.List(ctx)
		}
		fyne.Do(func() {
			if err != nil {
				u.setStatus(errorText("Sign-out failed", err))
				return
			}
			if s := u.sessions[id]; s != nil {
				if s.stopLive != nil {
					s.stopLive()
				}
				if s.stopPresence != nil {
					s.stopPresence()
				}
			}
			delete(u.sessions, id)
			u.setAccounts(accs, "")
		})
	})
}

func (u *UI) clearMessages() {
	u.composer.reset()
	u.composer.setEnabled(false)
	u.header.SetText("")
	u.msgBox.Objects = nil
	u.msgBox.Refresh()
	u.olderBtn.Hide()
}

// renderMessages rebuilds the history of the selected chat.
func (u *UI) renderMessages(scrollToEnd bool) {
	if u.current == nil || u.current.store == nil || u.selected == "" {
		return
	}
	st := u.current.store
	msgs := st.Messages(u.selected)
	objs := make([]fyne.CanvasObject, 0, len(msgs))
	for _, m := range msgs {
		objs = append(objs, newMessageItem(m, st.Self(), u.showMessageMenu, u.joinMeeting))
	}
	u.msgBox.Objects = objs
	u.msgBox.Refresh()
	if loaded, older := st.State(u.selected); loaded && older != "" {
		u.olderBtn.Show()
	} else {
		u.olderBtn.Hide()
	}
	if scrollToEnd {
		u.msgScroll.ScrollToBottom()
	}
}

func messageView(m graph.Message, self string, onJoin func(string)) fyne.CanvasObject {
	v := messageContent(m, self)
	if link, ok := meetingLinkOf(m); ok && onJoin != nil {
		join := widget.NewButtonWithIcon("Join meeting", theme.MediaVideoIcon(), func() { onJoin(link) })
		join.Importance = widget.HighImportance
		return container.NewVBox(v, container.NewHBox(join))
	}
	return v
}

func messageContent(m graph.Message, self string) fyne.CanvasObject {
	if m.MessageType != "" && m.MessageType != graph.MessageTypeMessage {
		l := widget.NewLabelWithStyle(systemText(m), fyne.TextAlignCenter, fyne.TextStyle{Italic: true})
		l.Importance = widget.LowImportance
		return l
	}
	name := m.From.DisplayName()
	if m.From.UserID() == self {
		name = "You"
	}
	meta := formatTime(m.CreatedDateTime)
	if m.IsEdited() && !m.IsDeleted() {
		meta += " · edited"
	}
	head := widget.NewRichText(
		&widget.TextSegment{Text: name + "  ", Style: widget.RichTextStyle{Inline: true, TextStyle: fyne.TextStyle{Bold: true}, ColorName: theme.ColorNameForeground}},
		&widget.TextSegment{Text: meta, Style: widget.RichTextStyle{ColorName: theme.ColorNameDisabled, SizeName: theme.SizeNameCaptionText}},
	)
	var body *widget.RichText
	if m.IsDeleted() {
		body = widget.NewRichText(&widget.TextSegment{Text: "This message was deleted.",
			Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Italic: true}, ColorName: theme.ColorNameDisabled}})
	} else {
		body = widget.NewRichText(segments(bodyDoc(m), m.Attachments)...)
	}
	body.Wrapping = fyne.TextWrapWord
	return container.NewVBox(head, body)
}

func bodyDoc(m graph.Message) render.Doc { return render.Body(m.Body.ContentType, m.Body.Content) }

func systemText(m graph.Message) string {
	if t := render.Body(m.Body.ContentType, m.Body.Content).PlainText(); t != "" {
		return t
	}
	return "System event"
}

func previewText(c graph.Chat) string {
	p := c.LastMessagePreview
	switch {
	case p == nil:
		return ""
	case p.IsDeleted:
		return "Message deleted"
	}
	text := render.Body(p.Body.ContentType, p.Body.Content).PlainText()
	if name := p.From.DisplayName(); name != "" {
		text = name + ": " + text
	}
	return firstLine(text)
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}

func formatTime(t time.Time) string {
	lt := t.Local()
	now := time.Now()
	if lt.Year() == now.Year() && lt.YearDay() == now.YearDay() {
		return lt.Format("15:04")
	}
	return lt.Format("02.01.2006 15:04")
}

func (u *UI) setStatus(s string) {
	u.status.SetText(s)
	if s == "" {
		u.status.Hide()
	} else {
		u.status.Show()
	}
}

func errorText(prefix string, err error) string {
	var ge *graph.Error
	if errors.As(err, &ge) && ge.Message != "" {
		return fmt.Sprintf("%s: %s", prefix, ge.Message)
	}
	return fmt.Sprintf("%s: %v", prefix, err)
}
