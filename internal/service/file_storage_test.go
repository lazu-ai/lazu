package service

import "testing"

func TestDefaultFileExpiresAtKeepsFilesThirtyDays(t *testing.T) {
	if got := DefaultFileExpiresAt(1000); got != 1000+30*24*60*60 {
		t.Fatalf("DefaultFileExpiresAt(1000) = %d", got)
	}
}
