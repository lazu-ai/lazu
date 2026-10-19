package main

// Mock upstream for gateway-overhead measurement.
// Fixed TTFT, then N SSE events at a fixed interval, usage in the final event.
import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

var newConns, reqs atomic.Int64

var (
	ttft     = flag.Duration("ttft", 300*time.Millisecond, "")
	interval = flag.Duration("interval", 10*time.Millisecond, "")
	chunks   = flag.Int("chunks", 100, "")
	addr     = flag.String("addr", "127.0.0.1:10062", "")
)

type reqShape struct {
	Model  string `json:"model"`
	Stream bool   `json:"stream"`
}

func readReq(r *http.Request) (reqShape, int) {
	reqs.Add(1)
	b, _ := io.ReadAll(r.Body)
	var s reqShape
	json.Unmarshal(b, &s)
	return s, len(b)
}

func sse(w http.ResponseWriter) http.Flusher {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	return w.(http.Flusher)
}

func chat(w http.ResponseWriter, r *http.Request) {
	s, n := readReq(r)
	in := n / 4
	time.Sleep(*ttft)
	if !s.Stream {
		time.Sleep(time.Duration(*chunks) * *interval)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"chatcmpl-mock","object":"chat.completion","created":1790000000,"model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"ok ok ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d}}`, s.Model, in, *chunks, in+*chunks)
		return
	}
	f := sse(w)
	for i := 0; i < *chunks; i++ {
		if i > 0 {
			time.Sleep(*interval)
		}
		fmt.Fprintf(w, "data: {\"id\":\"chatcmpl-mock\",\"object\":\"chat.completion.chunk\",\"created\":1790000000,\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{\"content\":\"tok%d \"},\"finish_reason\":null}]}\n\n", s.Model, i)
		f.Flush()
	}
	fmt.Fprintf(w, "data: {\"id\":\"chatcmpl-mock\",\"object\":\"chat.completion.chunk\",\"created\":1790000000,\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n", s.Model)
	fmt.Fprintf(w, "data: {\"id\":\"chatcmpl-mock\",\"object\":\"chat.completion.chunk\",\"created\":1790000000,\"model\":%q,\"choices\":[],\"usage\":{\"prompt_tokens\":%d,\"completion_tokens\":%d,\"total_tokens\":%d}}\n\n", s.Model, in, *chunks, in+*chunks)
	fmt.Fprint(w, "data: [DONE]\n\n")
	f.Flush()
}

func responses(w http.ResponseWriter, r *http.Request) {
	s, n := readReq(r)
	in := n / 4
	time.Sleep(*ttft)
	usage := fmt.Sprintf(`{"input_tokens":%d,"input_tokens_details":{"cached_tokens":0},"output_tokens":%d,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":%d}`, in, *chunks, in+*chunks)
	base := func(status string, withOut bool) string {
		out := "[]"
		u := "null"
		if withOut {
			out = `[{"id":"msg_mock","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ok ok ok","annotations":[]}]}]`
			u = usage
		}
		return fmt.Sprintf(`{"id":"resp_mock","object":"response","created_at":1790000000,"status":%q,"model":%q,"output":%s,"usage":%s}`, status, s.Model, out, u)
	}
	if !s.Stream {
		time.Sleep(time.Duration(*chunks) * *interval)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, base("completed", true))
		return
	}
	f := sse(w)
	seq := 0
	ev := func(name, data string) {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data)
		seq++
	}
	ev("response.created", fmt.Sprintf(`{"type":"response.created","sequence_number":%d,"response":%s}`, seq, base("in_progress", false)))
	ev("response.output_item.added", fmt.Sprintf(`{"type":"response.output_item.added","sequence_number":%d,"output_index":0,"item":{"id":"msg_mock","type":"message","status":"in_progress","role":"assistant","content":[]}}`, seq))
	ev("response.content_part.added", fmt.Sprintf(`{"type":"response.content_part.added","sequence_number":%d,"item_id":"msg_mock","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}`, seq))
	f.Flush()
	for i := 0; i < *chunks; i++ {
		if i > 0 {
			time.Sleep(*interval)
		}
		ev("response.output_text.delta", fmt.Sprintf(`{"type":"response.output_text.delta","sequence_number":%d,"item_id":"msg_mock","output_index":0,"content_index":0,"delta":"tok%d "}`, seq, i))
		f.Flush()
	}
	ev("response.output_text.done", fmt.Sprintf(`{"type":"response.output_text.done","sequence_number":%d,"item_id":"msg_mock","output_index":0,"content_index":0,"text":"ok ok ok"}`, seq))
	ev("response.output_item.done", fmt.Sprintf(`{"type":"response.output_item.done","sequence_number":%d,"output_index":0,"item":{"id":"msg_mock","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ok ok ok","annotations":[]}]}}`, seq))
	ev("response.completed", fmt.Sprintf(`{"type":"response.completed","sequence_number":%d,"response":%s}`, seq, base("completed", true)))
	f.Flush()
}

func decisions(w http.ResponseWriter, r *http.Request) {
	s, n := readReq(r)
	time.Sleep(*ttft)
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/v1/decisions" {
		fmt.Fprintf(w, `{"model":%q,"answers":[{"name":"fixture","type":"predicate","probability":0.99}],"usage":{"input_tokens":%d,"output_tokens":0,"total_tokens":%d,"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}}`, s.Model, n/4, n/4)
		return
	}
	fmt.Fprintf(w, `{"model":%q,"answers":{"fixture":{"type":"noul","noul":0.99}},"usage":{"input_tokens":%d,"output_tokens":0}}`, s.Model, n/4)
}

func main() {
	flag.Parse()
	http.HandleFunc("/v1/chat/completions", chat)
	http.HandleFunc("/v1/responses", responses)
	http.HandleFunc("/v1/decisions", decisions)
	http.HandleFunc("/v1/systemone", decisions)
	http.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "new_conns=%d requests=%d\n", max(int64(0), newConns.Swap(0)-1), reqs.Swap(0))
	})
	srv := &http.Server{Addr: *addr, ConnState: func(c net.Conn, st http.ConnState) {
		if st == http.StateNew {
			newConns.Add(1)
		}
	}}
	srv.ListenAndServe()
}
