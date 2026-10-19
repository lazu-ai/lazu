package textgen

import (
	"bytes"
	"testing"
)

func TestRetainTopLevelFieldsKeepsOriginalWireSlices(t *testing.T) {
	body := []byte(` { "input" : [ {"text":"\u4f60"} ], "large":9007199254740993, "empty":"" } `)
	fields, err := RetainTopLevelFields(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(fields["input"]) != `[{"text":"\u4f60"} ]` && string(fields["input"]) != `[ {"text":"\u4f60"} ]` {
		t.Fatalf("raw input changed: %s", fields["input"])
	}
	if string(fields["large"]) != "9007199254740993" {
		t.Fatal("numeric precision changed")
	}
	start := bytes.Index(body, []byte("9007199254740993"))
	if &fields["large"][0] != &body[start] {
		t.Fatal("retention copied the inbound allocation")
	}
}

func TestRetainTopLevelFieldsRejectsAmbiguousDuplicateSemanticKeys(t *testing.T) {
	for _, body := range []string{`{"model":"allowed","model":"denied"}`, `{"stream":false,"stream":true}`, `{"input":[],"input":"hello"}`} {
		if _, err := RetainTopLevelFields([]byte(body)); err == nil {
			t.Fatalf("accepted ambiguous wire semantics: %s", body)
		}
	}
}
