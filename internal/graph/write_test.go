package graph

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type captured struct {
	method, path, query string
	headers             http.Header
	body                map[string]any
}

// captureServer records the request and answers with status + resp.
func captureServer(t *testing.T, status int, resp string) (*Client, *captured) {
	t.Helper()
	cap := &captured{}
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.method, cap.path, cap.query, cap.headers, cap.body = r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Clone(), nil
		b, _ := io.ReadAll(r.Body)
		if len(b) > 0 {
			if err := json.Unmarshal(b, &cap.body); err != nil {
				t.Errorf("request body is not JSON: %s", b)
			}
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, resp)
	}))
	return c, cap
}

func asJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

// normJSON canonicalises a JSON literal (sorted keys, same escaping as asJSON).
func normJSON(t *testing.T, s string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	return asJSON(v)
}

func TestSendMessageWithMention(t *testing.T) {
	c, cap := captureServer(t, 201, `{"id":"1616990032035","body":{"contentType":"html","content":"x"}}`)
	m, err := c.SendMessage(context.Background(), "19:abc@thread.v2", NewMessage{
		Body: ItemBody{ContentType: ContentHTML, Content: `Hi <at id="0">Jane Roe</at>`},
		Mentions: []Mention{{ID: 0, MentionText: "Jane Roe", Mentioned: &MentionedIdentity{User: &Identity{
			ID: "u1", DisplayName: "Jane Roe", UserIdentityType: "aadUser"}}}},
	})
	if err != nil || m.ID != "1616990032035" {
		t.Fatalf("%+v %v", m, err)
	}
	if cap.method != "POST" || cap.path != "/v1.0/chats/19:abc@thread.v2/messages" {
		t.Fatalf("%s %s", cap.method, cap.path)
	}
	// Shape from the Graph docs "Send message in a chat with mention".
	want := normJSON(t, `{"body":{"content":"Hi <at id=\"0\">Jane Roe</at>","contentType":"html"},"mentions":[{"id":0,"mentioned":{"user":{"displayName":"Jane Roe","id":"u1","userIdentityType":"aadUser"}},"mentionText":"Jane Roe"}]}`)
	if got := asJSON(cap.body); got != want {
		t.Fatalf("body\n got %s\nwant %s", got, want)
	}
}

func TestEditAndDelete(t *testing.T) {
	c, cap := captureServer(t, 204, "")
	if err := c.EditMessage(context.Background(), "c1", "m1", NewMessage{Body: ItemBody{ContentType: ContentHTML, Content: "new"}}); err != nil {
		t.Fatal(err)
	}
	if cap.method != "PATCH" || cap.path != "/v1.0/chats/c1/messages/m1" || asJSON(cap.body) != `{"body":{"content":"new","contentType":"html"}}` {
		t.Fatalf("%s %s %s", cap.method, cap.path, asJSON(cap.body))
	}
	if err := c.DeleteMessage(context.Background(), "c1", "m1"); err != nil {
		t.Fatal(err)
	}
	if cap.method != "POST" || cap.path != "/v1.0/me/chats/c1/messages/m1/softDelete" {
		t.Fatalf("%s %s", cap.method, cap.path)
	}
	if err := c.UndoDeleteMessage(context.Background(), "c1", "m1"); err != nil || cap.path != "/v1.0/me/chats/c1/messages/m1/undoSoftDelete" {
		t.Fatalf("%s %v", cap.path, err)
	}
}

func TestCreateChat(t *testing.T) {
	c, cap := captureServer(t, 201, `{"id":"19:new","chatType":"oneOnOne"}`)
	ch, err := c.CreateChat(context.Background(), "me", []string{"me", "jane"}, "ignored")
	if err != nil || ch.ID != "19:new" {
		t.Fatalf("%+v %v", ch, err)
	}
	base := c.BaseURL
	want := `{"chatType":"oneOnOne","members":[` +
		`{"@odata.type":"#microsoft.graph.aadUserConversationMember","roles":["owner"],"user@odata.bind":"` + base + `/users('me')"},` +
		`{"@odata.type":"#microsoft.graph.aadUserConversationMember","roles":["owner"],"user@odata.bind":"` + base + `/users('jane')"}]}`
	if got := asJSON(cap.body); got != want {
		t.Fatalf("1:1 body\n got %s\nwant %s", got, want)
	}

	_, _ = c.CreateChat(context.Background(), "me", []string{"jane", "tom", "jane"}, "Project")
	if cap.body["chatType"] != "group" || cap.body["topic"] != "Project" || len(cap.body["members"].([]any)) != 3 {
		t.Fatalf("group body %s", asJSON(cap.body))
	}
}

func TestSearchUsers(t *testing.T) {
	c, cap := captureServer(t, 200, `{"value":[{"id":"u1","displayName":"Jane Roe","mail":"jane@contoso.example"}]}`)
	us, err := c.SearchUsers(context.Background(), ` ja"ne\ `, 0)
	if err != nil || len(us) != 1 || us[0].DisplayName != "Jane Roe" {
		t.Fatalf("%+v %v", us, err)
	}
	if cap.headers.Get("ConsistencyLevel") != "eventual" {
		t.Fatal("missing ConsistencyLevel header")
	}
	q := cap.query
	for _, want := range []string{`$search=%22displayName%3Ajane%22+OR+%22mail%3Ajane%22`, "$top=15", "$select=id,displayName,mail,userPrincipalName"} {
		if !strings.Contains(q, want) {
			t.Fatalf("query %q missing %q", q, want)
		}
	}
	if us, err := c.SearchUsers(context.Background(), `  "" `, 0); err != nil || us != nil {
		t.Fatal("empty query must not call Graph")
	}
}

func TestMarkReadUnread(t *testing.T) {
	c, cap := captureServer(t, 204, "")
	if err := c.MarkChatRead(context.Background(), "c1", "u1", "t1"); err != nil {
		t.Fatal(err)
	}
	if cap.path != "/v1.0/chats/c1/markChatReadForUser" || asJSON(cap.body) != `{"user":{"id":"u1","tenantId":"t1"}}` {
		t.Fatalf("%s %s", cap.path, asJSON(cap.body))
	}
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("X", 3600))
	if err := c.MarkChatUnread(context.Background(), "c1", "u1", "t1", ts); err != nil {
		t.Fatal(err)
	}
	if asJSON(cap.body) != `{"lastMessageReadDateTime":"2026-01-02T02:04:05Z","user":{"id":"u1","tenantId":"t1"}}` {
		t.Fatalf("%s", asJSON(cap.body))
	}
}

func TestMessageReference(t *testing.T) {
	a := MessageReference(Message{ID: "m1", Body: ItemBody{Content: "hello"}, From: &IdentitySet{User: &Identity{ID: "u", DisplayName: "Jane"}}})
	if a.ID != "m1" || a.ContentType != "messageReference" {
		t.Fatalf("%+v", a)
	}
	var c map[string]any
	if err := json.Unmarshal([]byte(a.Content), &c); err != nil || c["messageId"] != "m1" || c["messagePreview"] != "hello" {
		t.Fatalf("%s %v", a.Content, err)
	}
}
