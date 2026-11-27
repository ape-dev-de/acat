package store

import (
	"testing"
	"time"

	"github.com/ape-dev-de/acat/internal/graph"
)

func ts(min int) time.Time { return time.Date(2026, 1, 1, 12, min, 0, 0, time.UTC) }

func msg(id string, min int, from string) graph.Message {
	return graph.Message{ID: id, CreatedDateTime: ts(min), MessageType: graph.MessageTypeMessage,
		From: &graph.IdentitySet{User: &graph.Identity{ID: from}}, Body: graph.ItemBody{ContentType: "text", Content: id}}
}

func ids(ms []graph.Message) string {
	s := ""
	for _, m := range ms {
		s += m.ID
	}
	return s
}

func TestPagesMergeOldestFirst(t *testing.T) {
	s := New("me")
	var events []Event
	s.Subscribe(func(e Event) { events = append(events, e) })

	s.SetNewest("c", graph.Page[graph.Message]{Items: []graph.Message{msg("d", 4, "x"), msg("c", 3, "x")}, NextLink: "older"})
	if got := ids(s.Messages("c")); got != "cd" {
		t.Fatalf("got %s", got)
	}
	loaded, older := s.State("c")
	if !loaded || older != "older" {
		t.Fatalf("state %v %q", loaded, older)
	}
	s.AddOlder("c", graph.Page[graph.Message]{Items: []graph.Message{msg("b", 2, "x"), msg("a", 1, "x")}})
	if got := ids(s.Messages("c")); got != "abcd" {
		t.Fatalf("got %s", got)
	}
	if _, older := s.State("c"); older != "" {
		t.Fatal("should be complete")
	}
	if len(events) != 2 || events[0].Kind != MessagesChanged || events[0].ChatID != "c" {
		t.Fatalf("events %+v", events)
	}
}

func TestUpsertUpdatesInPlaceAndPreview(t *testing.T) {
	s := New("me")
	s.SetChats([]graph.Chat{{ID: "c", LastUpdatedDateTime: ts(0)}, {ID: "other", LastUpdatedDateTime: ts(10)}})
	s.SetNewest("c", graph.Page[graph.Message]{Items: []graph.Message{msg("a", 1, "x")}})

	s.Upsert("c", msg("b", 20, "x"))
	if s.Chats()[0].ID != "c" {
		t.Fatal("chat with new message should be first")
	}
	edited := msg("a", 1, "x")
	edited.Body.Content = "edited"
	s.Upsert("c", edited)
	ms := s.Messages("c")
	if ids(ms) != "ab" || ms[0].Body.Content != "edited" {
		t.Fatalf("got %+v", ms)
	}
	c, _ := s.Chat("c")
	if c.LastMessagePreview.ID != "b" {
		t.Fatal("older edit must not replace the preview")
	}
}

func TestMarkRead(t *testing.T) {
	s := New("me")
	s.SetChats([]graph.Chat{{ID: "c", LastMessagePreview: &graph.MessagePreview{ID: "m", CreatedDateTime: ts(5),
		From: &graph.IdentitySet{User: &graph.Identity{ID: "other"}}}}})
	c, _ := s.Chat("c")
	if !c.Unread("me") {
		t.Fatal("should be unread")
	}
	s.MarkRead("c")
	c, _ = s.Chat("c")
	if c.Unread("me") {
		t.Fatal("should be read")
	}
}

func TestNotificationBeforeInitialLoadIsKept(t *testing.T) {
	s := New("me")
	s.Upsert("c", msg("z", 30, "x"))
	s.SetNewest("c", graph.Page[graph.Message]{Items: []graph.Message{msg("b", 2, "x")}})
	if got := ids(s.Messages("c")); got != "bz" {
		t.Fatalf("got %s", got)
	}
}
