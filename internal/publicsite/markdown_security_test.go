package publicsite

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func markdownURLAttribute(t *testing.T, rendered, tag, attribute string) string {
	t.Helper()
	document, err := html.Parse(strings.NewReader(rendered))
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == tag {
			for _, item := range node.Attr {
				if item.Key == attribute {
					found = append(found, item.Val)
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)
	if len(found) != 1 {
		t.Fatalf("wanted one %s %s attribute, got %v in %s", tag, attribute, found, rendered)
	}
	return found[0]
}

func TestRenderMarkdownRejectsDangerousNormalizedURLs(t *testing.T) {
	// GO-2026-5320: raw HTML is disabled, but URL validation also needs to
	// follow entity normalization for links/images and cover autolinks.
	for _, item := range []struct {
		name, source, tag, attribute string
	}{
		{"link_numeric_reference", `[click](&#106;avascript:alert(1))`, "a", "href"},
		{"link_named_reference", `[click](javascript&colon;alert(1))`, "a", "href"},
		{"image_numeric_reference", `![alt](&#106;avascript:alert(1))`, "img", "src"},
		{"image_named_reference", `![alt](javascript&colon;alert(1))`, "img", "src"},
		{"javascript_autolink", `<javascript:alert(1)>`, "a", "href"},
	} {
		t.Run(item.name, func(t *testing.T) {
			rendered := string(RenderMarkdown(item.source, nil))
			if destination := markdownURLAttribute(t, rendered, item.tag, item.attribute); destination != "" {
				t.Fatalf("dangerous normalized URL survived public Markdown rendering: %q in %s", destination, rendered)
			}
		})
	}
}

func TestRenderMarkdownPreservesSafeURLs(t *testing.T) {
	for _, item := range []struct {
		name, source, tag, attribute string
	}{
		{"link", `[docs](https://lazu.ai/docs)`, "a", "href"},
		{"image", `![docs](https://lazu.ai/docs)`, "img", "src"},
		{"autolink", `<https://lazu.ai/docs>`, "a", "href"},
	} {
		t.Run(item.name, func(t *testing.T) {
			rendered := string(RenderMarkdown(item.source, nil))
			if destination := markdownURLAttribute(t, rendered, item.tag, item.attribute); destination != "https://lazu.ai/docs" {
				t.Fatalf("safe destination changed during public Markdown rendering: %q", destination)
			}
		})
	}
}
