package video

import "testing"

func TestClassifyFailure(t *testing.T) {
	cases := []struct {
		code string
		want FailureClass
	}{
		// Ark moderation, before and after generation. These used to be
		// filed as channel outages because no marker matched them.
		{"InputTextSensitiveContentDetected", FailureCaller},
		{"InputImageSensitiveContentDetected", FailureCaller},
		{"OutputVideoSensitiveContentDetected", FailureCaller},
		{"OutputAudioSensitiveContentDetected.PolicyViolation", FailureCaller},
		{"InvalidParameter", FailureCaller},
		{"moderation_blocked", FailureCaller},
		{"content_filter", FailureCaller},
		{"INVALID_ARGUMENT", FailureCaller},
		{"video_provider_cancelled", FailureCaller},
		{"cancelled", FailureCaller},

		{"RateLimitExceeded", FailureRateLimited},
		{"QuotaExceeded", FailureRateLimited},
		{"RESOURCE_EXHAUSTED", FailureRateLimited},

		{"InternalServiceError", FailureUpstream},
		{"ModelLoadingError", FailureUpstream},
		{"ServerOverloaded", FailureUpstream},
		// A model the provider account cannot serve is a broken route, not a
		// bad request, even though its code says "Invalid".
		{"InvalidEndpointOrModel.NotFound", FailureUpstream},
		{"AuthenticationError", FailureUpstream},
		{"video_generation_timeout", FailureUpstream},
		{"video_provider_poll_rejected", FailureUpstream},
		{"video_artifact_missing", FailureUpstream},
		{"video_provider_failed", FailureUpstream},
		{"", FailureUpstream},
	}
	for _, tc := range cases {
		if got := ClassifyFailure(tc.code); got != tc.want {
			t.Errorf("ClassifyFailure(%q) = %d, want %d", tc.code, got, tc.want)
		}
	}
}
