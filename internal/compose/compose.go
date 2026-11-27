// Package compose turns composer input (plain text plus @mentions picked
// from suggestions) into a Graph chat message.
package compose

import (
	"html"
	"sort"
	"strconv"
	"strings"

	"github.com/ape-dev-de/acat/internal/graph"
)

// Person is someone who can be mentioned.
type Person struct {
	ID          string
	DisplayName string
}

// Input is what the user typed. Mentions are the people picked from the
// suggestion popup; each was inserted into Text as "@" + DisplayName.
type Input struct {
	Text     string
	Mentions []Person
	Quote    *graph.Message // optional quoted message
}

// Empty reports whether there is nothing to send.
func (in Input) Empty() bool { return strings.TrimSpace(in.Text) == "" && in.Quote == nil }

// Build converts the input to a Graph message. Text is HTML-escaped and
// line breaks become <br>. Every "@DisplayName" occurrence of a picked
// person becomes a mention; longer names win when names overlap.
func Build(in Input) graph.NewMessage {
	text := strings.TrimSpace(strings.ReplaceAll(in.Text, "\r\n", "\n"))

	people := append([]Person(nil), in.Mentions...)
	sort.SliceStable(people, func(i, j int) bool { return len(people[i].DisplayName) > len(people[j].DisplayName) })

	var sb strings.Builder
	var mentions []graph.Mention
	if in.Quote != nil {
		sb.WriteString(`<attachment id="` + html.EscapeString(in.Quote.ID) + `"></attachment>`)
	}
	for len(text) > 0 {
		i := strings.IndexByte(text, '@')
		if i < 0 {
			sb.WriteString(escape(text))
			break
		}
		sb.WriteString(escape(text[:i]))
		rest := text[i+1:]
		matched := false
		atBoundary := i == 0 || strings.ContainsRune(" \n(", rune(text[i-1]))
		for _, p := range people {
			if !atBoundary {
				break
			}
			if p.DisplayName != "" && strings.HasPrefix(rest, p.DisplayName) {
				id := len(mentions)
				sb.WriteString(`<at id="` + strconv.Itoa(id) + `">` + html.EscapeString(p.DisplayName) + `</at>`)
				mentions = append(mentions, graph.Mention{
					ID: id, MentionText: p.DisplayName,
					Mentioned: &graph.MentionedIdentity{User: &graph.Identity{
						ID: p.ID, DisplayName: p.DisplayName, UserIdentityType: "aadUser",
					}},
				})
				text = rest[len(p.DisplayName):]
				matched = true
				break
			}
		}
		if !matched {
			sb.WriteString("@")
			text = rest
		}
	}

	m := graph.NewMessage{
		Body:     graph.ItemBody{ContentType: graph.ContentHTML, Content: sb.String()},
		Mentions: mentions,
	}
	if in.Quote != nil {
		m.Attachments = []graph.Attachment{graph.MessageReference(*in.Quote)}
	}
	return m
}

func escape(s string) string {
	return strings.ReplaceAll(html.EscapeString(s), "\n", "<br>")
}

// MentionQuery returns the partial name after an '@' at the end of text
// (e.g. "hi @Ja" → "Ja", true). It stops at line breaks and only looks
// back a short distance so normal text does not trigger suggestions.
func MentionQuery(text string) (string, bool) {
	i := strings.LastIndexByte(text, '@')
	if i < 0 {
		return "", false
	}
	if i > 0 {
		prev := text[i-1]
		if prev != ' ' && prev != '\n' && prev != '(' {
			return "", false // e-mail address etc.
		}
	}
	q := text[i+1:]
	if len(q) > 40 || strings.ContainsAny(q, "\n@") {
		return "", false
	}
	return q, true
}

// Suggest filters candidates whose name starts with, or has a word
// starting with, query (case-insensitive). At most limit results.
func Suggest(candidates []Person, query string, limit int) []Person {
	q := strings.ToLower(strings.TrimSpace(query))
	var out []Person
	for _, p := range candidates {
		name := strings.ToLower(p.DisplayName)
		match := q == "" || strings.HasPrefix(name, q)
		if !match {
			for _, w := range strings.Fields(name) {
				if strings.HasPrefix(w, q) {
					match = true
					break
				}
			}
		}
		if match {
			out = append(out, p)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out
}

// ToEditText converts an existing message back into composer text and
// the people it mentions (formatting is not preserved).
func ToEditText(m graph.Message, plain string) Input {
	in := Input{Text: plain}
	for _, mm := range m.Mentions {
		if mm.Mentioned != nil && mm.Mentioned.User != nil {
			in.Mentions = append(in.Mentions, Person{ID: mm.Mentioned.User.ID, DisplayName: mm.MentionText})
		}
	}
	return in
}
