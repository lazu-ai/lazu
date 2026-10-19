package publicsite

import (
	"bytes"
	"html/template"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

// Blog bodies are Markdown. Raw HTML is not rendered (goldmark's default), so
// a post cannot inject markup. Two block directives, each on a line of its
// own, pull live catalog data into a post:
//
//	::model claude-opus-5-5
//	::prices claude-opus-5-5 compare=claude-opus-5
//
// The first draws the model strip that links to the model page; the second a
// price table read at render time, so a post never quotes a stale price.

var markdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(html.WithXHTML()),
)

var directiveLine = regexp.MustCompile(`^::(model|prices)\s+([A-Za-z0-9._:/-]+)((?:\s+[a-z]+=[A-Za-z0-9._:/-]+)*)\s*$`)

// Directive is a parsed ::model / ::prices line.
type Directive struct {
	Kind    string
	Model   string
	Compare string
}

// ParseDirective reads a directive line.
func ParseDirective(line string) (Directive, bool) {
	match := directiveLine.FindStringSubmatch(strings.TrimSpace(line))
	if match == nil {
		return Directive{}, false
	}
	directive := Directive{Kind: match[1], Model: match[2]}
	for _, pair := range strings.Fields(match[3]) {
		key, value, _ := strings.Cut(pair, "=")
		if key == "compare" {
			directive.Compare = value
		}
	}
	return directive, true
}

// PlaceholderPattern finds [bracketed placeholders] a draft leaves for a
// person to fill in; a post that still has one cannot be published.
var PlaceholderPattern = regexp.MustCompile(`\[(?:[^\]\n]{2,160})\]`)

// Placeholders lists the unfilled [..] slots outside code and links.
func Placeholders(body string) []string {
	var found []string
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		for _, loc := range PlaceholderPattern.FindAllStringIndex(line, -1) {
			end := loc[1]
			// Markdown links and images: [text](url) is not a placeholder.
			if end < len(line) && line[end] == '(' {
				continue
			}
			if loc[0] > 0 && line[loc[0]-1] == '!' {
				continue
			}
			found = append(found, line[loc[0]:loc[1]])
		}
	}
	return found
}

// DirectiveRenderer turns a directive into HTML.
type DirectiveRenderer func(Directive) template.HTML

// RenderMarkdown renders a body, handing directive lines to the renderer.
func RenderMarkdown(body string, render DirectiveRenderer) template.HTML {
	var out bytes.Buffer
	var chunk strings.Builder
	inFence := false
	flush := func() {
		if chunk.Len() == 0 {
			return
		}
		_ = markdown.Convert([]byte(chunk.String()), &out)
		chunk.Reset()
	}
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
		}
		if !inFence {
			if directive, ok := ParseDirective(trimmed); ok && render != nil {
				flush()
				out.WriteString(string(render(directive)))
				continue
			}
		}
		chunk.WriteString(line)
		chunk.WriteString("\n")
	}
	flush()
	return template.HTML(out.String())
}

// PlainText strips Markdown to text (reading time, descriptions).
func PlainText(body string) string {
	var b strings.Builder
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if _, ok := ParseDirective(trimmed); ok {
			continue
		}
		trimmed = strings.TrimLeft(trimmed, "#>-*0123456789. ")
		trimmed = strings.NewReplacer("**", "", "__", "", "`", "").Replace(trimmed)
		if trimmed != "" {
			b.WriteString(trimmed)
			b.WriteString(" ")
		}
	}
	return strings.TrimSpace(b.String())
}

// ReadingMinutes estimates reading time: CJK at ~400 characters a minute,
// everything else at ~220 words a minute.
func ReadingMinutes(body string) int {
	text := PlainText(body)
	cjk := 0
	for _, r := range text {
		if r >= 0x3000 && r <= 0x9fff || r >= 0xf900 && r <= 0xfaff {
			cjk++
		}
	}
	words := len(strings.Fields(text))
	minutes := cjk/400 + words/220
	if utf8.RuneCountInString(text) > 0 && minutes < 1 {
		minutes = 1
	}
	return minutes
}
