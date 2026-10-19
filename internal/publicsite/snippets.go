package publicsite

import (
	"fmt"
	"html/template"
	"regexp"
	"strings"
)

// Code samples ported from apps/web/src/pages/Routes/code-samples.tsx so the
// public model page and the console's model detail show the same requests.

type SampleLanguage struct {
	Key   string
	Label string
}

var SampleLanguages = []SampleLanguage{
	{Key: "curl", Label: "cURL"},
	{Key: "python", Label: "Python"},
	{Key: "typescript", Label: "TypeScript"},
}

var endpointLabels = map[string]string{
	"anthropic":        "Messages",
	"embeddings":       "Embeddings",
	"gemini":           "Gemini",
	"image-generation": "Images",
	"jina-rerank":      "Rerank",
	"openai":           "Chat Completions",
	"openai-response":  "Responses",
	"video-generation": "Videos",
}

func endpointLabel(kind string) string {
	if label, ok := endpointLabels[kind]; ok {
		return label
	}
	if kind == "" {
		return "API"
	}
	return kind
}

func methodHasBody(method string) bool { return method != "GET" && method != "HEAD" }

func curlJSON(base, path, body, method, extra string) string {
	out := fmt.Sprintf("curl -X %s %s%s \\\n  -H \"Authorization: Bearer $LAZU_API_KEY\" \\%s\n  -H \"Content-Type: application/json\"", method, base, path, extra)
	if methodHasBody(method) {
		out += " \\\n  -d '" + body + "'"
	}
	return out
}

func pyRequests(base, path, body, method string) string {
	out := "import os\nimport requests\n\nresp = requests.request(\n    \"" + method + "\",\n    \"" + base + path + "\",\n    headers={\"Authorization\": f\"Bearer {os.environ['LAZU_API_KEY']}\"},"
	if methodHasBody(method) {
		out += "\n    json=" + body + ","
	}
	return out + "\n)\nprint(resp.json())"
}

func tsFetch(base, path, body, method string) string {
	out := "const resp = await fetch(\"" + base + path + "\", {\n  method: \"" + method + "\",\n  headers: {\n    Authorization: `Bearer ${process.env.LAZU_API_KEY}`,\n    \"Content-Type\": \"application/json\",\n  },"
	if methodHasBody(method) {
		out += "\n  body: JSON.stringify(" + body + "),"
	}
	return out + "\n});\nconsole.log(await resp.json());"
}

