package video

import "testing"

func TestReadLiveness(t *testing.T) {
	arkNotFound := []byte(`{"error":{"code":"ResourceNotFound","message":"task not found"}}`)
	cases := []struct {
		name      string
		status    int
		body      []byte
		hinted    bool
		want      LivenessClass
		reachable bool
	}{
		{"real task read back", 200, []byte(`{"id":"cgt-1","status":"succeeded"}`), true, LivenessTask, true},
		{"gateway answers 200 about the sentinel", 200, []byte(`{"id":"lazu-liveness-check"}`), false, LivenessAnswered, true},
		{"ark does not know the sentinel", 404, arkNotFound, false, LivenessAnswered, true},
		{"expired hint", 404, arkNotFound, true, LivenessAnswered, true},
		{"malformed id refused after auth", 400, []byte(`{"error":{"code":"InvalidParameter"}}`), false, LivenessAnswered, true},
		{"key refused", 401, []byte(`{"error":{"code":"AuthenticationError"}}`), false, LivenessAuth, false},
		{"key forbidden", 403, nil, false, LivenessAuth, false},
		{"throttled", 429, nil, false, LivenessRateLimited, false},
		{"provider down", 503, []byte(`<html>bad gateway</html>`), false, LivenessUpstream, false},
		{"gateway does not mount the path", 404, []byte(`<html>404 page not found</html>`), false, LivenessUnmounted, false},
		{"method not served", 405, nil, false, LivenessUnmounted, false},
	}
	for _, tc := range cases {
		got := ReadLiveness(tc.status, tc.body, tc.hinted)
		if got.Class != tc.want || got.Reachable() != tc.reachable {
			t.Errorf("%s: ReadLiveness(%d) = %s reachable=%t, want %s reachable=%t",
				tc.name, tc.status, got.Class, got.Reachable(), tc.want, tc.reachable)
		}
		if !tc.reachable && got.Message == "" {
			t.Errorf("%s: an unreachable verdict must say why", tc.name)
		}
	}
}
