package publicsite

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestShareImageVersion fails when apps/web/public/og-image.png changes
// without a new ?v= in OGImagePath, or when the web and docs builds emit a
// different share image URL than the public pages do.
func TestShareImageVersion(t *testing.T) {
	root := filepath.Join("..", "..")
	png, err := os.ReadFile(filepath.Join(root, "apps", "web", "public", "og-image.png"))
	if err != nil {
		t.Fatalf("read share image: %v", err)
	}
	sum := sha256.Sum256(png)
	want := "/og-image.png?v=" + hex.EncodeToString(sum[:])[:10]
	if OGImagePath != want {
		t.Fatalf("OGImagePath = %q, want %q (bump it with the image)", OGImagePath, want)
	}
	for _, rel := range []string{
		filepath.Join("apps", "web", "scripts", "generate-static-pages.mjs"),
		filepath.Join("apps", "docs", "scripts", "postbuild-geo.ts"),
	} {
		src, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if !strings.Contains(string(src), want) {
			t.Errorf("%s does not reference %s", rel, want)
		}
	}
}

func TestImageAltFollowsTheImage(t *testing.T) {
	site := NewMeta("en", "models", "/models", "Models", "All models")
	if got := site.ImageAlt(); got != OGImageAlt {
		t.Fatalf("site card alt = %q, want %q", got, OGImageAlt)
	}
	own := site
	own.OGImage = SiteOrigin + "/blog/covers/post.png"
	if got := own.ImageAlt(); got != "" {
		t.Fatalf("own image without alt got %q, want none", got)
	}
	own.OGImageAlt = "A chart"
	if got := own.ImageAlt(); got != "A chart" {
		t.Fatalf("own alt = %q", got)
	}
}
