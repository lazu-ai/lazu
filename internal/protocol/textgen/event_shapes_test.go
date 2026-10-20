package textgen

import (
	"os"
	"regexp"
	"testing"
)

// Protocol Layer rule 4 (AGENTS.md): the IR carries content, never a
// protocol's output shapes. The parsed wire request (context.go) and the
// usage fact are the only dto types the IR may name.
func TestEventIRCarriesNoWireShapes(t *testing.T) {
	wireShape := regexp.MustCompile(`dto\.(Responses|Claude|Gemini|OpenAI|ChatCompletions)[A-Za-z]*`)
	for _, file := range []string{"event.go", "ir.go", "features.go", "plan.go", "degrade.go", "params.go"} {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if match := wireShape.Find(body); match != nil {
			t.Fatalf("%s names the wire shape %s; the IR carries content, emitters own protocol shapes (AGENTS.md, Protocol Layer 4)", file, match)
		}
	}
}
