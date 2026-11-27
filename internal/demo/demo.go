// Package demo provides an offline backend with sample data, so the UI can
// be developed and tested without a Microsoft 365 tenant (acat -demo).
package demo

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/ape-dev-de/acat/internal/graph"
	"github.com/ape-dev-de/acat/internal/ui"
)

const (
	meID   = "00000000-0000-0000-0000-00000000000a"
	janeID = "00000000-0000-0000-0000-00000000000b"
	tomID  = "00000000-0000-0000-0000-00000000000c"
)

// Accounts is a fake ui.Accounts with one pre-signed-in account.
type Accounts struct {
	mu       sync.Mutex
	accounts []ui.AccountInfo
	api      *API
}

// NewAccounts returns demo accounts.
func NewAccounts() *Accounts {
	return &Accounts{
		accounts: []ui.AccountInfo{{ID: "demo-account", Username: "you@contoso.example", TenantID: "contoso"}},
		api:      NewAPI(time.Now()),
	}
}

// List implements ui.Accounts.
func (a *Accounts) List(context.Context) ([]ui.AccountInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]ui.AccountInfo(nil), a.accounts...), nil
}

// Login implements ui.Accounts (adds a second demo tenant).
func (a *Accounts) Login(context.Context) (ui.AccountInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	acc := ui.AccountInfo{ID: "demo-" + strconv.Itoa(len(a.accounts)), Username: fmt.Sprintf("guest%d@fabrikam.example", len(a.accounts)), TenantID: "fabrikam"}
	a.accounts = append(a.accounts, acc)
	return acc, nil
}

// Logout implements ui.Accounts.
func (a *Accounts) Logout(_ context.Context, id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, x := range a.accounts {
		if x.ID == id {
			a.accounts = append(a.accounts[:i], a.accounts[i+1:]...)
			return nil
		}
	}
	return errors.New("demo: unknown account")
}

// API implements ui.Accounts.
func (a *Accounts) API(string) ui.ChatAPI { return a.api }

// API is a fake ui.ChatAPI.
type API struct {
	chats    []graph.Chat
	messages map[string][]graph.Message // newest first
}

var _ ui.ChatAPI = (*API)(nil)

