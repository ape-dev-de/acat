package graph

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// NewMessage is the payload for sending a chat message.
type NewMessage struct {
	Body        ItemBody     `json:"body"`
	Mentions    []Mention    `json:"mentions,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// SendMessage posts a message to a chat and returns the created message.
func (c *Client) SendMessage(ctx context.Context, chatID string, m NewMessage) (Message, error) {
	var out Message
	err := c.do(ctx, request{method: http.MethodPost, path: "/chats/" + url.PathEscape(chatID) + "/messages", body: m}, &out)
	return out, err
}

// EditMessage replaces the body (and mentions) of one of the user's own
// messages.
func (c *Client) EditMessage(ctx context.Context, chatID, messageID string, m NewMessage) error {
	path := "/chats/" + url.PathEscape(chatID) + "/messages/" + url.PathEscape(messageID)
	return c.do(ctx, request{method: http.MethodPatch, path: path, body: m}, nil)
}

// DeleteMessage soft-deletes one of the user's own messages.
func (c *Client) DeleteMessage(ctx context.Context, chatID, messageID string) error {
	path := "/me/chats/" + url.PathEscape(chatID) + "/messages/" + url.PathEscape(messageID) + "/softDelete"
	return c.do(ctx, request{method: http.MethodPost, path: path}, nil)
}

// UndoDeleteMessage restores a soft-deleted message.
func (c *Client) UndoDeleteMessage(ctx context.Context, chatID, messageID string) error {
	path := "/me/chats/" + url.PathEscape(chatID) + "/messages/" + url.PathEscape(messageID) + "/undoSoftDelete"
	return c.do(ctx, request{method: http.MethodPost, path: path}, nil)
}

// MessageReference builds the attachment Teams uses to quote another
// message in a chat. Put <attachment id="{ID}"></attachment> into the body.
// (v1.0 support verified in spike M1.5.)
func MessageReference(quoted Message) Attachment {
	preview := []rune(strings.TrimSpace(quoted.Body.Content))
	if len(preview) > 200 {
		preview = preview[:200]
	}
	content, _ := json.Marshal(struct {
		MessageID      string       `json:"messageId"`
		MessagePreview string       `json:"messagePreview"`
		MessageSender  *IdentitySet `json:"messageSender,omitempty"`
	}{quoted.ID, string(preview), quoted.From})
	return Attachment{ID: quoted.ID, ContentType: "messageReference", Content: string(content)}
}

type memberBind struct {
	Type     string   `json:"@odata.type"`
	Roles    []string `json:"roles"`
	UserBind string   `json:"user@odata.bind"`
}

// CreateChat creates a 1:1 chat (exactly one other user, topic ignored) or
// a group chat. selfID must be included by the caller in userIDs or is
// added automatically. For 1:1 chats Graph returns the existing chat if
// there already is one.
func (c *Client) CreateChat(ctx context.Context, selfID string, userIDs []string, topic string) (Chat, error) {
	ids := []string{selfID}
	for _, id := range userIDs {
		if id != selfID && !contains(ids, id) {
			ids = append(ids, id)
		}
	}
	chatType := ChatGroup
	if len(ids) == 2 {
		chatType, topic = ChatOneOnOne, ""
	}
	base := strings.TrimRight(c.BaseURL, "/")
	members := make([]memberBind, 0, len(ids))
	for _, id := range ids {
		members = append(members, memberBind{
			Type:     "#microsoft.graph.aadUserConversationMember",
			Roles:    []string{"owner"},
			UserBind: base + "/users('" + id + "')",
		})
	}
	body := struct {
		ChatType string       `json:"chatType"`
		Topic    string       `json:"topic,omitempty"`
		Members  []memberBind `json:"members"`
	}{chatType, topic, members}
	var out Chat
	err := c.do(ctx, request{method: http.MethodPost, path: "/chats", body: body}, &out)
	return out, err
}

// SearchUsers finds people in the user's tenant by name or e-mail prefix.
func (c *Client) SearchUsers(ctx context.Context, query string, top int) ([]User, error) {
	q := strings.Map(func(r rune) rune {
		if r == '"' || r == '\\' {
			return -1
		}
		return r
	}, strings.TrimSpace(query))
	if q == "" {
		return nil, nil
	}
	if top <= 0 || top > 25 {
		top = 15
	}
	params := url.Values{
		"$search": {`"displayName:` + q + `" OR "mail:` + q + `"`},
		"$select": {"id,displayName,mail,userPrincipalName"},
		"$top":    {strconv.Itoa(top)},
	}
	var p page[User]
	err := c.do(ctx, request{
		method: http.MethodGet, path: "/users", query: params,
		headers: map[string]string{"ConsistencyLevel": "eventual"},
	}, &p)
	return p.Value, err
}

type readUser struct {
	ID       string `json:"id"`
	TenantID string `json:"tenantId"`
}

// MarkChatRead marks a chat as read for the user.
func (c *Client) MarkChatRead(ctx context.Context, chatID, userID, tenantID string) error {
	body := struct {
		User readUser `json:"user"`
	}{readUser{userID, tenantID}}
	return c.do(ctx, request{method: http.MethodPost, path: "/chats/" + url.PathEscape(chatID) + "/markChatReadForUser", body: body}, nil)
}

// MarkChatUnread marks a chat as unread from lastRead on.
func (c *Client) MarkChatUnread(ctx context.Context, chatID, userID, tenantID string, lastRead time.Time) error {
	body := struct {
		User                    readUser  `json:"user"`
		LastMessageReadDateTime time.Time `json:"lastMessageReadDateTime"`
	}{readUser{userID, tenantID}, lastRead.UTC()}
	return c.do(ctx, request{method: http.MethodPost, path: "/chats/" + url.PathEscape(chatID) + "/markChatUnreadForUser", body: body}, nil)
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
