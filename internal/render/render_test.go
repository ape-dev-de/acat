package render

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestHTML(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []Block
	}{
		{
			name: "plain paragraph",
			in:   "<p>Hello   world</p>",
			want: []Block{{Kind: Paragraph, Inlines: []Inline{{Text: "Hello world"}}}},
		},
		{
			name: "bold italic nested",
			in:   "<p>a <b>bold <i>both</i></b> c</p>",
			want: []Block{{Kind: Paragraph, Inlines: []Inline{
				{Text: "a "}, {Text: "bold ", Style: Bold}, {Text: "both", Style: Bold | Italic}, {Text: " c"},
			}}},
		},
		{
			name: "line breaks split paragraphs",
			in:   "<div>one<br>two</div>",
			want: []Block{
				{Kind: Paragraph, Inlines: []Inline{{Text: "one"}}},
				{Kind: Paragraph, Inlines: []Inline{{Text: "two"}}},
			},
		},
		{
			name: "safe link",
			in:   `<p>see <a href="https://example.org/x?y=1">docs</a></p>`,
			want: []Block{{Kind: Paragraph, Inlines: []Inline{{Text: "see "}, {Kind: KindLink, Text: "docs", Href: "https://example.org/x?y=1"}}}},
		},
		{
			name: "javascript link becomes text",
			in:   `<p><a href="javascript:alert(1)">click</a></p>`,
			want: []Block{{Kind: Paragraph, Inlines: []Inline{{Text: "click"}}}},
		},
		{
			name: "mention",
			in:   `<div><div><at id="0">Jane Roe</at> please check</div></div>`,
			want: []Block{{Kind: Paragraph, Inlines: []Inline{{Kind: KindMention, Text: "Jane Roe", Ref: "0"}, {Text: " please check"}}}},
		},
		{
			name: "teams code block keeps whitespace",
			in:   "<codeblock class=\"Go\"><code>func main() {\n\tfmt.Println(\"hi\")\n}</code></codeblock>",
			want: []Block{{Kind: CodeBlock, Code: "func main() {\n\tfmt.Println(\"hi\")\n}"}},
		},
		{
			name: "pre block",
			in:   "<pre>a  b\nc</pre>",
			want: []Block{{Kind: CodeBlock, Code: "a  b\nc"}},
		},
		{
			name: "inline code",
			in:   "<p>run <code>make test</code></p>",
			want: []Block{{Kind: Paragraph, Inlines: []Inline{{Text: "run "}, {Text: "make test", Style: Code}}}},
		},
		{
			name: "emoji tag and emoji img",
			in:   `<p>ok <emoji id="smile" alt="😄" title="Smile"></emoji><img itemtype="http://schema.skype.com/Emoji" alt="👍" src="x"></p>`,
			want: []Block{{Kind: Paragraph, Inlines: []Inline{{Text: "ok 😄👍"}}}},
		},
		{
			name: "non-emoji image is a placeholder",
			in:   `<p><img src="https://graph.microsoft.com/v1.0/hosted" alt="screenshot"></p>`,
			want: []Block{{Kind: Paragraph, Inlines: []Inline{{Kind: KindImage, Text: "screenshot"}}}},
		},
		{
			name: "attachment reference",
			in:   `<attachment id="1617284372951"></attachment><p>see above</p>`,
			want: []Block{
				{Kind: Paragraph, Inlines: []Inline{{Kind: KindAttachment, Ref: "1617284372951"}}},
				{Kind: Paragraph, Inlines: []Inline{{Text: "see above"}}},
			},
		},
		{
			name: "teams reply quote",
			in:   `<blockquote itemscope="" itemtype="http://schema.skype.com/Reply" itemid="1616"><strong itemprop="mri">Jane</strong><span itemprop="time" itemid="1616"></span><p itemprop="preview">original</p></blockquote><p>my answer</p>`,
			want: []Block{
				{Kind: Paragraph, Quote: 1, Inlines: []Inline{{Text: "Jane", Style: Bold}}},
				{Kind: Paragraph, Quote: 1, Inlines: []Inline{{Text: "original"}}},
				{Kind: Paragraph, Inlines: []Inline{{Text: "my answer"}}},
			},
		},
		{
			name: "ordered and nested lists",
			in:   `<ol start="3"><li>three</li><li>four<ul><li>sub</li></ul></li></ol>`,
			want: []Block{
				{Kind: ListItem, List: 1, Ordered: true, Index: 3, Inlines: []Inline{{Text: "three"}}},
				{Kind: ListItem, List: 1, Ordered: true, Index: 4, Inlines: []Inline{{Text: "four"}}},
				{Kind: ListItem, List: 2, Index: 1, Inlines: []Inline{{Text: "sub"}}},
			},
		},
		{
			name: "heading",
			in:   `<h2>Agenda</h2><p>x</p>`,
			want: []Block{
				{Kind: Heading, Level: 2, Inlines: []Inline{{Text: "Agenda"}}},
				{Kind: Paragraph, Inlines: []Inline{{Text: "x"}}},
			},
		},
		{
			name: "script and style dropped, entities decoded",
			in:   `<style>p{}</style><script>alert(1)</script><p>a &amp; b &lt;c&gt;</p>`,
			want: []Block{{Kind: Paragraph, Inlines: []Inline{{Text: "a & b <c>"}}}},
		},
		{
			name: "unknown tags degrade to text",
			in:   `<p><marquee>wow</marquee> <span style="color:red">red</span></p>`,
			want: []Block{{Kind: Paragraph, Inlines: []Inline{{Text: "wow red"}}}},
		},
		{
			name: "strike underline",
			in:   `<p><s>old</s><u>new</u></p>`,
			want: []Block{{Kind: Paragraph, Inlines: []Inline{{Text: "old", Style: Strike}, {Text: "new", Style: Underline}}}},
		},
		{
			name: "empty",
			in:   `<p> </p><div></div>`,
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HTML(tt.in).Blocks
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("\n got %#v\nwant %#v", got, tt.want)
			}
		})
	}
}

