package compose

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ape-dev-de/acat/internal/graph"
	"github.com/ape-dev-de/acat/internal/render"
)

var (
	jane     = Person{ID: "u1", DisplayName: "Jane Roe"}
	janeLong = Person{ID: "u3", DisplayName: "Jane Roe-Miller"}
	tom      = Person{ID: "u2", DisplayName: "Tom"}
)

func TestBuildEscapesAndBreaks(t *testing.T) {
	m := Build(Input{Text: "  a < b & \"c\"\nnext line \n"})
	if m.Body.ContentType != "html" || m.Body.Content != "a &lt; b &amp; &#34;c&#34;<br>next line" {
		t.Fatalf("%q", m.Body.Content)
	}
	if m.Mentions != nil {
		t.Fatal("no mentions expected")
	}
}

func TestBuildMentions(t *testing.T) {
	m := Build(Input{Text: "@Jane Roe and @Tom, also @Jane Roe-Miller; mail x@Tom.example @Nobody", Mentions: []Person{jane, tom, janeLong}})
	want := `<at id="0">Jane Roe</at> and <at id="1">Tom</at>, also <at id="2">Jane Roe-Miller</at>; mail x@Tom.example @Nobody`
	if m.Body.Content != want {
		t.Fatalf("\n got %s\nwant %s", m.Body.Content, want)
	}
	if len(m.Mentions) != 3 || m.Mentions[2].Mentioned.User.ID != "u3" || m.Mentions[1].MentionText != "Tom" {
		t.Fatalf("%+v", m.Mentions)
	}
	// Round trip through the renderer shows the mentions again.
	if got := render.HTML(m.Body.Content).PlainText(); !strings.HasPrefix(got, "@Jane Roe and @Tom") {
		t.Fatalf("render: %q", got)
	}
}

func TestBuildQuote(t *testing.T) {
	q := &graph.Message{ID: "m1", Body: graph.ItemBody{Content: "orig"}}
	m := Build(Input{Text: "answer", Quote: q})
	if m.Body.Content != `<attachment id="m1"></attachment>answer` || len(m.Attachments) != 1 || m.Attachments[0].ContentType != "messageReference" {
		t.Fatalf("%+v", m)
	}
	if (Input{Quote: q}).Empty() || !(Input{Text: " \n"}).Empty() {
		t.Fatal("Empty() wrong")
	}
}

func TestMentionQuery(t *testing.T) {
	cases := map[string]struct {
		q  string
		ok bool
	}{
		"hi @Ja":                      {"Ja", true},
		"@":                           {"", true},
		"(@to":                        {"to", true},
		"mail a@b":                    {"", false},
		"no mention":                  {"", false},
		"@Jane\nnext":                 {"", false},
		"@" + strings.Repeat("x", 41): {"", false},
	}
	for in, want := range cases {
		q, ok := MentionQuery(in)
		if q != want.q || ok != want.ok {
			t.Errorf("MentionQuery(%q) = %q,%v want %q,%v", in, q, ok, want.q, want.ok)
		}
	}
}

func TestSuggest(t *testing.T) {
	all := []Person{jane, tom, janeLong, {ID: "u4", DisplayName: "Anna Roeder"}}
	got := Suggest(all, "roe", 0)
	if !reflect.DeepEqual(got, []Person{jane, janeLong, all[3]}) {
		t.Fatalf("%+v", got)
	}
	if got := Suggest(all, "", 2); len(got) != 2 {
		t.Fatalf("limit: %+v", got)
	}
	if got := Suggest(all, "TO", 0); len(got) != 1 || got[0] != tom {
		t.Fatalf("case: %+v", got)
	}
}

func TestToEditText(t *testing.T) {
	m := graph.Message{Mentions: []graph.Mention{{ID: 0, MentionText: "Jane Roe", Mentioned: &graph.MentionedIdentity{User: &graph.Identity{ID: "u1"}}}}}
	in := ToEditText(m, "@Jane Roe hi")
	rebuilt := Build(in)
	if rebuilt.Body.Content != `<at id="0">Jane Roe</at> hi` {
		t.Fatalf("%q", rebuilt.Body.Content)
	}
}
