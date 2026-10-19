package uploader

import (
	"errors"
	"testing"
)

func TestCompactorErrorFingerprintChangesWithRootCause(t *testing.T) {
	initFailure := compactorErrorFingerprint(errors.New("init s3 client: credentials unavailable"))
	listFailure := compactorErrorFingerprint(errors.New("list archive/requests/: context deadline exceeded"))
	if initFailure == listFailure {
		t.Fatalf("different compactor causes must produce different fingerprints: %q", initFailure)
	}
}
