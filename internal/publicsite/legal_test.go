package publicsite

import (
	"strings"
	"testing"
)

func TestLegalServerBodyAndSafeLegacyHTML(t *testing.T) {
	for _, source := range []string{
		"# Privacy Policy\n\n## Retention\n\nContent expires after 180 days.",
		`<h1>Privacy Policy</h1><h2>Retention</h2><p onclick="alert(1)">Content expires after 180 days.</p><script>alert(1)</script><a href="javascript:alert(1)">Unsafe</a><a href="https://lazu.ai/terms">Terms</a>`,
	} {
		body, err := RenderLegal(source, "en", "privacy")
		if err != nil {
			t.Fatal(err)
		}
		rendered := string(body)
		for _, want := range []string{"Content expires after 180 days.", `href="#legal-section-1"`, `id="legal-section-1"`, `href="https://lazu.ai/privacy.md"`} {
			if !strings.Contains(rendered, want) {
				t.Errorf("missing %q", want)
			}
		}
		for _, bad := range []string{"onclick=", "javascript:", "alert(1)"} {
			if strings.Contains(rendered, bad) {
				t.Errorf("unsafe %q", bad)
			}
		}
	}
}
