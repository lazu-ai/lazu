package model

import "testing"

func TestDeriveModalityGenerationTags(t *testing.T) {
	result := DeriveModality("video generation,image generation,audio generation")

	if !SliceContains(result.Output, "video") {
		t.Fatalf("expected output to contain video")
	}
	if !SliceContains(result.Output, "image") {
		t.Fatalf("expected output to contain image")
	}
	if !SliceContains(result.Output, "audio") {
		t.Fatalf("expected output to contain audio")
	}

	if SliceContains(result.Input, "video") {
		t.Fatalf("video generation tag should not imply video input")
	}
	if SliceContains(result.Input, "audio") {
		t.Fatalf("audio generation tag should not imply audio input")
	}
}

func TestDeriveModalityNonGenerationInputTags(t *testing.T) {
	result := DeriveModality("vision,audio,video")

	if !SliceContains(result.Input, "image") {
		t.Fatalf("expected input to contain image for vision tag")
	}
	if !SliceContains(result.Input, "audio") {
		t.Fatalf("expected input to contain audio for audio tag")
	}
	if !SliceContains(result.Input, "video") {
		t.Fatalf("expected input to contain video for video tag")
	}
}
