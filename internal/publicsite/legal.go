package publicsite

import (
	"fmt"
	"html/template"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"

	"github.com/lazu-ai/lazu/internal/common"
)

type legalChapter struct{ ID, Title string }
type legalPage struct {
	Title, Home, Contents, SiblingTitle, SiblingHref, MarkdownHref string
	Body                                                           template.HTML
	Chapters                                                       []legalChapter
}

var legalHTML = regexp.MustCompile(`(?i)<\/?[a-z][^>]*>`)
var legalTags = map[string]bool{"p": true, "div": true, "section": true, "h2": true, "h3": true, "h4": true, "ul": true, "ol": true, "li": true, "strong": true, "b": true, "em": true, "i": true, "blockquote": true, "code": true, "pre": true, "a": true, "table": true, "thead": true, "tbody": true, "tr": true, "th": true, "td": true, "br": true, "hr": true}
var legalDrop = map[string]bool{"script": true, "style": true, "iframe": true, "object": true, "embed": true, "svg": true, "math": true, "form": true, "input": true, "button": true, "template": true, "head": true}

// RenderLegal reads the effective runtime document. Legacy HTML overrides are
// parsed into an allowlisted reading tree, with no styles, event handlers,
// embedded resources or executable URL schemes. Markdown uses the same tree.
func RenderLegal(content, locale, kind string) ([]byte, error) {
	labels := map[string][4]string{
		"en":    {"Privacy Policy", "Terms of Service", "Home", "Contents"},
		"zh":    {"隐私政策", "服务条款", "首页", "目录"},
		"zh-TW": {"隱私政策", "服務條款", "首頁", "目錄"},
		"ja":    {"プライバシーポリシー", "利用規約", "ホーム", "目次"},
	}[NormalizeLocale(locale)]
	index, sibling := 0, "terms"
	if kind == "terms" {
		index, sibling = 1, "privacy"
	}
	page := legalPage{Title: labels[index], Home: LocalizedPath("/", locale), Contents: labels[3], SiblingTitle: labels[1-index], SiblingHref: LocalizedPath("/"+sibling, locale), MarkdownHref: LocalizedPath("/"+kind+".md", locale)}
	source := content
	if !legalHTML.MatchString(source) {
		source = string(RenderMarkdown(source, nil))
	}
	root, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return nil, err
	}
	var body strings.Builder
	var textOf func(*html.Node) string
	textOf = func(n *html.Node) string {
		if n.Type == html.TextNode {
			return n.Data
		}
		var b strings.Builder
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			b.WriteString(textOf(c))
		}
		return b.String()
	}
	var write func(*html.Node)
	write = func(n *html.Node) {
		if n.Type == html.TextNode {
			body.WriteString(template.HTMLEscapeString(n.Data))
			return
		}
		if n.Type != html.ElementNode && n.Type != html.DocumentNode {
			return
		}
		tag := n.Data
		if legalDrop[tag] {
			return
		}
		if tag == "h1" {
			if title := strings.TrimSpace(textOf(n)); title != "" {
				page.Title = title
			}
			return
		}
		allowed := legalTags[tag]
		if allowed {
			body.WriteString("<" + tag)
			if tag == "h2" {
				id := fmt.Sprintf("legal-section-%d", len(page.Chapters)+1)
				page.Chapters = append(page.Chapters, legalChapter{ID: id, Title: strings.TrimSpace(textOf(n))})
				body.WriteString(` id="` + id + `"`)
			}
			if tag == "a" {
				for _, attr := range n.Attr {
					if attr.Key == "href" {
						href := strings.TrimSpace(attr.Val)
						u, e := url.Parse(href)
						if e == nil && (u.Scheme == "" || u.Scheme == "https" || u.Scheme == "http" || u.Scheme == "mailto") && !strings.ContainsAny(href, "\x00\r\n\t") {
							body.WriteString(` href="` + template.HTMLEscapeString(href) + `"`)
						}
					}
				}
			}
			body.WriteString(">")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			write(c)
		}
		if allowed && tag != "br" && tag != "hr" {
			body.WriteString("</" + tag + ">")
		}
	}
	write(root)
	page.Body = template.HTML(body.String())
	description := page.Title + " · Lazu"
	if operator := common.SiteOperator(); operator != "" {
		description += " · " + operator
	}
	meta := NewMeta(locale, kind, "/"+kind, page.Title+" · Lazu", description)
	meta.MachineLinks = []MachineLink{{Type: "text/markdown", Href: SiteOrigin + page.MarkdownHref}}
	meta.SetJSONLD(map[string]any{"@context": "https://schema.org", "@type": "WebPage", "name": page.Title, "url": meta.Canonical, "inLanguage": HTMLLang(locale)})
	return Render("legal.gohtml", meta, page)
}
