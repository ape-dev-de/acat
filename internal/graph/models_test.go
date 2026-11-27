package graph

import (
	"encoding/json"
	"testing"
	"time"
)

// Trimmed from the Graph docs "List chats" example response.
const chatJSON = `{
  "id": "19:8b081ef6-4792-4def-b2c9-c363a1bf41d5_5031bb31-22c0-4f6f-9f73-91d34ab2b32d@unq.gbl.spaces",
  "topic": null,
  "createdDateTime": "2020-12-04T23:10:28.51Z",
  "lastUpdatedDateTime": "2020-12-04T23:10:36.925Z",
  "chatType": "oneOnOne",
  "viewpoint": {"isHidden": false, "lastMessageReadDateTime": "2021-03-28T21:00:00Z"},
  "lastMessagePreview": {
    "id": "1616964509832",
    "createdDateTime": "2021-03-28T21:00:00.000Z",
    "isDeleted": false,
    "messageType": "message",
    "body": {"contentType": "text", "content": "Hello world"},
    "from": {"user": {"id": "8b081ef6-4792-4def-b2c9-c363a1bf41d5", "displayName": "John Doe"}}
  },
  "members": [
    {"@odata.type": "#microsoft.graph.aadUserConversationMember", "id": "m1", "displayName": "John Doe", "userId": "8b081ef6-4792-4def-b2c9-c363a1bf41d5"},
    {"@odata.type": "#microsoft.graph.aadUserConversationMember", "id": "m2", "displayName": "Jane Roe", "userId": "5031bb31-22c0-4f6f-9f73-91d34ab2b32d"}
  ]
}`

func TestChatDecodeAndHelpers(t *testing.T) {
	var c Chat
	if err := json.Unmarshal([]byte(chatJSON), &c); err != nil {
		t.Fatal(err)
	}
	const john, jane = "8b081ef6-4792-4def-b2c9-c363a1bf41d5", "5031bb31-22c0-4f6f-9f73-91d34ab2b32d"
	if got := c.Title(jane); got != "John Doe" {
		t.Fatalf("title for Jane = %q", got)
	}
	if got := c.Title(john); got != "Jane Roe" {
		t.Fatalf("title for John = %q", got)
	}
	if !c.LastActivity().Equal(time.Date(2021, 3, 28, 21, 0, 0, 0, time.UTC)) {
		t.Fatalf("last activity %v", c.LastActivity())
	}
	// Read marker equals preview time → read.
	if c.Unread(jane) {
		t.Fatal("chat should be read")
	}
	later := c.LastMessagePreview.CreatedDateTime.Add(-time.Minute)
	c.Viewpoint.LastMessageReadDateTime = &later
	if !c.Unread(jane) {
		t.Fatal("chat should be unread for Jane")
	}
	if c.Unread(john) {
		t.Fatal("own message must not count as unread")
	}
}

func TestTitleTruncatesLargeGroups(t *testing.T) {
	c := Chat{Members: []ChatMember{
		{UserID: "me", DisplayName: "Me"}, {UserID: "1", DisplayName: "A"}, {UserID: "2", DisplayName: "B"},
		{UserID: "3", DisplayName: "C"}, {UserID: "4", DisplayName: "D"},
	}}
	if got := c.Title("me"); got != "A, B, C, …" {
		t.Fatalf("got %q", got)
	}
	c.Topic = "Project X"
	if got := c.Title("me"); got != "Project X" {
		t.Fatalf("got %q", got)
	}
}
