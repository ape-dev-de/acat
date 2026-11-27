package ui

import (
	"context"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/ape-dev-de/acat/internal/graph"
)

const searchDebounce = 300 * time.Millisecond

// showNewChat opens the "new chat" dialog: search people, pick one (1:1)
// or several (group, optional topic), create, open.
func (u *UI) showNewChat() {
	s := u.current
	if s == nil || s.store == nil {
		return
	}
	var (
		results  []graph.User
		picked   []graph.User
		timer    *time.Timer
		searchID int
	)

	pickedLabel := widget.NewLabel("Nobody selected")
	pickedLabel.Wrapping = fyne.TextWrapWord
	topic := widget.NewEntry()
	topic.SetPlaceHolder("Group name (optional)")
	topic.Hide()

	updatePicked := func() {
		names := make([]string, len(picked))
		for i, p := range picked {
			names[i] = p.DisplayName
		}
		if len(names) == 0 {
			pickedLabel.SetText("Nobody selected")
		} else {
			pickedLabel.SetText("To: " + strings.Join(names, ", "))
		}
		if len(picked) > 1 {
			topic.Show()
		} else {
			topic.Hide()
		}
	}

	list := widget.NewList(
		func() int { return len(results) },
		func() fyne.CanvasObject { return widget.NewLabel("name") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i >= len(results) {
				return
			}
			r := results[i]
			label := r.DisplayName
			if r.Mail != "" {
				label += "  <" + r.Mail + ">"
			}
			o.(*widget.Label).SetText(label)
		},
	)
	list.OnSelected = func(i widget.ListItemID) {
		list.UnselectAll()
		if i >= len(results) || results[i].ID == s.store.Self() {
			return
		}
		for _, p := range picked {
			if p.ID == results[i].ID {
				return
			}
		}
		picked = append(picked, results[i])
		updatePicked()
	}

	search := widget.NewEntry()
	search.SetPlaceHolder("Search people by name or e-mail")
	search.OnChanged = func(q string) {
		if timer != nil {
			timer.Stop()
		}
		searchID++
		id := searchID
		timer = time.AfterFunc(searchDebounce, func() {
			u.spawn(func() {
				ctx, cancel := context.WithTimeout(u.ctx, requestTimeout)
				defer cancel()
				users, err := s.api.SearchUsers(ctx, q, 15)
				fyne.Do(func() {
					if id != searchID {
						return // a newer search is running
					}
					if err != nil {
						u.setStatus(errorText("Search failed", err))
						return
					}
					results = users
					list.Refresh()
				})
			})
		})
	}

	clear := widget.NewButton("Clear", func() { picked = nil; updatePicked() })
	content := container.NewBorder(
		container.NewVBox(search, container.NewBorder(nil, nil, nil, clear, pickedLabel), topic),
		nil, nil, nil, list,
	)
	d := dialog.NewCustomConfirm("New chat", "Create", "Cancel", content, func(ok bool) {
		if !ok || len(picked) == 0 {
			return
		}
		ids := make([]string, len(picked))
		for i, p := range picked {
			ids[i] = p.ID
		}
		u.createChat(s, ids, strings.TrimSpace(topic.Text))
	}, u.win)
	d.Resize(fyne.NewSize(520, 480))
	d.Show()
	u.win.Canvas().Focus(search)
}

func (u *UI) createChat(s *session, userIDs []string, topic string) {
	u.setStatus("Creating chat…")
	u.spawn(func() {
		ctx, cancel := context.WithTimeout(u.ctx, requestTimeout)
		defer cancel()
		ch, err := s.api.CreateChat(ctx, s.store.Self(), userIDs, topic)
		if err == nil && len(ch.Members) == 0 {
			// POST /chats does not return members; load the full chat.
			if full, gerr := s.api.GetChat(ctx, ch.ID); gerr == nil {
				ch = full
			}
		}
		if err != nil {
			fyne.Do(func() { u.setStatus(errorText("Could not create chat", err)) })
			return
		}
		s.store.UpsertChat(ch)
		fyne.Do(func() {
			u.setStatus("")
			if u.current == s {
				u.selectChat(ch.ID)
			}
		})
	})
}
