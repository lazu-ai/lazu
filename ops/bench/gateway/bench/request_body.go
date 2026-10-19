package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

type benchmarkBody struct {
	text         string
	promptEnd    int
	uniquePrefix string
}

func benchmarkRunNonce() (string, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(nonce[:]), nil
}

// An empty nonce preserves the exact historical request bytes. A nonempty
// nonce is generated once per run and contains only hexadecimal JSON-safe text.
func makeBenchmarkBody(api, model string, stream bool, kb int, nonce string) benchmarkBody {
	var sb strings.Builder
	words := []string{"the", "gateway", "forwards", "request", "function", "return", "error", "context", "stream", "token", "客户", "请求", "模型", "{\"a\":1}", "if err != nil {", "}\n", "import", "class", "0x7f", "value"}
	for i := 0; sb.Len() < kb*1024; i++ {
		sb.WriteString(words[(i*7+i/3)%len(words)])
		sb.WriteByte(' ')
	}
	prompt := strings.ReplaceAll(strings.ReplaceAll(sb.String(), `\`, ``), `"`, `'`)
	prompt = strings.ReplaceAll(prompt, "\n", " ")
	var text, suffix string
	if api == "decisions-openai" {
		suffix = `","questions":[{"name":"fixture","type":"predicate","instructions":"Is this gateway test text?"}]}`
		text = fmt.Sprintf(`{"model":%q,"input":"%s%s`, model, prompt, suffix)
	} else if api == "decisions-legacy" {
		suffix = `","questions":{"fixture":{"type":"noul","instructions":"Is this gateway test text?"}}}`
		text = fmt.Sprintf(`{"model":%q,"state":"%s%s`, model, prompt, suffix)
	} else if api == "responses" {
		text = fmt.Sprintf(`{"model":%q,"stream":%v,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"%s"}]}]}`, model, stream, prompt)
		suffix = `"}]}]}`
	} else {
		extra := ""
		if stream {
			extra = `,"stream_options":{"include_usage":true}`
		}
		text = fmt.Sprintf(`{"model":%q,"stream":%v%s,"messages":[{"role":"user","content":"%s"}]}`, model, stream, extra, prompt)
		suffix = `"}]}`
	}
	body := benchmarkBody{text: text, promptEnd: len(text) - len(suffix)}
	if nonce != "" {
		body.uniquePrefix = " [lazu-bench:" + nonce + ";w="
	}
	return body
}

func (body benchmarkBody) payload(worker, index int) []byte {
	if body.uniquePrefix == "" {
		return []byte(body.text)
	}
	// One allocation/copy of the full body, as in the historical []byte(text)
	// path. Reserve room for two native int values without another large copy.
	result := make([]byte, 0, len(body.text)+len(body.uniquePrefix)+48)
	result = append(result, body.text[:body.promptEnd]...)
	result = append(result, body.uniquePrefix...)
	result = strconv.AppendInt(result, int64(worker), 10)
	result = append(result, ";i="...)
	result = strconv.AppendInt(result, int64(index), 10)
	result = append(result, "]"...)
	return append(result, body.text[body.promptEnd:]...)
}
