package model

import (
	"fmt"
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
)

func TestUserTokenStatusQueriesFilterBeforePagination(t *testing.T) {
	db := setupTokenKeyStorageTestDB(t, "token_status_filter")
	withTokenKeyStorageGlobals(t, db)

	statuses := []int{
		common.TokenStatusEnabled,
		common.TokenStatusDisabled,
		common.TokenStatusDisabled,
		common.TokenStatusExpired,
	}
	for index, status := range statuses {
		token := &Token{
			UserId:         91,
			Name:           fmt.Sprintf("status-%d", index),
			Key:            fmt.Sprintf("sk-lazu-status-filter-%d-abcdefghijklmnopqrstuvwxyz", index), // secret-scan: allow -- test fixture
			Status:         status,
			ExpiredTime:    -1,
			RemainQuota:    100,
			UnlimitedQuota: true,
		}
		if err := token.Insert(); err != nil {
			t.Fatalf("insert token %d: %v", index, err)
		}
	}

	page, err := GetAllUserTokensByStatus(91, common.TokenStatusDisabled, 0, 1)
	if err != nil {
		t.Fatalf("list disabled tokens: %v", err)
	}
	if len(page) != 1 || page[0].Status != common.TokenStatusDisabled {
		t.Fatalf("unexpected filtered page: %#v", page)
	}

	total, err := CountUserTokensByStatus(91, common.TokenStatusDisabled)
	if err != nil {
		t.Fatalf("count disabled tokens: %v", err)
	}
	if total != 2 {
		t.Fatalf("expected 2 disabled tokens, got %d", total)
	}

	search, err := SearchUserTokensByStatus(91, "status-", "", common.TokenStatusExpired)
	if err != nil {
		t.Fatalf("search expired tokens: %v", err)
	}
	if len(search) != 1 || search[0].Status != common.TokenStatusExpired {
		t.Fatalf("unexpected filtered search: %#v", search)
	}

	padded, err := SearchUserTokensByStatus(91, "  status-3 ", "", 0)
	if err != nil {
		t.Fatalf("search with padded keyword: %v", err)
	}
	if len(padded) != 1 || padded[0].Name != "status-3" {
		t.Fatalf("padded keyword should match status-3, got %#v", padded)
	}
}
