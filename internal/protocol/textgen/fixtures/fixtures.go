// Package fixtures loads the recorded upstream streams under
// internal/protocol/textgen/testdata/upstream for codec tests (AGENTS.md,
// Protocol Layer rule 8). The recorder is
// internal/relay/contracttest/record_upstream_test.go.
package fixtures

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Event is one server-sent event of a recording: its event name ("" when
// the vendor sends none) and its data, the lines joined with newlines.
type Event struct {
	Name string
	Data string
}

// dir is the testdata/upstream directory, found from this file so a test in
// any package can load a recording by "<vendor>/<case>.sse".
func dir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "testdata", "upstream")
}

// Raw is the recording's body as the vendor sent it, without the two
// leading "# " lines the recorder adds.
func Raw(t testing.TB, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir(), name))
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	lines := strings.Split(string(body), "\n")
	for len(lines) > 0 && strings.HasPrefix(lines[0], "# ") {
		lines = lines[1:]
	}
	return strings.Join(lines, "\n")
}

// Events are the server-sent events of a recording, in order. A recording
// that is a plain JSON body (a refusal) has one event with that body.
func Events(t testing.TB, name string) []Event {
	t.Helper()
	raw := Raw(t, name)
	if !strings.HasPrefix(strings.TrimLeft(raw, "\n"), "event:") && !strings.HasPrefix(strings.TrimLeft(raw, "\n"), "data:") {
		return []Event{{Data: strings.TrimSpace(raw)}}
	}
	var out []Event
	var current Event
	var data []string
	flush := func() {
		if len(data) == 0 && current.Name == "" {
			return
		}
		current.Data = strings.Join(data, "\n")
		out = append(out, current)
		current, data = Event{}, nil
	}
	sc := bufio.NewScanner(strings.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, ":"):
		case strings.HasPrefix(line, "event:"):
			current.Name = strings.TrimSpace(line[6:])
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(line[5:], " "))
		}
	}
	flush()
	return out
}

// Data are the data payloads of a recording's events, in order; "[DONE]"
// is left in, as a Chat stream ends with it.
func Data(t testing.TB, name string) []string {
	t.Helper()
	events := Events(t, name)
	out := make([]string, 0, len(events))
	for _, event := range events {
		out = append(out, event.Data)
	}
	return out
}
