package service

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
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

func TestAPIVideosUseTheConfiguredRetention(t *testing.T) {
	original := common.AppConfig.ArtifactStorage.APIVideoTTLHours
	t.Cleanup(func() { common.AppConfig.ArtifactStorage.APIVideoTTLHours = original })
	model.RegisterMediaJobSource("retention-test-extension")
	now := int64(1_700_000_000)
	expires := func(job model.MediaJob) int64 {
		t.Helper()
		at := mediaJobArtifactExpiresAt(job, now)
		if at == nil {
			t.Fatalf("no expiry for %+v", job)
		}
		return *at
	}

	common.AppConfig.ArtifactStorage.APIVideoTTLHours = 72
	if got := expires(model.MediaJob{Kind: model.MediaJobKindVideo, Source: model.MediaJobSourceAPI}); got != now+72*3600 {
		t.Fatalf("an API video keeps 72 hours, got %d", got-now)
	}
	if got := expires(model.MediaJob{Kind: model.MediaJobKindVideo}); got != now+72*3600 {
		t.Fatalf("a video with no source is an API video, got %d", got-now)
	}
	if got := expires(model.MediaJob{Kind: model.MediaJobKindVideo, Source: "retention-test-extension"}); got != DefaultFileExpiresAt(now) {
		t.Fatalf("an extension's video (a Studio work) keeps the file default, got %d", got-now)
	}
	if got := expires(model.MediaJob{Kind: model.MediaJobKindImage, Source: model.MediaJobSourceAPI}); got != DefaultFileExpiresAt(now) {
		t.Fatalf("an API image keeps the file default, got %d", got-now)
	}

	common.AppConfig.ArtifactStorage.APIVideoTTLHours = 0
	if got := expires(model.MediaJob{Kind: model.MediaJobKindVideo, Source: model.MediaJobSourceAPI}); got != DefaultFileExpiresAt(now) {
		t.Fatalf("unset keeps the file default, got %d", got-now)
	}
}
