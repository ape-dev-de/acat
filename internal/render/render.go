// Package render converts Teams message HTML into a small, UI-independent
// document model. It supports what chats actually use (paragraphs, line
// breaks, bold/italic/underline/strike, links, @mentions, inline code,
// code blocks, quotes/replies, lists, emoji) and degrades everything else
// to plain text. It never executes or fetches anything.
package render

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Style is a bitmask of inline styles.
type Style uint8

// Inline styles.
const (
	Bold Style = 1 << iota
	Italic
	Underline
	Strike
	Code
)

// InlineKind distinguishes inline elements.
type InlineKind uint8

// Inline kinds.
const (
	KindText InlineKind = iota
	KindLink
	KindMention
	KindImage      // non-emoji image; Text holds the alt text, if any
	KindAttachment // <attachment id="…">; Ref holds the attachment ID
)

// Inline is a run of text with uniform formatting.
type Inline struct {
	Kind  InlineKind
	Text  string
	Style Style
	Href  string // KindLink: http(s)/mailto only
	Ref   string // KindMention: mention id; KindAttachment: attachment id
}

// BlockKind distinguishes blocks.
type BlockKind uint8

// Block kinds.
const (
	Paragraph BlockKind = iota
	CodeBlock
	ListItem
	Heading
)

// Block is a paragraph-level element.
type Block struct {
	Kind    BlockKind
	Inlines []Inline // Paragraph, ListItem, Heading
	Code    string   // CodeBlock: verbatim text
	Quote   int      // blockquote nesting depth (0 = none)
	List    int      // list nesting depth for ListItem (1 = top level)
	Ordered bool     // ListItem in <ol>
	Index   int      // ListItem number in <ol> (1-based)
	Level   int      // Heading level 1–6
}

// Doc is a rendered message.
type Doc struct {
	Blocks []Block
}

// maxDepth bounds recursion on hostile input.
const maxDepth = 64

// Text renders a plain-text body (contentType "text").
func Text(s string) Doc {
	var d Doc
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		d.Blocks = append(d.Blocks, Block{Kind: Paragraph, Inlines: nonEmpty(Inline{Text: line})})
	}
	return d.trim()
}

// HTML renders a Teams HTML body.
func HTML(s string) Doc {
	ctx := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(strings.NewReader(s), ctx)
	if err != nil {
		return Text(s)
	}
	b := &builder{}
	for _, n := range nodes {
		b.walk(n, 0)
	}
	b.flush()
	return Doc{Blocks: b.blocks}.trim()
}

// Body renders a Graph message body by content type.
func Body(contentType, content string) Doc {
	if strings.EqualFold(contentType, "html") {
		return HTML(content)
	}
	return Text(content)
}

type listState struct {
	ordered bool
	next    int
}

type builder struct {
	blocks []Block
	cur    []Inline
	style  Style
	href   string
	quote  int
	lists  []listState
	// pending block attributes for the paragraph being built
	kind  BlockKind
	level int
	item  *Block
}

