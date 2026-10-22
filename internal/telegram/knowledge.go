package telegram

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/types"
)

// Answers are grounded in the deployment's published documentation: the
// llms.txt files the web and docs builds already produce. Small files are
// sent whole with every question; large ones (llms-full.txt) are split into
// pages and only the pages that share the most words with the question are
// sent. Error numbers named in a question are looked up in the error
// catalog itself. There is no index to build or keep in sync.

const (
	knowledgeTTL        = time.Hour
	knowledgeMaxBytes   = 4 << 20
	alwaysSendBelow     = 32 << 10
	retrievedPages      = 3
	retrievedBudget     = 24 << 10
	pageExcerptBudget   = 9 << 10
	knowledgeFetchLimit = 15 * time.Second
)

var knowledgeClient = &http.Client{Timeout: knowledgeFetchLimit}

// KnowledgeURLs are the documents answers come from.
func KnowledgeURLs() []string {
	if urls := common.AppConfig.Telegram.Answer.KnowledgeURLs; len(urls) > 0 {
		return urls
	}
	origin := common.SiteWebOrigin()
	return []string{origin + "/llms.txt", origin + "/docs/llms.txt", origin + "/docs/llms-full.txt"}
}

type document struct {
	url   string
	text  string
	pages []page
}

type page struct {
	title  string
	text   string
	tokens map[string]int
	length int // number of tokens
}

var knowledge = struct {
	sync.Mutex
	docs      []document
	fetchedAt time.Time
	urls      string
	fetching  bool
}{}

// loadKnowledge returns the cached documents, fetching them when the cache
// is empty and refreshing them in the background once they are stale. A
// document that cannot be fetched is skipped; answering never fails because
// one source is down.
func loadKnowledge(ctx context.Context) []document {
	urls := KnowledgeURLs()
	key := strings.Join(urls, "\n")
	knowledge.Lock()
	docs, fresh, same := knowledge.docs, time.Since(knowledge.fetchedAt) < knowledgeTTL, knowledge.urls == key
	if same && len(docs) > 0 {
		if !fresh && !knowledge.fetching {
			knowledge.fetching = true
			common.SafeGo("telegram-knowledge-refresh", func() { refreshKnowledge(urls, key) })
		}
		knowledge.Unlock()
		return docs
	}
	knowledge.Unlock()
	return refreshKnowledge(urls, key)
}

func refreshKnowledge(urls []string, key string) []document {
	ctx, cancel := context.WithTimeout(context.Background(), 2*knowledgeFetchLimit)
	defer cancel()
	var docs []document
	for _, u := range urls {
		text, err := fetchText(ctx, u)
		if err != nil {
			common.SysError("telegram: knowledge source skipped: " + err.Error())
			continue
		}
		docs = append(docs, newDocument(u, text))
	}
	knowledge.Lock()
	defer knowledge.Unlock()
	knowledge.fetching = false
	if len(docs) == 0 && knowledge.urls == key {
		return knowledge.docs // keep serving the last good copy
	}
	knowledge.docs, knowledge.fetchedAt, knowledge.urls = docs, time.Now(), key
	return docs
}

func fetchText(ctx context.Context, u string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", fmt.Errorf("%s: %w", u, err)
	}
	resp, err := knowledgeClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s: %w", u, scrubTransportError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: HTTP %d", u, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, knowledgeMaxBytes))
	if err != nil {
		return "", fmt.Errorf("%s: %w", u, err)
	}
	return string(raw), nil
}

// newDocument splits a large document into pages at its "---" separators
// (the docs build's llms-full.txt) or, failing that, at top-level headings.
func newDocument(u, text string) document {
	doc := document{url: u, text: text}
	if len(text) < alwaysSendBelow {
		return doc
	}
	parts := strings.Split(text, "\n---\n")
	if len(parts) < 3 {
		parts = splitAtHeadings(text)
	}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		title, _, _ := strings.Cut(part, "\n")
		counts := tokenCounts(part)
		length := 0
		for _, n := range counts {
			length += n
		}
		doc.pages = append(doc.pages, page{title: strings.TrimSpace(strings.TrimLeft(title, "# ")), text: part, tokens: counts, length: length})
	}
	return doc
}

func splitAtHeadings(text string) []string {
	var parts []string
	var cur strings.Builder
	for _, line := range strings.SplitAfter(text, "\n") {
		if strings.HasPrefix(line, "# ") && cur.Len() > 0 {
			parts = append(parts, cur.String())
			cur.Reset()
		}
		cur.WriteString(line)
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}
	return parts
}

// tokens are lower-case words of letters, digits, _ . and -, and every pair
// of adjacent Han, kana or Hangul characters, so Chinese and Japanese
// questions match without a dictionary.
func tokens(s string) []string {
	var out []string
	var word []rune
	var prevCJK rune
	flush := func() {
		if len(word) >= 2 {
			out = append(out, string(word))
		}
		word = word[:0]
	}
	for _, r := range strings.ToLower(s) {
		switch {
		case isCJK(r):
			flush()
			if prevCJK != 0 {
				out = append(out, string([]rune{prevCJK, r}))
			}
			prevCJK = r
			continue
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.':
			word = append(word, r)
		default:
			flush()
		}
		prevCJK = 0
	}
	flush()
	return out
}

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r)
}

func tokenCounts(s string) map[string]int {
	counts := map[string]int{}
	for _, t := range tokens(s) {
		counts[t]++
	}
	return counts
}

