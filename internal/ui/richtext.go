package ui

import (
	"net/url"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/ape-dev-de/acat/internal/graph"
	"github.com/ape-dev-de/acat/internal/render"
)

// segments converts a rendered message into Fyne rich-text segments.
// attachments resolves <attachment id> references to names.
func segments(doc render.Doc, attachments []graph.Attachment) []widget.RichTextSegment {
	var out []widget.RichTextSegment
	for _, b := range doc.Blocks {
		switch b.Kind {
		case render.CodeBlock:
			out = append(out, &widget.TextSegment{Style: widget.RichTextStyleCodeBlock, Text: b.Code})
			continue
		case render.Heading:
			st := widget.RichTextStyleSubHeading
			if b.Level == 1 {
				st = widget.RichTextStyleHeading
			}
			out = append(out, &widget.TextSegment{Style: st, Text: inlineText(b.Inlines)})
			continue
		}

		var line []widget.RichTextSegment
		if b.Quote > 0 {
			line = append(line, &widget.TextSegment{
				Text:  strings.Repeat("▍ ", b.Quote),
				Style: widget.RichTextStyle{Inline: true, ColorName: theme.ColorNamePrimary},
			})
		}
		if b.Kind == render.ListItem {
			bullet := "• "
			if b.Ordered {
				bullet = strconv.Itoa(b.Index) + ". "
			}
			line = append(line, &widget.TextSegment{
				Text:  strings.Repeat("    ", b.List-1) + bullet,
				Style: widget.RichTextStyleInline,
			})
		}
		for _, in := range b.Inlines {
			line = append(line, inlineSegment(in, b.Quote > 0, attachments))
		}
		out = append(out, endParagraph(line)...)
	}
	return out
}

func inlineSegment(in render.Inline, quoted bool, attachments []graph.Attachment) widget.RichTextSegment {
	st := widget.RichTextStyle{Inline: true, ColorName: theme.ColorNameForeground, SizeName: theme.SizeNameText}
	st.TextStyle = fyne.TextStyle{
		Bold:          in.Style&render.Bold != 0,
		Italic:        in.Style&render.Italic != 0 || quoted,
		Underline:     in.Style&render.Underline != 0,
		Strikethrough: in.Style&render.Strike != 0,
		Monospace:     in.Style&render.Code != 0,
	}
	if quoted {
		st.ColorName = theme.ColorNameDisabled
	}

	switch in.Kind {
	case render.KindLink:
		if u, err := url.Parse(in.Href); err == nil {
			return &widget.HyperlinkSegment{Text: in.Text, URL: u}
		}
	case render.KindMention:
		st.TextStyle.Bold = true
		st.ColorName = theme.ColorNamePrimary
		return &widget.TextSegment{Text: "@" + in.Text, Style: st}
	case render.KindImage:
		st.TextStyle.Italic = true
		st.ColorName = theme.ColorNameDisabled
		label := "[image]"
		if in.Text != "" {
			label = "[image: " + in.Text + "]"
		}
		return &widget.TextSegment{Text: label, Style: st}
	case render.KindAttachment:
		st.TextStyle.Italic = true
		st.ColorName = theme.ColorNameDisabled
		return &widget.TextSegment{Text: attachmentLabel(in.Ref, attachments), Style: st}
	}
	return &widget.TextSegment{Text: in.Text, Style: st}
}

func attachmentLabel(id string, attachments []graph.Attachment) string {
	for _, a := range attachments {
		if a.ID != id {
			continue
		}
		switch {
		case a.ContentType == "messageReference":
			return "[quoted message]"
		case a.Name != "":
			return "[file: " + a.Name + "]"
		}
	}
	return "[attachment]"
}

// endParagraph turns the last inline text segment of a line into a block
// end; lines ending in a hyperlink (always inline) get an explicit break.
func endParagraph(line []widget.RichTextSegment) []widget.RichTextSegment {
	if len(line) == 0 {
		return line
	}
	if s, ok := line[len(line)-1].(*widget.TextSegment); ok {
		s.Style.Inline = false
		return line
	}
	return append(line, &widget.TextSegment{Style: widget.RichTextStyleParagraph})
}

func inlineText(in []render.Inline) string {
	var sb strings.Builder
	for _, x := range in {
		sb.WriteString(x.Text)
	}
	return sb.String()
}