func (b *builder) walk(n *html.Node, depth int) {
	if depth > maxDepth {
		return
	}
	switch n.Type {
	case html.TextNode:
		b.text(n.Data)
		return
	case html.ElementNode:
	default:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			b.walk(c, depth+1)
		}
		return
	}

	switch n.DataAtom {
	case atom.Script, atom.Style, atom.Head, atom.Title, atom.Template:
		return
	case atom.Br:
		b.flush()
		return
	case atom.Pre:
		b.flush()
		b.blocks = append(b.blocks, Block{Kind: CodeBlock, Code: strings.Trim(textContent(n), "\n"), Quote: b.quote})
		return
	case atom.Img:
		b.image(n)
		return
	}
	switch n.Data { // Teams-specific, non-standard tags
	case "codeblock":
		b.flush()
		b.blocks = append(b.blocks, Block{Kind: CodeBlock, Code: strings.Trim(textContent(n), "\n"), Quote: b.quote})
		return
	case "at":
		if t := strings.TrimSpace(textContent(n)); t != "" {
			b.cur = append(b.cur, Inline{Kind: KindMention, Text: t, Style: b.style, Ref: attr(n, "id")})
		}
		return
	case "emoji":
		if alt := attr(n, "alt"); alt != "" {
			b.text(alt)
		}
		return
	case "attachment":
		b.cur = append(b.cur, Inline{Kind: KindAttachment, Ref: attr(n, "id"), Style: b.style})
		return
	}

	saved := b.style
	savedHref := b.href
	children := func() {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			b.walk(c, depth+1)
		}
	}

	switch n.DataAtom {
	case atom.B, atom.Strong:
		b.style |= Bold
		children()
	case atom.I, atom.Em:
		b.style |= Italic
		children()
	case atom.U, atom.Ins:
		b.style |= Underline
		children()
	case atom.S, atom.Strike, atom.Del:
		b.style |= Strike
		children()
	case atom.Code, atom.Kbd, atom.Samp, atom.Tt:
		b.style |= Code
		children()
	case atom.A:
		if h := safeHref(attr(n, "href")); h != "" {
			b.href = h
		}
		children()
	case atom.Blockquote:
		b.flush()
		b.quote++
		children()
		b.flush()
		b.quote--
	case atom.Ul, atom.Ol:
		b.flush()
		start := 1
		if v, err := strconv.Atoi(attr(n, "start")); err == nil {
			start = v
		}
		b.lists = append(b.lists, listState{ordered: n.DataAtom == atom.Ol, next: start})
		children()
		b.flush()
		b.lists = b.lists[:len(b.lists)-1]
	case atom.Li:
		b.flush()
		item := Block{Kind: ListItem, List: max(len(b.lists), 1)}
		if len(b.lists) > 0 {
			l := &b.lists[len(b.lists)-1]
			item.Ordered, item.Index = l.ordered, l.next
			l.next++
		}
		b.item = &item
		children()
		b.flush()
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		b.flush()
		b.kind, b.level = Heading, int(n.Data[1]-'0')
		children()
		b.flush()
	case atom.P, atom.Div, atom.Tr, atom.Table, atom.Section, atom.Article, atom.Header, atom.Footer:
		b.flush()
		children()
		b.flush()
	case atom.Td, atom.Th:
		children()
		b.text(" ")
	default:
		children()
	}
	b.style = saved
	b.href = savedHref
}

func (b *builder) text(s string) {
	if s == "" {
		return
	}
	in := Inline{Kind: KindText, Text: s, Style: b.style}
	if b.href != "" {
		in.Kind, in.Href = KindLink, b.href
	}
	b.cur = append(b.cur, in)
}

func (b *builder) image(n *html.Node) {
	alt := attr(n, "alt")
	if strings.Contains(strings.ToLower(attr(n, "itemtype")), "emoji") && alt != "" {
		b.text(alt)
		return
	}
	b.cur = append(b.cur, Inline{Kind: KindImage, Text: alt, Style: b.style})
}

// flush ends the current block.
func (b *builder) flush() {
	inl := normalize(b.cur)
	b.cur = nil
	var blk Block
	switch {
	case b.item != nil:
		blk = *b.item
		b.item = nil
	case b.kind == Heading:
		blk = Block{Kind: Heading, Level: b.level}
	default:
		blk = Block{Kind: Paragraph}
	}
	b.kind, b.level = Paragraph, 0
	if len(inl) == 0 {
		return
	}
	blk.Inlines = inl
	blk.Quote = b.quote
	b.blocks = append(b.blocks, blk)
}