// NewAPI builds sample data relative to now.
func NewAPI(now time.Time) *API {
	at := func(d time.Duration) time.Time { return now.Add(-d).UTC().Truncate(time.Second) }
	user := func(id, name string) *graph.IdentitySet {
		return &graph.IdentitySet{User: &graph.Identity{ID: id, DisplayName: name}}
	}
	me, jane, tom := user(meID, "You"), user(janeID, "Jane Roe"), user(tomID, "Tom Miller")
	html := func(s string) graph.ItemBody { return graph.ItemBody{ContentType: graph.ContentHTML, Content: s} }

	a := &API{messages: map[string][]graph.Message{}}
	add := func(chat graph.Chat, msgs ...graph.Message) {
		for i := range msgs {
			msgs[i].ChatID = chat.ID
			msgs[i].MessageType = graph.MessageTypeMessage
			msgs[i].LastModifiedDateTime = msgs[i].CreatedDateTime
		}
		// newest first, like Graph
		for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
			msgs[i], msgs[j] = msgs[j], msgs[i]
		}
		newest := msgs[0]
		chat.LastMessagePreview = &graph.MessagePreview{ID: newest.ID, CreatedDateTime: newest.CreatedDateTime, From: newest.From, Body: newest.Body, MessageType: newest.MessageType, IsDeleted: newest.IsDeleted()}
		chat.LastUpdatedDateTime = newest.CreatedDateTime
		a.chats = append(a.chats, chat)
		a.messages[chat.ID] = msgs
	}

	jane1on1 := graph.Chat{ID: "19:demo-jane@unq.gbl.spaces", ChatType: graph.ChatOneOnOne, Members: []graph.ChatMember{
		{UserID: meID, DisplayName: "You"}, {UserID: janeID, DisplayName: "Jane Roe"}}}
	readAt := at(3 * time.Hour)
	jane1on1.Viewpoint = &graph.ChatViewpoint{LastMessageReadDateTime: &readAt}
	add(jane1on1,
		graph.Message{ID: "j1", CreatedDateTime: at(26 * time.Hour), From: jane, Body: html("<p>Hi! Did you see the <b>release notes</b>?</p>")},
		graph.Message{ID: "j2", CreatedDateTime: at(25 * time.Hour), From: me, Body: html(`<p>Yes, here: <a href="https://example.org/releases">example.org/releases</a></p>`)},
		graph.Message{ID: "j3", CreatedDateTime: at(2 * time.Hour), From: jane, Body: html(`<blockquote itemtype="http://schema.skype.com/Reply"><strong>You</strong><p>Yes, here</p></blockquote><p>Thanks <emoji alt="👍"></emoji> Can you run this?</p><codeblock class="Go"><code>make build test lint</code></codeblock>`)},
		graph.Message{ID: "j4", CreatedDateTime: at(90 * time.Minute), From: jane, Body: html("<p>Agenda for tomorrow:</p><ol><li>Relay deployment</li><li>Spikes<ul><li>getAllMessages</li><li>ACS exchange</li></ul></li></ol>")},
	)

	team := graph.Chat{ID: "19:demo-group@thread.v2", Topic: "Project acat", ChatType: graph.ChatGroup, Members: []graph.ChatMember{
		{UserID: meID, DisplayName: "You"}, {UserID: janeID, DisplayName: "Jane Roe"}, {UserID: tomID, DisplayName: "Tom Miller"}}}
	var older []graph.Message
	for i := 1; i <= 60; i++ {
		older = append(older, graph.Message{ID: fmt.Sprintf("g%02d", i), CreatedDateTime: at(time.Duration(72*60-i) * time.Minute), From: tom,
			Body: graph.ItemBody{ContentType: graph.ContentText, Content: fmt.Sprintf("Backlog message %d", i)}})
	}
	edited := at(10 * time.Minute)
	deleted := at(5 * time.Minute)
	older = append(older,
		graph.Message{ID: "g90", CreatedDateTime: at(30 * time.Minute), From: tom, Body: html(`<p><at id="0">Jane Roe</at> can you <i>review</i> the <u>relay</u> PR? <s>Tomorrow</s> Today please.</p>`),
			Mentions: []graph.Mention{{ID: 0, MentionText: "Jane Roe"}}},
		graph.Message{ID: "g91", CreatedDateTime: at(20 * time.Minute), From: jane, LastEditedDateTime: &edited, Body: html(`<p>Sure, see <attachment id="att1"></attachment></p>`),
			Attachments: []graph.Attachment{{ID: "att1", ContentType: "reference", Name: "review-notes.docx"}}},
		graph.Message{ID: "g92", CreatedDateTime: at(6 * time.Minute), From: me, DeletedDateTime: &deleted},
	)
	add(team, older...)

	empty := graph.Chat{ID: "19:demo-empty@unq.gbl.spaces", ChatType: graph.ChatOneOnOne, LastUpdatedDateTime: at(48 * time.Hour),
		Members: []graph.ChatMember{{UserID: meID, DisplayName: "You"}, {UserID: tomID, DisplayName: "Tom Miller"}}}
	a.chats = append(a.chats, empty)
	return a
}

// Me implements ui.ChatAPI.
func (a *API) Me(context.Context) (graph.User, error) {
	return graph.User{ID: meID, DisplayName: "You", UserPrincipalName: "you@contoso.example"}, nil
}

// ListChats implements ui.ChatAPI.
func (a *API) ListChats(context.Context, int) ([]graph.Chat, error) {
	return append([]graph.Chat(nil), a.chats...), nil
}

const pageSize = 25

// ListMessages implements ui.ChatAPI (pages of 25, cursor "demo:<chat>:<offset>").
func (a *API) ListMessages(_ context.Context, chatID string, _ int) (graph.Page[graph.Message], error) {
	return a.page(chatID, 0), nil
}

// MoreMessages implements ui.ChatAPI.
func (a *API) MoreMessages(_ context.Context, next string) (graph.Page[graph.Message], error) {
	var chatID string
	var off int
	for i := len(next) - 1; i >= 0; i-- {
		if next[i] == ':' {
			chatID = next[len("demo:"):i]
			off, _ = strconv.Atoi(next[i+1:])
			break
		}
	}
	if chatID == "" {
		return graph.Page[graph.Message]{}, errors.New("demo: bad cursor")
	}
	return a.page(chatID, off), nil
}

func (a *API) page(chatID string, off int) graph.Page[graph.Message] {
	all := a.messages[chatID]
	if off >= len(all) {
		return graph.Page[graph.Message]{}
	}
	end := min(off+pageSize, len(all))
	p := graph.Page[graph.Message]{Items: append([]graph.Message(nil), all[off:end]...)}
	if end < len(all) {
		p.NextLink = "demo:" + chatID + ":" + strconv.Itoa(end)
	}
	return p
}
