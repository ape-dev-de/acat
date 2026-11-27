package ui

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/ape-dev-de/acat/internal/graph"
	"github.com/ape-dev-de/acat/pkg/protocol"
)

// Calls is the calling backend (video helper or demo fake).
type Calls interface {
	Call(participants []string, video bool) error
	Join(meetingLink string, video bool) error
	Accept(callID string, video bool) error
	Reject(callID string) error
	Hangup(callID string) error
	Show() error
	Hide() error
	// OnEvent registers the event handler. Backends call it from any
	// goroutine; the UI marshals onto the main goroutine itself.
	OnEvent(func(protocol.VideoEvent))
}

// CallSource is optionally implemented by Accounts: it returns the calling
// backend for an account (nil when calls are unavailable, with a reason).
type CallSource interface {
	Calls(accountID string) (Calls, error)
}

// callState is the UI's view of the active call.
type callState struct {
	id    string
	title string
	state string
}

func (u *UI) buildCallUI() {
	u.callAudio = widget.NewButtonWithIcon("", theme.MediaRecordIcon(), func() { u.startCall(false) })
	u.callVideo = widget.NewButtonWithIcon("", theme.MediaVideoIcon(), func() { u.startCall(true) })
	u.callAudio.Importance = widget.LowImportance
	u.callVideo.Importance = widget.LowImportance
	u.callAudio.Disable()
	u.callVideo.Disable()

	u.callLabel = widget.NewLabel("")
	u.callLabel.Truncation = fyne.TextTruncateEllipsis
	showBtn := widget.NewButton("Show call", func() { u.withCalls(func(c Calls) error { return c.Show() }) })
	hangBtn := widget.NewButtonWithIcon("Hang up", theme.CancelIcon(), u.hangup)
	hangBtn.Importance = widget.DangerImportance
	u.callBar = container.NewBorder(nil, nil, nil, container.NewHBox(showBtn, hangBtn), u.callLabel)
	u.callBar.Hide()
}

// attachCalls connects the calling backend of the current account.
func (u *UI) attachCalls(s *session) {
	src, ok := u.accts.(CallSource)
	if !ok {
		return
	}
	if s.calls == nil {
		c, err := src.Calls(s.info.ID)
		if err != nil {
			u.callsUnavailable = err.Error()
			return
		}
		if c == nil {
			return
		}
		s.calls = c
		c.OnEvent(func(ev protocol.VideoEvent) { fyne.Do(func() { u.onCallEvent(s, ev) }) })
	}
	u.updateCallButtons()
}

func (u *UI) currentCalls() Calls {
	if u.current == nil {
		return nil
	}
	return u.current.calls
}

func (u *UI) updateCallButtons() {
	on := u.currentCalls() != nil && u.selected != "" && u.activeCall == nil
	for _, b := range []*widget.Button{u.callAudio, u.callVideo} {
		if on {
			b.Enable()
		} else {
			b.Disable()
		}
	}
}

func (u *UI) withCalls(fn func(Calls) error) {
	c := u.currentCalls()
	if c == nil {
		if u.callsUnavailable != "" {
			u.setStatus("Calls unavailable: " + u.callsUnavailable)
		}
		return
	}
	if err := fn(c); err != nil {
		u.setStatus(errorText("Call failed", err))
	}
}

// startCall calls the members of the selected chat.
func (u *UI) startCall(video bool) {
	s := u.current
	if s == nil || s.store == nil || u.selected == "" {
		return
	}
	ch, ok := s.store.Chat(u.selected)
	if !ok {
		return
	}
	var ids []string
	for _, m := range ch.Members {
		if m.UserID != "" && m.UserID != s.store.Self() {
			ids = append(ids, m.UserID)
		}
	}
	if len(ids) == 0 {
		return
	}
	u.activeCall = &callState{title: ch.Title(s.store.Self()), state: protocol.CallConnecting}
	u.renderCallBar()
	u.withCalls(func(c Calls) error {
		if err := c.Call(ids, video); err != nil {
			u.activeCall = nil
			u.renderCallBar()
			return err
		}
		return c.Show()
	})
}

// joinMeeting joins a Teams meeting from a link in a message.
func (u *UI) joinMeeting(link string) {
	u.activeCall = &callState{title: "Meeting", state: protocol.CallConnecting}
	u.renderCallBar()
	u.withCalls(func(c Calls) error {
		if err := c.Join(link, false); err != nil {
			u.activeCall = nil
			u.renderCallBar()
			return err
		}
		return c.Show()
	})
}

func (u *UI) hangup() {
	if u.activeCall == nil {
		return
	}
	id := u.activeCall.id
	u.withCalls(func(c Calls) error { return c.Hangup(id) })
}

func (u *UI) renderCallBar() {
	if u.activeCall == nil {
		u.callBar.Hide()
	} else {
		u.callLabel.SetText(fmt.Sprintf("Call · %s · %s", u.activeCall.title, u.activeCall.state))
		u.callBar.Show()
	}
	u.updateCallButtons()
}

func (u *UI) onCallEvent(s *session, ev protocol.VideoEvent) {
	switch ev.Type {
	case protocol.VideoIncomingCall:
		u.showIncomingCall(s, ev)
	case protocol.VideoCallState:
		if u.activeCall == nil {
			u.activeCall = &callState{title: "Call"}
		}
		if ev.CallID != "" {
			u.activeCall.id = ev.CallID
		}
		u.activeCall.state = ev.State
		if ev.State == protocol.CallDisconnected {
			u.activeCall = nil
			_ = s.calls.Hide()
		}
		u.renderCallBar()
	case protocol.VideoError:
		if ev.Message != "" {
			u.setStatus("Call: " + ev.Message)
		}
	}
}

func (u *UI) showIncomingCall(s *session, ev protocol.VideoEvent) {
	name := "Someone"
	if ev.Caller != nil && ev.Caller.DisplayName != "" {
		name = ev.Caller.DisplayName
	}
	u.app.SendNotification(fyne.NewNotification("Incoming call", name+" is calling"))
	u.win.Show()
	u.win.RequestFocus()

	var d dialog.Dialog
	answer := func(video bool) func() {
		return func() {
			d.Hide()
			u.activeCall = &callState{id: ev.CallID, title: name, state: protocol.CallConnecting}
			u.renderCallBar()
			if err := s.calls.Accept(ev.CallID, video); err != nil {
				u.setStatus(errorText("Could not accept call", err))
				return
			}
			_ = s.calls.Show()
		}
	}
	accept := widget.NewButtonWithIcon("Accept", theme.MediaRecordIcon(), answer(false))
	accept.Importance = widget.SuccessImportance
	acceptVideo := widget.NewButtonWithIcon("Video", theme.MediaVideoIcon(), answer(true))
	decline := widget.NewButtonWithIcon("Decline", theme.CancelIcon(), func() {
		d.Hide()
		_ = s.calls.Reject(ev.CallID)
	})
	decline.Importance = widget.DangerImportance
	content := container.NewVBox(
		widget.NewLabelWithStyle(name+" is calling", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		container.NewGridWithColumns(3, accept, acceptVideo, decline),
	)
	d = dialog.NewCustomWithoutButtons("Incoming call", content, u.win)
	d.Show()
}

// meetingLinkOf returns a Teams meeting link inside a message, if any.
func meetingLinkOf(m graph.Message) (string, bool) {
	if m.IsDeleted() {
		return "", false
	}
	return bodyDoc(m).MeetingLink()
}
