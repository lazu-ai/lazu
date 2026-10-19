package service

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/model"
)

func TestIsSupportedMediaArtifactMIMERejectsActiveContent(t *testing.T) {
	cases := []struct {
		kind     string
		mimeType string
		want     bool
	}{
		{kind: model.MediaJobKindImage, mimeType: "image/png", want: true},
		{kind: model.MediaJobKindImage, mimeType: "image/svg+xml", want: false},
		{kind: model.MediaJobKindImage, mimeType: "text/html", want: false},
		{kind: model.MediaJobKindVideo, mimeType: "video/mp4; charset=binary", want: true},
		{kind: model.MediaJobKindVideo, mimeType: "application/javascript", want: false},
	}
	for _, testCase := range cases {
		if got := IsSupportedMediaArtifactMIME(testCase.kind, testCase.mimeType); got != testCase.want {
			t.Fatalf("IsSupportedMediaArtifactMIME(%q, %q)=%v want=%v", testCase.kind, testCase.mimeType, got, testCase.want)
		}
	}
}

func TestMediaArtifactsUseNormalRetentionUntilGalleryApproval(t *testing.T) {
	now := int64(1_700_000_000)
	if expiresAt := mediaJobArtifactExpiresAt(model.MediaJob{Source: "test-extension"}, now); expiresAt == nil || *expiresAt <= now {
		t.Fatalf("an extension's artifact should keep normal retention, got %v", expiresAt)
	}
	if expiresAt := mediaJobArtifactExpiresAt(model.MediaJob{Source: model.MediaJobSourceAPI}, now); expiresAt == nil || *expiresAt <= now {
		t.Fatalf("api artifact should keep normal retention, got %v", expiresAt)
	}
}
