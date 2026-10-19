package publicsite

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"path"
	"strings"
	"sync"
)

//go:embed templates/*.gohtml
var templateFS embed.FS

// Alternate is one hreflang link.
type Alternate struct {
	Hreflang string
	Href     string
}

// MachineLink points at the page's machine-readable sibling.
type MachineLink struct {
	Type string
	Href string
}

// Meta is everything <head> needs.
type Meta struct {
	Locale      string
	Lang        string
	Section     string // chrome partial: models | blog | …
	Title       string
	Description string
	Canonical   string
	Robots      string
	OGType      string
	OGImage     string
	OGImageW    int    // og:image:width; 0 leaves the tag out
	OGImageH    int    // og:image:height
	OGImageAlt  string // og:image:alt; "" leaves the tag out
	OGLocale    string
	MediaOrigin string // optional origin of the page's prioritized image
	BodyClass   string // <body class>; "" is the default "lazu-site"
	// HeadScript runs in <head> before anything paints (a page's own
	// marks <html> with lzs-has-mine with it, so signed-in visitors who
	// already have results do not see the hero flash).
	HeadScript   template.JS
	Alternates   []Alternate
	MachineLinks []MachineLink
	JSONLD       template.JS
}

// NewMeta fills the locale-derived fields.
func NewMeta(locale, section, barePath, title, description string) Meta {
	meta := Meta{
		Locale:      locale,
		Lang:        HTMLLang(locale),
		Section:     section,
		Title:       title,
		Description: description,
		Canonical:   SiteOrigin + LocalizedPath(barePath, locale),
		Robots:      "index, follow",
		OGType:      "website",
		OGImage:     SiteOrigin + "/og-image.png",
		OGImageW:    1200,
		OGImageH:    630,
		OGLocale:    OGLocale(locale),
	}
	for _, other := range Locales {
		meta.Alternates = append(meta.Alternates, Alternate{Hreflang: other, Href: SiteOrigin + LocalizedPath(barePath, other)})
	}
	meta.Alternates = append(meta.Alternates, Alternate{Hreflang: "x-default", Href: SiteOrigin + barePath})
	return meta
}

// SetJSONLD stores a JSON-LD graph.
func (m *Meta) SetJSONLD(graph any) {
	body, err := json.Marshal(graph)
	if err != nil {
		return
	}
	m.JSONLD = template.JS(body)
}

var (
	baseOnce sync.Once
	base     *template.Template
	baseErr  error
)

type barsArg struct {
	Label string
	Bars  []Bar
}

// extraBaseTemplates are shared templates extensions add to every page's base
// set (files under templates/, which go:embed picks up with the core's).
var extraBaseTemplates []string

// RegisterBaseTemplates adds shared templates; call from init().
func RegisterBaseTemplates(names ...string) {
	extraBaseTemplates = append(extraBaseTemplates, names...)
}

func loadBase() (*template.Template, error) {
	baseOnce.Do(func() {
		base, baseErr = template.New("site").Funcs(template.FuncMap{
			"t":      func(id string, args ...any) string { return id },
			"docs":   func(slug string) string { return slug },
			"ssi":    ssi,
			"barsOf": func(label string, bars []Bar) barsArg { return barsArg{Label: label, Bars: bars} },
			"join":   strings.Join,
			"base":   path.Base,
		}).ParseFS(templateFS, append([]string{"templates/layout.gohtml"}, extraBaseTemplates...)...)
	})
	return base, baseErr
}

// ssi writes an nginx include for a chrome partial the web build emitted.
// html/template strips comments from template text, so it is produced here.
func ssi(name string) template.HTML {
	clean := path.Base(name)
	return template.HTML(`<!--# include virtual="/__chrome/` + template.HTMLEscapeString(clean) + `" -->`)
}

// DocsURL is a docs page in the locale's docs tree.
func DocsURL(locale, slug string) string {
	prefix := "/docs"
	if locale != "en" {
		prefix += "/" + locale
	}
	if slug == "" {
		return prefix
	}
	return prefix + "/" + strings.TrimPrefix(slug, "/")
}

var pageCache sync.Map

func pageTemplate(file string) (*template.Template, error) {
	if cached, ok := pageCache.Load(file); ok {
		return cached.(*template.Template), nil
	}
	root, err := loadBase()
	if err != nil {
		return nil, err
	}
	clone, err := root.Clone()
	if err != nil {
		return nil, err
	}
	parsed, err := clone.ParseFS(templateFS, "templates/"+file)
	if err != nil {
		return nil, err
	}
	pageCache.Store(file, parsed)
	return parsed, nil
}

// Render executes a page template ("models.gohtml") inside the layout.
func Render(file string, meta Meta, page any) ([]byte, error) {
	parsed, err := pageTemplate(file)
	if err != nil {
		return nil, err
	}
	locale := meta.Locale
	bound, err := parsed.Clone()
	if err != nil {
		return nil, err
	}
	bound.Funcs(template.FuncMap{
		"t":    func(id string, args ...any) string { return T(locale, id, args...) },
		"docs": func(slug string) string { return DocsURL(locale, slug) },
	})
	var out bytes.Buffer
	if err := bound.ExecuteTemplate(&out, "layout", map[string]any{"Meta": meta, "Page": page}); err != nil {
		return nil, fmt.Errorf("render %s: %w", file, err)
	}
	return out.Bytes(), nil
}

// RenderNotFound is the 404 for unknown models and posts.
func RenderNotFound(locale string) ([]byte, error) {
	meta := NewMeta(locale, "models", "/models", T(locale, "notFound.metaTitle"), T(locale, "notFound.body"))
	meta.Robots = "noindex, follow"
	meta.Alternates = nil
	meta.SetJSONLD(map[string]any{"@context": "https://schema.org", "@type": "WebPage", "name": meta.Title})
	return Render("notfound.gohtml", meta, map[string]string{
		"ModelsHref": LocalizedPath("/models", locale),
		"HomeHref":   LocalizedPath("/", locale),
	})
}

// absoluteURL makes a page's media address absolute for meta tags and
// structured data: a site path gets SiteOrigin; an address that is already
// absolute (an image on a media CDN) is kept.
func absoluteURL(src string) string {
	if strings.HasPrefix(src, "https://") || strings.HasPrefix(src, "http://") {
		return src
	}
	return SiteOrigin + src
}
