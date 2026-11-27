package ui

import (
	"context"
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"

	"github.com/ape-dev-de/acat/internal/graph"
)

// PresenceInterval is how often chat partners' presence is polled.
const PresenceInterval = 60 * time.Second

const maxPresenceUsers = 100

// presenceColor maps an availability to a status dot color.
func presenceColor(p graph.Presence, ok bool) color.Color {
	if !ok {
		return color.Transparent
	}
	switch p.Availability {
	case graph.AvailAvailable, graph.AvailAvailableIdle:
		return theme.Color(theme.ColorNameSuccess)
	case graph.AvailBusy, graph.AvailBusyIdle, graph.AvailDoNotDisturb:
		return theme.Color(theme.ColorNameError)
	case graph.AvailAway, graph.AvailBeRightBack:
		return theme.Color(theme.ColorNameWarning)
	case graph.AvailOffline:
		return theme.Color(theme.ColorNameDisabled)
	}
	return color.Transparent
}

// partner returns the other member of a 1:1 chat.
func partner(c graph.Chat, self string) (string, bool) {
	if c.ChatType != graph.ChatOneOnOne {
		return "", false
	}
	for _, m := range c.Members {
		if m.UserID != "" && m.UserID != self {
			return m.UserID, true
		}
	}
	return "", false
}

// startPresence polls presence for the session's 1:1 partners.
func (u *UI) startPresence(s *session) {
	if s.stopPresence != nil {
		return
	}
	ctx, cancel := context.WithCancel(u.ctx)
	s.stopPresence = cancel
	poll := func() {
		var ids []string
		for _, c := range s.store.Chats() {
			if id, ok := partner(c, s.store.Self()); ok {
				ids = append(ids, id)
				if len(ids) == maxPresenceUsers {
					break
				}
			}
		}
		if len(ids) == 0 {
			return
		}
		rctx, rcancel := context.WithTimeout(ctx, requestTimeout)
		defer rcancel()
		ps, err := s.api.GetPresences(rctx, ids)
		if err != nil {
			return // presence is cosmetic; try again next round
		}
		fyne.Do(func() {
			s.presence = ps
			if u.current == s {
				u.chatList.Refresh()
			}
		})
	}
	if u.sync {
		poll()
		return
	}
	go func() {
		poll()
		t := time.NewTicker(PresenceInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				poll()
			}
		}
	}()
}

// statusOptions are the entries of the own-status selector.
func statusOptions() []string {
	opts := []string{statusAutomatic}
	for _, p := range graph.PreferredPresences {
		opts = append(opts, p.Label)
	}
	return opts
}

const statusAutomatic = "Automatic status"

func (u *UI) onStatusSelected(label string) {
	s := u.current
	if s == nil || s.store == nil || u.updatingStatus {
		return
	}
	u.spawn(func() {
		ctx, cancel := context.WithTimeout(u.ctx, requestTimeout)
		defer cancel()
		var err error
		if label == statusAutomatic {
			err = s.api.ClearPreferredPresence(ctx, s.store.Self())
		} else {
			for _, p := range graph.PreferredPresences {
				if p.Label == label {
					err = s.api.SetPreferredPresence(ctx, s.store.Self(), p, 0)
				}
			}
		}
		if err != nil {
			fyne.Do(func() { u.setStatus(errorText("Could not change status", err)) })
			return
		}
		fyne.Do(func() { s.statusLabel = label })
	})
}

// showOwnStatus reflects the session's last chosen status in the selector.
func (u *UI) showOwnStatus(s *session) {
	u.updatingStatus = true
	defer func() { u.updatingStatus = false }()
	if s == nil || s.statusLabel == "" {
		u.statusSelect.ClearSelected()
	} else {
		u.statusSelect.SetSelected(s.statusLabel)
	}
}
