package ui

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"

	"github.com/ape-dev-de/acat/internal/graph"
	"github.com/ape-dev-de/acat/internal/render"
)

// LiveHandler receives live-update callbacks (same shape as subs.Handler).
type LiveHandler interface {
	OnMessage(chatID, messageID, changeType string)
	OnResync()
	OnState(live bool, err error)
}

// LiveSource is optionally implemented by Accounts to push changes.
type LiveSource interface {
	// StartLive begins live updates for an account. The returned stop
	// function ends them (and deletes server-side subscriptions).
	StartLive(ctx context.Context, accountID, userID string, h LiveHandler) (stop func(), err error)
}

// liveHandler binds callbacks to one session.
type liveHandler struct {
	u *UI
	s *session
}

func (h liveHandler) OnMessage(chatID, messageID, changeType string) {
	u, s := h.u, h.s
	u.spawn(func() {
		ctx, cancel := context.WithTimeout(u.ctx, requestTimeout)
		defer cancel()
		m, err := s.api.GetMessage(ctx, chatID, messageID)
		if err != nil {
			if graph.IsStatus(err, http.StatusNotFound) {
				return // gone (hard-deleted or no access anymore)
			}
			fyne.Do(func() { u.setStatus(errorText("Could not load new message", err)) })
			return
		}
		if _, ok := s.store.Chat(chatID); !ok {
			if ch, err := s.api.GetChat(ctx, chatID); err == nil {
				s.store.UpsertChat(ch)
			}
		}
		s.store.Upsert(chatID, m)
		if changeType == "created" {
			fyne.Do(func() { u.onIncoming(s, chatID, m) })
		}
	})
}

func (h liveHandler) OnResync() {
	u, s := h.u, h.s
	u.spawn(func() {
		ctx, cancel := context.WithTimeout(u.ctx, requestTimeout)
		defer cancel()
		chats, err := s.api.ListChats(ctx, 0)
		if err != nil {
			return
		}
		s.store.SetChats(chats)
		var open string
		fyne.DoAndWait(func() {
			if u.current == s {
				open = u.selected
			}
		})
		if open == "" {
			return
		}
		if p, err := s.api.ListMessages(ctx, open, 50); err == nil {
			s.store.SetNewest(open, p)
		}
	})
}

const liveStatusPrefix = "Live updates unavailable"

func (h liveHandler) OnState(live bool, err error) {
	u, s := h.u, h.s
	fyne.Do(func() {
		if u.current != s {
			return
		}
		if live {
			if strings.HasPrefix(u.status.Text, liveStatusPrefix) {
				u.setStatus("")
			}
			return
		}
		if err != nil {
			u.setStatus(liveStatusPrefix + " (reconnecting): " + err.Error())
		}
	})
}

// startLive starts live updates for a freshly loaded session.
func (u *UI) startLive(s *session) {
	ls, ok := u.accts.(LiveSource)
	if !ok || s.stopLive != nil {
		return
	}
	stop, err := ls.StartLive(u.ctx, s.info.ID, s.store.Self(), liveHandler{u: u, s: s})
	if err != nil {
		fyne.Do(func() {
			if u.current == s {
				u.setStatus(liveStatusPrefix + ": " + err.Error())
			}
		})
		return
	}
	s.stopLive = stop
}

// onIncoming shows a desktop notification for a new message from someone
// else, unless the user is looking at that chat right now.
func (u *UI) onIncoming(s *session, chatID string, m graph.Message) {
	if m.MessageType != graph.MessageTypeMessage || m.IsDeleted() || m.From.UserID() == s.store.Self() {
		return
	}
	looking := u.focused && u.current == s && u.selected == chatID
	if looking {
		if c, ok := s.store.Chat(chatID); ok {
			u.markRead(s, c)
		}
		return
	}
	title := m.From.DisplayName()
	if c, ok := s.store.Chat(chatID); ok && c.ChatType != graph.ChatOneOnOne {
		title += " in " + c.Title(s.store.Self())
	}
	body := render.Body(m.Body.ContentType, m.Body.Content).PlainText()
	if r := []rune(body); len(r) > 160 {
		body = string(r[:160]) + "…"
	}
	u.app.SendNotification(fyne.NewNotification(title, body))
}

// updateUnread puts the number of unread chats into the window title.
func (u *UI) updateUnread() {
	n := 0
	if u.current != nil && u.current.store != nil {
		self := u.current.store.Self()
		for _, c := range u.chats {
			if c.Unread(self) {
				n++
			}
		}
	}
	title := "acat"
	if n > 0 {
		title += " (" + strconv.Itoa(n) + ")"
	}
	u.win.SetTitle(title)
}

// setupDesktop wires focus tracking and, where supported, a tray icon so
// closing the window keeps acat running for notifications.
func (u *UI) setupDesktop() {
	u.focused = true
	lc := u.app.Lifecycle()
	lc.SetOnEnteredForeground(func() {
		u.focused = true
		if u.current != nil && u.current.store != nil && u.selected != "" {
			if c, ok := u.current.store.Chat(u.selected); ok && c.Unread(u.current.store.Self()) {
				u.markRead(u.current, c)
			}
		}
	})
	lc.SetOnExitedForeground(func() { u.focused = false })
	lc.SetOnStopped(u.stopAllLive)

	if desk, ok := u.app.(desktop.App); ok {
		desk.SetSystemTrayMenu(fyne.NewMenu("acat",
			fyne.NewMenuItem("Show acat", func() { u.win.Show(); u.win.RequestFocus() }),
		))
		u.win.SetCloseIntercept(func() { u.win.Hide() })
	}
}

func (u *UI) stopAllLive() {
	for _, s := range u.sessions {
		if s.stopLive != nil {
			s.stopLive()
			s.stopLive = nil
		}
		if s.stopPresence != nil {
			s.stopPresence()
			s.stopPresence = nil
		}
	}
}
