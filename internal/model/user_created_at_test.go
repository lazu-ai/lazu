package model

import (
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestUserInsertStampsExactCreatedAt(t *testing.T) {
	db, err := gorm.Open(gormsqlite.Open("file:user_created_at?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&User{}); err != nil {
		t.Fatal(err)
	}
	before := time.Now().Unix()
	fresh := &User{Username: "fresh", Password: "x"}
	if err := db.Create(fresh).Error; err != nil {
		t.Fatal(err)
	}
	// An explicit time (imports, seeded accounts) is kept as given.
	imported := &User{Username: "imported", Password: "x", CreatedAt: 1700000000}
	if err := db.Create(imported).Error; err != nil {
		t.Fatal(err)
	}
	var rows []User
	if err := db.Order("id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if rows[0].CreatedAt < before || rows[0].CreatedAtSource != UserCreatedAtSourceExact {
		t.Fatalf("fresh created_at=%d source=%q", rows[0].CreatedAt, rows[0].CreatedAtSource)
	}
	if rows[1].CreatedAt != 1700000000 {
		t.Fatalf("imported created_at=%d", rows[1].CreatedAt)
	}
}
