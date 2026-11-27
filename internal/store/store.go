// Package store keeps one account's chats and messages in memory only.
// Nothing is persisted: closing acat forgets all message content.
package store

import (
	"sort"
	"sync"

	"github.com/ape-dev-de/acat/internal/graph"
)

// EventKind says what changed.
type EventKind int

// Event kinds.
const (
	ChatsChanged    EventKind = iota // chat list order/content changed
	MessagesChanged                  // messages of ChatID changed
)

// Event is delivered to subscribers after a change.
type Event struct {
	Kind   EventKind
	ChatID string
}

type thread struct {
	msgs     []graph.Message // oldest → newest
	index    map[string]int  // message ID → position in msgs
	older    string          // nextLink to older messages
	loaded   bool            // newest page loaded
	complete bool            // no older messages exist
}

// Store is safe for concurrent use. Subscribers are called synchronously
// after the lock is released; they must not block.
type Store struct {
	mu      sync.Mutex
	self    string
	chats   map[string]graph.Chat
	threads map[string]*thread
	subs    []func(Event)
}

// New creates an empty store for the user with object ID self.
func New(self string) *Store {
	return &Store{self: self, chats: map[string]graph.Chat{}, threads: map[string]*thread{}}
}

// Self returns the signed-in user's object ID.
func (s *Store) Self() string { return s.self }

// Subscribe registers fn for change events.
func (s *Store) Subscribe(fn func(Event)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subs = append(s.subs, fn)
}

func (s *Store) emit(evs ...Event) {
	s.mu.Lock()
	subs := append([]func(Event){}, s.subs...)
	s.mu.Unlock()
	for _, e := range evs {
		for _, fn := range subs {
			fn(e)
		}
	}
}

// SetChats replaces the chat list.
func (s *Store) SetChats(chats []graph.Chat) {
	s.mu.Lock()
	s.chats = make(map[string]graph.Chat, len(chats))
	for _, c := range chats {
		s.chats[c.ID] = c
	}
	s.mu.Unlock()
	s.emit(Event{Kind: ChatsChanged})
}

// UpsertChat adds or replaces one chat.
func (s *Store) UpsertChat(c graph.Chat) {
	s.mu.Lock()
	s.chats[c.ID] = c
	s.mu.Unlock()
	s.emit(Event{Kind: ChatsChanged})
}

// Chats returns all chats, most recent activity first.
func (s *Store) Chats() []graph.Chat {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]graph.Chat, 0, len(s.chats))
	for _, c := range s.chats {
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ai, aj := out[i].LastActivity(), out[j].LastActivity()
		if !ai.Equal(aj) {
			return ai.After(aj)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Chat returns one chat.
func (s *Store) Chat(id string) (graph.Chat, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.chats[id]
	return c, ok
}

func (s *Store) thread(chatID string) *thread {
	t := s.threads[chatID]
	if t == nil {
		t = &thread{index: map[string]int{}}
		s.threads[chatID] = t
	}
	return t
}

// SetNewest stores the newest page of a chat (as returned by Graph,
// newest first). Messages already known (e.g. from notifications) are kept.
func (s *Store) SetNewest(chatID string, p graph.Page[graph.Message]) {
	s.mu.Lock()
	t := s.thread(chatID)
	for _, m := range p.Items {
		t.put(m)
	}
	t.sort()
	t.loaded = true
	t.older = p.NextLink
	t.complete = p.NextLink == ""
	s.mu.Unlock()
	s.emit(Event{Kind: MessagesChanged, ChatID: chatID})
}

// AddOlder stores a page of older messages.
func (s *Store) AddOlder(chatID string, p graph.Page[graph.Message]) {
	s.mu.Lock()
	t := s.thread(chatID)
	for _, m := range p.Items {
		t.put(m)
	}
	t.sort()
	t.older = p.NextLink
	t.complete = p.NextLink == ""
	s.mu.Unlock()
	s.emit(Event{Kind: MessagesChanged, ChatID: chatID})
}

// Upsert adds or updates a single message (new, edited, deleted) and
// refreshes the chat's last-message preview.
func (s *Store) Upsert(chatID string, m graph.Message) {
	s.mu.Lock()
	t := s.thread(chatID)
	t.put(m)
	t.sort()
	if c, ok := s.chats[chatID]; ok && (c.LastMessagePreview == nil || !m.CreatedDateTime.Before(c.LastMessagePreview.CreatedDateTime)) {
		c.LastMessagePreview = &graph.MessagePreview{
			ID: m.ID, CreatedDateTime: m.CreatedDateTime, IsDeleted: m.IsDeleted(),
			MessageType: m.MessageType, From: m.From, Body: m.Body,
		}
		s.chats[chatID] = c
	}
	s.mu.Unlock()
	s.emit(Event{Kind: MessagesChanged, ChatID: chatID}, Event{Kind: ChatsChanged})
}

// Messages returns a chat's messages, oldest first.
func (s *Store) Messages(chatID string) []graph.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.threads[chatID]
	if t == nil {
		return nil
	}
	return append([]graph.Message(nil), t.msgs...)
}

// State reports whether the newest page is loaded and the link to older
// messages ("" when complete or not loaded).
func (s *Store) State(chatID string) (loaded bool, older string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.threads[chatID]
	if t == nil {
		return false, ""
	}
	return t.loaded, t.older
}

// MarkRead sets the local read marker (the Graph call is the caller's job).
func (s *Store) MarkRead(chatID string) {
	s.mu.Lock()
	c, ok := s.chats[chatID]
	if ok && c.LastMessagePreview != nil {
		ts := c.LastMessagePreview.CreatedDateTime
		vp := graph.ChatViewpoint{}
		if c.Viewpoint != nil {
			vp = *c.Viewpoint
		}
		vp.LastMessageReadDateTime = &ts
		c.Viewpoint = &vp
		s.chats[chatID] = c
	}
	s.mu.Unlock()
	if ok {
		s.emit(Event{Kind: ChatsChanged})
	}
}

func (t *thread) put(m graph.Message) {
	if i, ok := t.index[m.ID]; ok {
		t.msgs[i] = m
		return
	}
	t.index[m.ID] = len(t.msgs)
	t.msgs = append(t.msgs, m)
}

func (t *thread) sort() {
	sort.SliceStable(t.msgs, func(i, j int) bool {
		a, b := t.msgs[i], t.msgs[j]
		if !a.CreatedDateTime.Equal(b.CreatedDateTime) {
			return a.CreatedDateTime.Before(b.CreatedDateTime)
		}
		return a.ID < b.ID
	})
	for i, m := range t.msgs {
		t.index[m.ID] = i
	}
}
