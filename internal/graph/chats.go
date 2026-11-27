package graph

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strconv"
)

// Page is one page of a collection plus the cursor to the next one.
type Page[T any] struct {
	Items    []T
	NextLink string // empty when there are no more pages
}

func getPage[T any](ctx context.Context, c *Client, path string, q url.Values) (Page[T], error) {
	var p page[T]
	if err := c.do(ctx, request{method: http.MethodGet, path: path, query: q}, &p); err != nil {
		return Page[T]{}, err
	}
	return Page[T]{Items: p.Value, NextLink: p.NextLink}, nil
}

// all follows @odata.nextLink until the collection is exhausted or limit
// items were collected (limit <= 0 means no limit).
func all[T any](ctx context.Context, c *Client, path string, q url.Values, limit int) ([]T, error) {
	var out []T
	p, err := getPage[T](ctx, c, path, q)
	for {
		if err != nil {
			return out, err
		}
		out = append(out, p.Items...)
		if p.NextLink == "" || (limit > 0 && len(out) >= limit) {
			if limit > 0 && len(out) > limit {
				out = out[:limit]
			}
			return out, nil
		}
		p, err = getPage[T](ctx, c, p.NextLink, nil)
	}
}

// Me returns the signed-in user.
func (c *Client) Me(ctx context.Context) (User, error) {
	var u User
	q := url.Values{"$select": {"id,displayName,mail,userPrincipalName"}}
	err := c.do(ctx, request{method: http.MethodGet, path: "/me", query: q}, &u)
	return u, err
}

// ListChats returns the user's chats with members and last message
// preview, most recent activity first. limit <= 0 loads all pages.
func (c *Client) ListChats(ctx context.Context, limit int) ([]Chat, error) {
	q := url.Values{
		"$expand":  {"members,lastMessagePreview"},
		"$orderby": {"lastMessagePreview/createdDateTime desc"},
		"$top":     {"50"},
	}
	chats, err := all[Chat](ctx, c, "/me/chats", q, limit)
	if err != nil {
		return chats, err
	}
	// Graph orders by preview time; chats without messages are mixed in.
	sort.SliceStable(chats, func(i, j int) bool { return chats[i].LastActivity().After(chats[j].LastActivity()) })
	return chats, nil
}

// GetChat returns one chat with members and last message preview.
func (c *Client) GetChat(ctx context.Context, chatID string) (Chat, error) {
	var ch Chat
	q := url.Values{"$expand": {"members,lastMessagePreview"}}
	err := c.do(ctx, request{method: http.MethodGet, path: "/chats/" + url.PathEscape(chatID), query: q}, &ch)
	return ch, err
}

// ListMessages returns the newest messages of a chat (newest first).
// Pass the returned NextLink to MoreMessages to load older ones.
func (c *Client) ListMessages(ctx context.Context, chatID string, top int) (Page[Message], error) {
	if top <= 0 || top > 50 {
		top = 50 // Graph maximum for chat messages
	}
	q := url.Values{
		"$top":     {strconv.Itoa(top)},
		"$orderby": {"createdDateTime desc"},
	}
	return getPage[Message](ctx, c, "/chats/"+url.PathEscape(chatID)+"/messages", q)
}

// MoreMessages loads the page behind a NextLink from ListMessages.
func (c *Client) MoreMessages(ctx context.Context, nextLink string) (Page[Message], error) {
	return getPage[Message](ctx, c, nextLink, nil)
}

// GetMessage loads one message (used after a change notification).
func (c *Client) GetMessage(ctx context.Context, chatID, messageID string) (Message, error) {
	var m Message
	path := "/chats/" + url.PathEscape(chatID) + "/messages/" + url.PathEscape(messageID)
	err := c.do(ctx, request{method: http.MethodGet, path: path}, &m)
	return m, err
}