// commonWords never decide a match on their own.
var commonWords = map[string]bool{
	"the": true, "and": true, "for": true, "how": true, "what": true, "can": true, "does": true, "is": true,
	"to": true, "of": true, "in": true, "it": true, "my": true, "with": true, "on": true, "do": true, "an": true,
	"我的": true, "怎么": true, "什么": true, "可以": true, "是不": true, "不是": true, "一下": true, "请问": true,
}

type scoredPage struct {
	page  page
	score float64
}

// relevantPages ranks the pages of large documents with BM25: words the
// question shares with a page count more when they are rare across pages,
// and a long page (the error reference) does not win just by being long.
func relevantPages(docs []document, question string) []scoredPage {
	query := map[string]bool{}
	for _, t := range tokens(question) {
		if !commonWords[t] {
			query[t] = true
		}
	}
	var all []page
	for _, d := range docs {
		all = append(all, d.pages...)
	}
	if len(query) == 0 || len(all) == 0 {
		return nil
	}
	df := map[string]int{}
	for t := range query {
		for _, p := range all {
			if p.tokens[t] > 0 {
				df[t]++
			}
		}
	}
	const k1, b = 1.2, 0.75
	var totalLength float64
	for _, p := range all {
		totalLength += float64(p.length)
	}
	avgLength := math.Max(totalLength/float64(len(all)), 1)
	n := float64(len(all))
	var scored []scoredPage
	for _, p := range all {
		var score float64
		for t := range query {
			if tf := float64(p.tokens[t]); tf > 0 {
				idf := math.Log(1 + (n-float64(df[t])+0.5)/(float64(df[t])+0.5))
				score += idf * tf * (k1 + 1) / (tf + k1*(1-b+b*float64(p.length)/avgLength))
			}
		}
		if score > 0 {
			scored = append(scored, scoredPage{p, score})
		}
	}
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].score > scored[j].score })
	if len(scored) > retrievedPages {
		scored = scored[:retrievedPages]
	}
	return scored
}

// excerpt keeps a long page's title and the paragraphs that share the most
// words with the question, in their original order, within budget.
func excerpt(p page, question string, budget int) string {
	if len(p.text) <= budget {
		return p.text
	}
	query := map[string]bool{}
	for _, t := range tokens(question) {
		if !commonWords[t] {
			query[t] = true
		}
	}
	paras := strings.Split(p.text, "\n\n")
	type ranked struct {
		i     int
		score int
	}
	var rs []ranked
	for i, para := range paras {
		s := 0
		for t, n := range tokenCounts(para) {
			if query[t] {
				s += n
			}
		}
		rs = append(rs, ranked{i, s})
	}
	sort.SliceStable(rs, func(a, b int) bool { return rs[a].score > rs[b].score })
	keep := map[int]bool{0: true}
	used := len(paras[0])
	for _, r := range rs {
		if r.score == 0 || used+len(paras[r.i]) > budget {
			continue
		}
		keep[r.i] = true
		used += len(paras[r.i])
	}
	var out []string
	for i, para := range paras {
		if keep[i] {
			out = append(out, para)
		}
	}
	return strings.Join(out, "\n\n")
}

var lzNumber = regexp.MustCompile(`(?i)\bLZ-?(\d{4})\b`)

// catalogRows are the error catalog rows a question names by number or by
// code, rendered as one line each.
func catalogRows(question string) []string {
	lower := strings.ToLower(question)
	wanted := map[string]bool{}
	for _, m := range lzNumber.FindAllStringSubmatch(question, -1) {
		wanted["LZ-"+m[1]] = true
	}
	var rows []string
	for code, spec := range types.Catalog {
		if spec.ID == "" || spec.Audience == types.AudienceInternal {
			continue
		}
		if wanted[spec.ID] || (len(code) > 8 && strings.Contains(lower, string(code))) {
			rows = append(rows, fmt.Sprintf("%s `%s` (HTTP %d, %s): %s", spec.ID, code, spec.HTTPStatus, spec.Type, spec.Template))
		}
	}
	sort.Strings(rows)
	if len(rows) > 8 {
		rows = rows[:8]
	}
	return rows
}

// Reference assembles the material one question is answered from.
func Reference(ctx context.Context, question string) string {
	docs := loadKnowledge(ctx)
	var b strings.Builder
	for _, d := range docs {
		if len(d.pages) == 0 {
			fmt.Fprintf(&b, "<document source=%q>\n%s\n</document>\n\n", d.url, strings.TrimSpace(d.text))
		}
	}
	used := 0
	for _, sp := range relevantPages(docs, question) {
		text := excerpt(sp.page, question, pageExcerptBudget)
		if used+len(text) > retrievedBudget {
			break
		}
		used += len(text)
		fmt.Fprintf(&b, "<document page=%q>\n%s\n</document>\n\n", sp.page.title, text)
	}
	if rows := catalogRows(question); len(rows) > 0 {
		b.WriteString("<error-catalog>\n")
		for _, r := range rows {
			b.WriteString(r)
			b.WriteByte('\n')
		}
		b.WriteString("</error-catalog>\n")
	}
	return b.String()
}

// resetKnowledge clears the cache (tests).
func resetKnowledge() {
	knowledge.Lock()
	knowledge.docs, knowledge.fetchedAt, knowledge.urls, knowledge.fetching = nil, time.Time{}, "", false
	knowledge.Unlock()
}
