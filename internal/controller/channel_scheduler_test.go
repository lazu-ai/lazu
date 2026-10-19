package controller

import "testing"

func TestChannelTestLeaseCanBeReleased(t *testing.T) {
	releaseAllChannelsTest()
	if err := acquireAllChannelsTest(); err != nil {
		t.Fatalf("first channel test lease failed: %v", err)
	}
	if err := acquireAllChannelsTest(); err == nil {
		t.Fatal("expected concurrent channel test lease to be rejected")
	}
	releaseAllChannelsTest()
	if err := acquireAllChannelsTest(); err != nil {
		t.Fatalf("released channel test lease was not reusable: %v", err)
	}
	releaseAllChannelsTest()
}