// BuildSnippet renders one request for an endpoint in a language.
func BuildSnippet(language string, endpoint Endpoint, modelID, base string) string {
	kind := endpoint.Type
	if kind == "" {
		kind = "openai"
	}
	path := endpoint.Path
	if path == "" {
		path = "/v1/chat/completions"
	}
	method := strings.ToUpper(strings.TrimSpace(endpoint.Method))
	if method == "" {
		method = "POST"
	}
	curl := func(body, extra string) string { return curlJSON(base, path, body, method, extra) }
	py := func(body string) string { return pyRequests(base, path, body, method) }
	ts := func(body string) string { return tsFetch(base, path, body, method) }
	id := modelID

	switch kind {
	case "openai":
		body := "{\n    \"model\": \"" + id + "\",\n    \"messages\": [{ \"role\": \"user\", \"content\": \"Hello from Lazu\" }]\n  }"
		if language == "curl" {
			return curl(body, "")
		}
		if method != "POST" {
			if language == "python" {
				return py(`{"model": "` + id + `", "messages": [{"role": "user", "content": "Hello from Lazu"}]}`)
			}
			return ts(`{ model: "` + id + `", messages: [{ role: "user", content: "Hello from Lazu" }] }`)
		}
		if language == "python" {
			return "import os\nfrom openai import OpenAI\n\nclient = OpenAI(\n    base_url=\"" + base + "/v1\",\n    api_key=os.getenv(\"LAZU_API_KEY\"),\n)\n\nresp = client.chat.completions.create(\n    model=\"" + id + "\",\n    messages=[{\"role\": \"user\", \"content\": \"Hello from Lazu\"}],\n)\nprint(resp.choices[0].message.content)"
		}
		return "import OpenAI from \"openai\";\n\nconst client = new OpenAI({\n  baseURL: \"" + base + "/v1\",\n  apiKey: process.env.LAZU_API_KEY,\n});\n\nconst resp = await client.chat.completions.create({\n  model: \"" + id + "\",\n  messages: [{ role: \"user\", content: \"Hello from Lazu\" }],\n});\nconsole.log(resp.choices[0].message.content);"
	case "openai-response":
		body := "{\n    \"model\": \"" + id + "\",\n    \"input\": \"Hello from Lazu\"\n  }"
		if language == "curl" {
			return curl(body, "")
		}
		if method != "POST" {
			if language == "python" {
				return py(`{"model": "` + id + `", "input": "Hello from Lazu"}`)
			}
			return ts(`{ model: "` + id + `", input: "Hello from Lazu" }`)
		}
		if language == "python" {
			return "import os\nfrom openai import OpenAI\n\nclient = OpenAI(\n    base_url=\"" + base + "/v1\",\n    api_key=os.getenv(\"LAZU_API_KEY\"),\n)\n\nresp = client.responses.create(model=\"" + id + "\", input=\"Hello from Lazu\")\nprint(resp.output_text)"
		}
		return "import OpenAI from \"openai\";\n\nconst client = new OpenAI({\n  baseURL: \"" + base + "/v1\",\n  apiKey: process.env.LAZU_API_KEY,\n});\n\nconst resp = await client.responses.create({\n  model: \"" + id + "\",\n  input: \"Hello from Lazu\",\n});\nconsole.log(resp.output_text);"
	case "anthropic":
		body := "{\n    \"model\": \"" + id + "\",\n    \"max_tokens\": 1024,\n    \"messages\": [{ \"role\": \"user\", \"content\": \"Hello from Lazu\" }]\n  }"
		if language == "curl" {
			return curl(body, "\n  -H \"anthropic-version: 2023-06-01\" \\")
		}
		if method != "POST" {
			if language == "python" {
				return py(`{"model": "` + id + `", "max_tokens": 1024, "messages": [{"role": "user", "content": "Hello from Lazu"}]}`)
			}
			return ts(`{ model: "` + id + `", max_tokens: 1024, messages: [{ role: "user", content: "Hello from Lazu" }] }`)
		}
		if language == "python" {
			return "import os\nimport anthropic\n\nclient = anthropic.Anthropic(\n    base_url=\"" + base + "\",\n    api_key=os.getenv(\"LAZU_API_KEY\"),\n)\n\nresp = client.messages.create(\n    model=\"" + id + "\",\n    max_tokens=1024,\n    messages=[{\"role\": \"user\", \"content\": \"Hello from Lazu\"}],\n)\nprint(resp.content[0].text)"
		}
		return "import Anthropic from \"@anthropic-ai/sdk\";\n\nconst client = new Anthropic({\n  baseURL: \"" + base + "\",\n  apiKey: process.env.LAZU_API_KEY,\n});\n\nconst resp = await client.messages.create({\n  model: \"" + id + "\",\n  max_tokens: 1024,\n  messages: [{ role: \"user\", content: \"Hello from Lazu\" }],\n});\nconsole.log(resp.content[0]);"
	case "embeddings":
		if language == "curl" {
			return curl("{\n    \"model\": \""+id+"\",\n    \"input\": \"Hello from Lazu\"\n  }", "")
		}
		if language == "python" {
			return py(`{"model": "` + id + `", "input": "Hello from Lazu"}`)
		}
		return ts(`{ model: "` + id + `", input: "Hello from Lazu" }`)
	case "image-generation":
		prompt := "A minimalist poster of a sunrise over the ocean"
		if language == "curl" {
			return curl("{\n    \"model\": \""+id+"\",\n    \"prompt\": \""+prompt+"\",\n    \"size\": \"1024x1024\"\n  }", "")
		}
		if language == "python" {
			return py(`{"model": "` + id + `", "prompt": "` + prompt + `", "size": "1024x1024"}`)
		}
		return ts(`{ model: "` + id + `", prompt: "` + prompt + `", size: "1024x1024" }`)
	case "video-generation":
		if language == "curl" {
			return curl("{\n    \"model\": \""+id+"\",\n    \"prompt\": \"A cinematic product reveal on a clean studio table\",\n    \"seconds\": 5,\n    \"size\": \"16:9\"\n  }", "")
		}
		if language == "python" {
			return py(`{"model": "` + id + `", "prompt": "A cinematic product reveal", "seconds": 5, "size": "16:9"}`)
		}
		return ts(`{ model: "` + id + `", prompt: "A cinematic product reveal", seconds: 5, size: "16:9" }`)
	case "jina-rerank":
		if language == "curl" {
			return curl("{\n    \"model\": \""+id+"\",\n    \"query\": \"Which document is about billing?\",\n    \"documents\": [\"API billing is usage-based.\", \"This guide covers images.\"],\n    \"top_n\": 2\n  }", "")
		}
		if language == "python" {
			return py(`{"model": "` + id + `", "query": "Which document is about billing?", "documents": ["API billing is usage-based.", "This guide covers images."], "top_n": 2}`)
		}
		return ts(`{ model: "` + id + `", query: "Which document is about billing?", documents: ["API billing is usage-based.", "This guide covers images."], top_n: 2 }`)
	case "gemini":
		if language == "curl" {
			return curl("{\n    \"contents\": [{ \"parts\": [{ \"text\": \"Hello from Lazu\" }] }]\n  }", "")
		}
		if language == "python" {
			return py(`{"contents": [{"parts": [{"text": "Hello from Lazu"}]}]}`)
		}
		return ts(`{ contents: [{ parts: [{ text: "Hello from Lazu" }] }] }`)
	}
	if language == "curl" {
		return curl("{\n    \"model\": \""+id+"\"\n  }", "")
	}
	if language == "python" {
		return py(`{"model": "` + id + `"}`)
	}
	return ts(`{ model: "` + id + `" }`)
}

