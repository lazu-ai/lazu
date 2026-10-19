package service

import (
	"strings"
	"testing"
)

func TestGeneratedDecisionQuestionsValidateRubricsAndIDs(t *testing.T) {
	valid := `{"questions":[{"id":"department","type":"choice","instructions":"Which area?","choices":[{"id":"billing","description":"Payment"},{"id":"technical","description":"Login"}],"levels":[]},{"id":"urgent","type":"noul","instructions":"Is this urgent?","choices":[],"levels":[]}]}`
	questions, err := ParseGeneratedDecisionQuestions([]byte(valid))
	if err != nil || len(questions) != 2 {
		t.Fatalf("valid questions rejected: %v", err)
	}
	for _, body := range []string{`{"questions":[]}`, strings.Replace(valid, `"urgent"`, `"department"`, 1), strings.Replace(valid, `"technical"`, `"billing"`, 1), strings.Replace(valid, `"department"`, `"invalid id"`, 1), strings.Replace(valid, `"instructions":"Which area?"`, `"instructions":""`, 1)} {
		if _, err := ParseGeneratedDecisionQuestions([]byte(body)); err == nil {
			t.Fatalf("invalid generator output accepted: %s", body)
		}
	}
	// A real generator returned this empty score rubric despite strict output.
	// Keep rejecting it even if an upstream ignores the stronger request schema.
	emptyScore := `{"questions":[{"id":"handling_priority","type":"score","instructions":"判断此请求的处理优先级。","choices":[],"levels":[]}]}`
	if _, err := ParseGeneratedDecisionQuestions([]byte(emptyScore)); err == nil {
		t.Fatal("empty generated score rubric accepted")
	}
	validScore := strings.Replace(emptyScore, `"levels":[]`, `"levels":["低","中","高","紧急"]`, 1)
	if _, err := ParseGeneratedDecisionQuestions([]byte(validScore)); err != nil {
		t.Fatalf("valid generated score rubric rejected: %v", err)
	}
}

func TestModelLogsRedactInlineImagesBeforeTruncation(t *testing.T) {
	image := "data:image/png;base64," + strings.Repeat("A", 9<<20)
	body := []byte(`{"state":{"integer":9007199254740993},"images":["` + image + `"],"input":[{"source":{"type":"base64","media_type":"image/png","data":"SECRET_IMAGE"}}]}`)
	redacted := string(redactLoggedImageData(body))
	if strings.Contains(redacted, "SECRET_IMAGE") || strings.Contains(redacted, "data:image/png;base64") || !strings.Contains(redacted, "9007199254740993") || len(redacted) > 2000 {
		t.Fatal("image redaction failed or corrupted business JSON")
	}
}

func TestGeneratedBinaryQuestionRejectsOpenEndedImageText(t *testing.T) {
	for _, instructions := range []string{"Which text appears near the top of the image?", "How many circles are shown?", "图片中是否有红色圆形？", "Does the text say HELLO?", "哪个文字出现在图片顶部？"} {
		body := `{"questions":[{"id":"text_content","type":"noul","instructions":"` + instructions + `","choices":[],"levels":[]}]}`
		_, err := ParseGeneratedDecisionQuestions([]byte(body))
		binary := instructions == "图片中是否有红色圆形？" || instructions == "Does the text say HELLO?"
		if (err == nil) != binary {
			t.Fatalf("incorrect generated binary validation for %q: %v", instructions, err)
		}
	}
}
