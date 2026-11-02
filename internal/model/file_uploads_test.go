package model

import "testing"

func TestListUploadedFilesByOwnerLeavesOutResults(t *testing.T) {
	db, _ := newLedgerTestDBs(t)
	if err := db.AutoMigrate(&File{}); err != nil {
		t.Fatalf("migrate files: %v", err)
	}
	for _, file := range []File{
		{ID: "file-upload", UserID: 5, Filename: "notes.pdf", Purpose: "user_data", MimeType: "application/pdf", StorageKey: "a", CreatedAt: 10},
		{ID: "file-result", UserID: 5, Filename: "clip.mp4", Purpose: "user_data", MimeType: "video/mp4", StorageKey: "b", CreatedAt: 20},
		{ID: "file-other", UserID: 6, Filename: "theirs.png", Purpose: "vision", MimeType: "image/png", StorageKey: "c", CreatedAt: 30},
	} {
		if err := db.Create(&file).Error; err != nil {
			t.Fatalf("seed file: %v", err)
		}
	}
	if err := db.Create(&MediaJob{PublicID: "job-1", UserID: 5, Kind: MediaJobKindVideo, Status: MediaJobStatusSucceeded, ArtifactFileID: "file-result"}).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}
	files, err := ListUploadedFilesByOwner(5, 0)
	if err != nil || len(files) != 1 || files[0].ID != "file-upload" {
		t.Fatalf("uploads = %+v err=%v, want only file-upload", files, err)
	}
}
