package graph

import "time"

// User is the subset of a Graph user acat displays.
type User struct {
	ID                string `json:"id"`
	DisplayName       string `json:"displayName"`
	Mail              string `json:"mail,omitempty"`
	UserPrincipalName string `json:"userPrincipalName,omitempty"`
}

// Identity is a Graph identity (user, application, device).
type Identity struct {
	ID               string `json:"id"`
	DisplayName      string `json:"displayName"`
	UserIdentityType string `json:"userIdentityType,omitempty"`
	TenantID         string `json:"tenantId,omitempty"`
}

// IdentitySet is the "from" of a message.
type IdentitySet struct {
	User        *Identity `json:"user,omitempty"`
	Application *Identity `json:"application,omitempty"`
}

// DisplayName returns the best available name.
func (s *IdentitySet) DisplayName() string {
	switch {
	case s == nil:
		return ""
	case s.User != nil:
		return s.User.DisplayName
	case s.Application != nil:
		return s.Application.DisplayName
	}
	return ""
}

// UserID returns the sender's user object ID, or "".
func (s *IdentitySet) UserID() string {
	if s == nil || s.User == nil {
		return ""
	}
	return s.User.ID
}

// Chat types.
const (
	ChatOneOnOne = "oneOnOne"
	ChatGroup    = "group"
	ChatMeeting  = "meeting"
)

// ChatMember is a conversationMember of type aadUserConversationMember.
type ChatMember struct {
	ID          string   `json:"id"`
	DisplayName string   `json:"displayName"`
	UserID      string   `json:"userId"`
	Email       string   `json:"email,omitempty"`
	TenantID    string   `json:"tenantId,omitempty"`
	Roles       []string `json:"roles,omitempty"`
}

// ChatViewpoint carries per-user read state.
type ChatViewpoint struct {
	IsHidden                bool       `json:"isHidden"`
	LastMessageReadDateTime *time.Time `json:"lastMessageReadDateTime,omitempty"`
}

// MessagePreview is chat.lastMessagePreview.
type MessagePreview struct {
	ID              string       `json:"id"`
	CreatedDateTime time.Time    `json:"createdDateTime"`
	IsDeleted       bool         `json:"isDeleted"`
	MessageType     string       `json:"messageType"`
	From            *IdentitySet `json:"from,omitempty"`
	Body            ItemBody     `json:"body"`
}

// Chat is a 1:1, group or meeting chat.
type Chat struct {
	ID                  string          `json:"id"`
	Topic               string          `json:"topic,omitempty"`
	ChatType            string          `json:"chatType"`
	CreatedDateTime     time.Time       `json:"createdDateTime"`
	LastUpdatedDateTime time.Time       `json:"lastUpdatedDateTime"`
	TenantID            string          `json:"tenantId,omitempty"`
	WebURL              string          `json:"webUrl,omitempty"`
	Members             []ChatMember    `json:"members,omitempty"`
	LastMessagePreview  *MessagePreview `json:"lastMessagePreview,omitempty"`
	Viewpoint           *ChatViewpoint  `json:"viewpoint,omitempty"`
}

// LastActivity is the timestamp used to sort the chat list.
func (c *Chat) LastActivity() time.Time {
	if c.LastMessagePreview != nil && c.LastMessagePreview.CreatedDateTime.After(c.LastUpdatedDateTime) {
		return c.LastMessagePreview.CreatedDateTime
	}
	return c.LastUpdatedDateTime
}

// Unread reports whether the last message is newer than the read marker
// and was not sent by selfID.
func (c *Chat) Unread(selfID string) bool {
	p := c.LastMessagePreview
	if p == nil || p.IsDeleted || p.From.UserID() == selfID {
		return false
	}
	if c.Viewpoint == nil || c.Viewpoint.LastMessageReadDateTime == nil {
		return true
	}
	return p.CreatedDateTime.After(*c.Viewpoint.LastMessageReadDateTime)
}

// Title returns the chat topic, or the other members' names.
func (c *Chat) Title(selfID string) string {
	if c.Topic != "" {
		return c.Topic
	}
	var names []string
	for _, m := range c.Members {
		if m.UserID == selfID || m.DisplayName == "" {
			continue
		}
		names = append(names, m.DisplayName)
	}
	switch len(names) {
	case 0:
		return "(chat)"
	case 1, 2, 3:
		return joinNames(names)
	default:
		return joinNames(names[:3]) + ", …"
	}
}

func joinNames(n []string) string {
	out := ""
	for i, s := range n {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

// Body content types.
const (
	ContentHTML = "html"
	ContentText = "text"
)

// ItemBody is a message body.
type ItemBody struct {
	ContentType string `json:"contentType"`
	Content     string `json:"content"`
}

// Mention references a mentioned entity inside a message body via
// <at id="N">…</at>.
type Mention struct {
	ID          int                `json:"id"`
	MentionText string             `json:"mentionText"`
	Mentioned   *MentionedIdentity `json:"mentioned,omitempty"`
}

// MentionedIdentity is the target of a mention.
type MentionedIdentity struct {
	User *Identity `json:"user,omitempty"`
}

// Attachment is a chatMessageAttachment.
type Attachment struct {
	ID          string `json:"id"`
	ContentType string `json:"contentType"`
	ContentURL  string `json:"contentUrl,omitempty"`
	Content     string `json:"content,omitempty"`
	Name        string `json:"name,omitempty"`
}

// Message types.
const (
	MessageTypeMessage = "message"
	MessageTypeSystem  = "systemEventMessage"
)

// Message is a chatMessage.
type Message struct {
	ID                   string       `json:"id"`
	ChatID               string       `json:"chatId,omitempty"`
	ReplyToID            string       `json:"replyToId,omitempty"`
	MessageType          string       `json:"messageType"`
	CreatedDateTime      time.Time    `json:"createdDateTime"`
	LastModifiedDateTime time.Time    `json:"lastModifiedDateTime"`
	LastEditedDateTime   *time.Time   `json:"lastEditedDateTime,omitempty"`
	DeletedDateTime      *time.Time   `json:"deletedDateTime,omitempty"`
	Subject              string       `json:"subject,omitempty"`
	Importance           string       `json:"importance,omitempty"`
	From                 *IdentitySet `json:"from,omitempty"`
	Body                 ItemBody     `json:"body"`
	Mentions             []Mention    `json:"mentions,omitempty"`
	Attachments          []Attachment `json:"attachments,omitempty"`
}

// IsDeleted reports whether the message was soft-deleted.
func (m *Message) IsDeleted() bool { return m.DeletedDateTime != nil }

// IsEdited reports whether the message was edited.
func (m *Message) IsEdited() bool { return m.LastEditedDateTime != nil }

// page is a Graph collection response.
type page[T any] struct {
	Value    []T    `json:"value"`
	NextLink string `json:"@odata.nextLink,omitempty"`
}