// normalize collapses HTML whitespace, trims block edges and merges
// adjacent runs with identical formatting.
func normalize(in []Inline) []Inline {
	var out []Inline
	lastSpace := true // trims leading whitespace
	for _, x := range in {
		if x.Kind == KindText || x.Kind == KindLink {
			var sb strings.Builder
			for _, r := range x.Text {
				if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' {
					if !lastSpace {
						sb.WriteByte(' ')
					}
					lastSpace = true
					continue
				}
				sb.WriteRune(r)
				lastSpace = false
			}
			x.Text = sb.String()
			if x.Text == "" {
				continue
			}
		} else {
			lastSpace = false
		}
		if n := len(out); n > 0 && mergeable(out[n-1], x) {
			out[n-1].Text += x.Text
			continue
		}
		out = append(out, x)
	}
	// trim trailing whitespace
	for len(out) > 0 {
		l := &out[len(out)-1]
		if l.Kind != KindText && l.Kind != KindLink {
			break
		}
		l.Text = strings.TrimRight(l.Text, " ")
		if l.Text != "" {
			break
		}
		out = out[:len(out)-1]
	}
	return out
}

func mergeable(a, b Inline) bool {
	return a.Kind == b.Kind && a.Style == b.Style && a.Href == b.Href && (a.Kind == KindText || a.Kind == KindLink)
}

func (d Doc) trim() Doc {
	// drop leading/trailing empty paragraphs (from plain text)
	for len(d.Blocks) > 0 && isEmpty(d.Blocks[0]) {
		d.Blocks = d.Blocks[1:]
	}
	for len(d.Blocks) > 0 && isEmpty(d.Blocks[len(d.Blocks)-1]) {
		d.Blocks = d.Blocks[:len(d.Blocks)-1]
	}
	return d
}

func isEmpty(b Block) bool { return b.Kind != CodeBlock && len(b.Inlines) == 0 }

func nonEmpty(in Inline) []Inline {
	if in.Text == "" {
		return nil
	}
	return []Inline{in}
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func textContent(n *html.Node) string {
	var sb strings.Builder
	var rec func(*html.Node, int)
	rec = func(n *html.Node, depth int) {
		if depth > maxDepth {
			return
		}
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		if n.Type == html.ElementNode && n.DataAtom == atom.Br {
			sb.WriteByte('\n')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			rec(c, depth+1)
		}
	}
	rec(n, 0)
	return sb.String()
}

// safeHref allows only web and mail links.
func safeHref(h string) string {
	h = strings.TrimSpace(h)
	l := strings.ToLower(h)
	if strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "mailto:") {
		return h
	}
	return ""
}

// PlainText flattens a document, e.g. for notifications and previews.
func (d Doc) PlainText() string {
	var sb strings.Builder
	for i, b := range d.Blocks {
		if i > 0 {
			sb.WriteByte('\n')
		}
		if b.Quote > 0 {
			sb.WriteString(strings.Repeat("> ", b.Quote))
		}
		switch b.Kind {
		case CodeBlock:
			sb.WriteString(b.Code)
			continue
		case ListItem:
			sb.WriteString(strings.Repeat("  ", b.List-1))
			if b.Ordered {
				sb.WriteString(strconv.Itoa(b.Index) + ". ")
			} else {
				sb.WriteString("• ")
			}
		}
		for _, in := range b.Inlines {
			switch in.Kind {
			case KindMention:
				sb.WriteString("@" + in.Text)
			case KindImage:
				sb.WriteString("[image]")
			case KindAttachment:
				sb.WriteString("[attachment]")
			default:
				sb.WriteString(in.Text)
			}
		}
	}
	return sb.String()
}

// Links returns all link targets in the document, in order.
func (d Doc) Links() []string {
	var out []string
	for _, b := range d.Blocks {
		for _, in := range b.Inlines {
			if in.Kind == KindLink {
				out = append(out, in.Href)
			}
		}
	}
	return out
}

// IsMeetingLink reports whether u is a Teams meeting join link.
func IsMeetingLink(u string) bool {
	l := strings.ToLower(u)
	return (strings.HasPrefix(l, "https://teams.microsoft.com/l/meetup-join/") ||
		strings.HasPrefix(l, "https://teams.live.com/meet/") ||
		strings.HasPrefix(l, "https://teams.microsoft.com/meet/"))
}

// MeetingLink returns the first Teams meeting link in the document.
func (d Doc) MeetingLink() (string, bool) {
	for _, l := range d.Links() {
		if IsMeetingLink(l) {
			return l, true
		}
	}
	return "", false
}