func TestTextBody(t *testing.T) {
	d := Body("text", "line 1\r\n\r\nline <b>3</b>\n")
	want := []Block{
		{Kind: Paragraph, Inlines: []Inline{{Text: "line 1"}}},
		{Kind: Paragraph},
		{Kind: Paragraph, Inlines: []Inline{{Text: "line <b>3</b>"}}},
	}
	if !reflect.DeepEqual(d.Blocks, want) {
		t.Fatalf("got %#v", d.Blocks)
	}
}

func TestPlainText(t *testing.T) {
	d := HTML(`<blockquote><p>quoted</p></blockquote><p>Hi <at id="0">Jane</at>, see <img alt="x"> and <attachment id="1"></attachment></p><ol><li>one</li></ol><ul><li>dot</li></ul><pre>code</pre>`)
	want := "> quoted\nHi @Jane, see [image] and [attachment]\n1. one\n• dot\ncode"
	if got := d.PlainText(); got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestDeepNestingIsBounded(t *testing.T) {
	in := strings.Repeat("<b>", 10000) + "deep" + strings.Repeat("</b>", 10000)
	_ = HTML(in) // must not stack overflow or hang
}

func FuzzHTML(f *testing.F) {
	for _, s := range []string{
		"<p>a<b>b</b></p>", `<at id="1">x</at>`, "<codeblock><code>x</code></codeblock>",
		"<ol><li><ul><li>x", "<blockquote><blockquote>q", "<a href='javascript:x'>y</a>", "\xff\xfe<p>",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		d := HTML(s)
		for _, b := range d.Blocks {
			for _, in := range b.Inlines {
				if in.Kind == KindLink && safeHref(in.Href) == "" {
					t.Fatalf("unsafe href %q", in.Href)
				}
			}
		}
		pt := d.PlainText()
		if utf8.ValidString(s) && !utf8.ValidString(pt) {
			t.Fatalf("invalid UTF-8 output for valid input")
		}
	})
}
