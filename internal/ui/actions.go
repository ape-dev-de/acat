package ui

import (
	"context"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/ape-dev-de/acat/internal/graph"
	"github.com/ape-dev-de/acat/internal/render"
)

// selectChat selects (and thereby opens) a chat in the list.
func (u *UI) selectChat(chatID string) {
	for i, c := range u.chats {
		if c.ID == chatID {
			u.chatList.Select(i)
			u.chatList.ScrollTo(i)
			return
		}
	}
}

// sendMessage sends, replies or edits, then updates the store.
func (u *UI) sendMessage(mode composeMode, target *graph.Message, m graph.NewMessage) {
	s := u.current
	chatID := u.selected
	if s == nil || s.store == nil || chatID == "" {
		return
	}
	u.spawn(func() {
		ctx, cancel := context.WithTimeout(u.ctx, requestTimeout)
		defer cancel()
		if mode == modeEdit && target != nil {
			err := s.api.EditMessage(ctx, chatID, target.ID, m)
			if err != nil {
				fyne.Do(func() { u.setStatus(errorText("Could not edit message", err)) })
				return
			}
			edited := *target
			now := time.Now().UTC()
			edited.Body, edited.Mentions, edited.LastEditedDateTime = m.Body, m.Mentions, &now
			s.store.Upsert(chatID, edited)
			return
		}
		sent, err := s.api.SendMessage(ctx, chatID, m)
		if err != nil {
			fyne.Do(func() { u.setStatus(errorText("Could not send message", err)) })
			return
		}
		if sent.CreatedDateTime.IsZero() {
			sent.CreatedDateTime = time.Now().UTC()
		}
		s.store.Upsert(chatID, sent)
		s.store.MarkRead(chatID) // own message: chat is read
		fyne.Do(func() {
			if u.selected == chatID {
				u.msgScroll.ScrollToBottom()
			}
		})
	})
}

func (u *UI) deleteMessage(m graph.Message) {
	s := u.current
	chatID := u.selected
	dialog.ShowConfirm("Delete message", "Delete this message for everyone?", func(ok bool) {
		if !ok {
			return
		}
		u.spawn(func() {
			ctx, cancel := context.WithTimeout(u.ctx, requestTimeout)
			defer cancel()
			if err := s.api.DeleteMessage(ctx, chatID, m.ID); err != nil {
				fyne.Do(func() { u.setStatus(errorText("Could not delete message", err)) })
				return
			}
			now := time.Now().UTC()
			m.DeletedDateTime = &now
			s.store.Upsert(chatID, m)
		})
	}, u.win)
}

// markRead marks a chat read locally and in Teams.
func (u *UI) markRead(s *session, c graph.Chat) {
	s.store.MarkRead(c.ID)
	u.spawn(func() {
		ctx, cancel := context.WithTimeout(u.ctx, requestTimeout)
		defer cancel()
		// Best effort: the read marker is cosmetic, errors are not shown.
		_ = s.api.MarkChatRead(ctx, c.ID, s.store.Self(), s.info.TenantID)
	})
}

// showMessageMenu is the context menu of a message.
func (u *UI) showMessageMenu(m graph.Message, pos fyne.Position) {
	if u.current == nil || u.current.store == nil || m.IsDeleted() || m.MessageType != graph.MessageTypeMessage {
		return
	}
	items := []*fyne.MenuItem{
		fyne.NewMenuItem("Reply with quote", func() { u.composer.reply(m) }),
		fyne.NewMenuItem("Copy text", func() { u.app.Clipboard().SetContent(plainBody(m)) }),
	}
	if m.From.UserID() == u.current.store.Self() {
		items = append(items,
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem("Edit", func() { u.composer.edit(m) }),
			fyne.NewMenuItem("Delete", func() { u.deleteMessage(m) }),
		)
	}
	widget.ShowPopUpMenuAtPosition(fyne.NewMenu("", items...), u.win.Canvas(), pos)
}

func plainBody(m graph.Message) string {
	return render.Body(m.Body.ContentType, m.Body.Content).PlainText()
}

// messageItem shows one message and opens the context menu on
// right-click (or long-press on touch devices).
type messageItem struct {
	widget.BaseWidget
	msg     graph.Message
	content fyne.CanvasObject
	onMenu  func(graph.Message, fyne.Position)
}

func newMessageItem(m graph.Message, self string, onMenu func(graph.Message, fyne.Position)) *messageItem {
	it := &messageItem{msg: m, content: messageView(m, self), onMenu: onMenu}
	it.ExtendBaseWidget(it)
	return it
}

func (it *messageItem) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(it.content)
}

// TappedSecondary implements fyne.SecondaryTappable.
func (it *messageItem) TappedSecondary(ev *fyne.PointEvent) {
	if it.onMenu != nil {
		it.onMenu(it.msg, ev.AbsolutePosition)
	}
}