var (
	snippetToken = regexp.MustCompile("(\"(?:[^\"\\\\]|\\\\.)*\"|'(?:[^'\\\\]|\\\\[\\s\\S])*'|`(?:[^`\\\\]|\\\\.)*`|#[^\\n]*|//[^\\n]*|-[A-Za-z](?:\\s)|\\b[A-Za-z_][A-Za-z0-9_]*\\b)")
	keywords     = map[string]*regexp.Regexp{
		"curl":       regexp.MustCompile(`^(curl|export)$`),
		"python":     regexp.MustCompile(`^(import|from|print|def|return|for|in|with|as)$`),
		"typescript": regexp.MustCompile(`^(import|from|const|await|new|return|console)$`),
	}
)

// Highlight tokenizes a generated snippet into styled spans (the minimal
// highlighter from code-samples.tsx; only needs to handle its own output).
func Highlight(code, language string) template.HTML {
	var b strings.Builder
	cursor := 0
	for _, match := range snippetToken.FindAllStringIndex(code, -1) {
		start, end := match[0], match[1]
		if start > cursor {
			b.WriteString(template.HTMLEscapeString(code[cursor:start]))
		}
		token := code[start:end]
		trailing := ""
		class := ""
		switch {
		case strings.HasPrefix(token, `"`) || strings.HasPrefix(token, "'") || strings.HasPrefix(token, "`"):
			class = "str"
		case strings.HasPrefix(token, "#") || strings.HasPrefix(token, "//"):
			class = "cm"
		case len(token) == 3 && token[0] == '-' && strings.TrimSpace(token[2:]) == "":
			class = "flag"
			trailing = token[2:]
			token = token[:2]
		case keywords[language] != nil && keywords[language].MatchString(token):
			class = "kw"
		}
		if class != "" {
			b.WriteString(`<span class="lazu-code__` + class + `">` + template.HTMLEscapeString(token) + `</span>`)
		} else {
			b.WriteString(template.HTMLEscapeString(token))
		}
		b.WriteString(template.HTMLEscapeString(trailing))
		cursor = end
	}
	if cursor < len(code) {
		b.WriteString(template.HTMLEscapeString(code[cursor:]))
	}
	return template.HTML(b.String())
}
