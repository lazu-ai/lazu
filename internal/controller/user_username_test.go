package controller

import (
	"sync"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
)

func seedUsernameUser(t *testing.T, username string) *model.User {
	t.Helper()
	user := &model.User{
		Username: username,
		Password: "placeholder-hash",
		Status:   1,
		Role:     1,
	}
	if err := model.CoreDB().Create(user).Error; err != nil {
		t.Fatalf("seed user %q: %v", username, err)
	}
	return user
}

func seedUsernamePassword(t *testing.T, userID int, password string) {
	t.Helper()
	hash, err := common.Password2Hash(password)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if err := model.CoreDB().Model(&model.User{}).Where("id = ?", userID).Updates(map[string]any{
		"password":       hash,
		"needs_password": false,
	}).Error; err != nil {
		t.Fatalf("seed password: %v", err)
	}
}

func TestCombinedUsernameAndPasswordUpdateIsAtomic(t *testing.T) {
	setupOIDCTestDB(t)
	owner := seedUsernameUser(t, "owner")
	seedUsernamePassword(t, owner.Id, "old-password")

	if _, _, err := applySelfProfileUpdate(owner.Id, "new-owner", "", "new-password", "wrong-password"); err == nil {
		t.Fatal("a wrong original password should reject the combined update")
	}
	var stored model.User
	if err := model.CoreDB().First(&stored, owner.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if stored.Username != "owner" || stored.UsernameChangedAt != 0 {
		t.Fatalf("failed password validation partially renamed the account: username=%q changed_at=%d", stored.Username, stored.UsernameChangedAt)
	}
	if !common.ValidatePasswordAndHash("old-password", stored.Password) {
		t.Fatal("failed combined update changed the stored password")
	}
}

func TestCombinedUsernameAndPasswordUpdateCommitsTogether(t *testing.T) {
	setupOIDCTestDB(t)
	owner := seedUsernameUser(t, "owner")
	seedUsernamePassword(t, owner.Id, "old-password")

	updated, verdict, err := applySelfProfileUpdate(owner.Id, "new-owner", "New Owner", "new-password", "old-password")
	if err != nil {
		t.Fatalf("combined update: %v", err)
	}
	if !verdict.Available || updated.Username != "new-owner" || updated.DisplayName != "New Owner" {
		t.Fatalf("combined update returned the wrong state: verdict=%+v user=%+v", verdict, updated)
	}
	var stored model.User
	if err := model.CoreDB().First(&stored, owner.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if stored.Username != "new-owner" || stored.UsernameChangedAt == 0 || !common.ValidatePasswordAndHash("new-password", stored.Password) {
		t.Fatalf("combined fields did not commit together: username=%q changed_at=%d", stored.Username, stored.UsernameChangedAt)
	}
}

func TestEvaluateUsernameChangeRejectsWhatItShould(t *testing.T) {
	setupOIDCTestDB(t)
	owner := seedUsernameUser(t, "owner")
	seedUsernameUser(t, "taken-handle")

	cases := []struct {
		candidate string
		name      string
		want      string
	}{
		{name: "unchanged", candidate: "owner", want: "unchanged"},
		{name: "surrounding space is trimmed, not rejected", candidate: "  owner  ", want: "unchanged"},
		{name: "too short", candidate: "a", want: "too_short"},
		{name: "too long", candidate: "abcdefghijklmnopqrstuvwxyz", want: "too_long"},
		{name: "space inside", candidate: "bad name", want: "invalid_format"},
		{name: "punctuation", candidate: "bad!name", want: "invalid_format"},
		{name: "cannot lead with a hyphen", candidate: "-lead", want: "invalid_format"},
		{name: "non-ascii", candidate: "用户名", want: "invalid_format"},
		{name: "reserved", candidate: "admin", want: "reserved"},
		{name: "reserved regardless of case", candidate: "Root", want: "reserved"},
		{name: "already taken", candidate: "taken-handle", want: "taken"},
		{name: "free", candidate: "brand-new_1", want: "available"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			verdict, err := evaluateUsernameChange(model.CoreDB(), owner, testCase.candidate)
			if err != nil {
				t.Fatalf("evaluate %q: %v", testCase.candidate, err)
			}
			if verdict.Status != testCase.want {
				t.Fatalf("candidate %q: got status %q, want %q", testCase.candidate, verdict.Status, testCase.want)
			}
			if verdict.Available != (testCase.want == "available") {
				t.Fatalf("candidate %q: available=%v disagrees with status %q", testCase.candidate, verdict.Available, verdict.Status)
			}
		})
	}
}

// A soft-deleted account still occupies the unique index, so its handle must
// not be advertised as free — the save would fail at the database.
func TestEvaluateUsernameChangeSeesSoftDeletedHandles(t *testing.T) {
	setupOIDCTestDB(t)
	owner := seedUsernameUser(t, "owner")
	retired := seedUsernameUser(t, "retired")
	if err := model.CoreDB().Delete(&model.User{}, retired.Id).Error; err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	verdict, err := evaluateUsernameChange(model.CoreDB(), owner, "retired")
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if verdict.Status != "taken" {
		t.Fatalf("a soft-deleted handle must read as taken, got %q", verdict.Status)
	}
}

func TestUsernameCooldownBlocksARepeatRename(t *testing.T) {
	setupOIDCTestDB(t)
	owner := seedUsernameUser(t, "owner")

	verdict, err := applyUsernameChange(owner.Id, "first-rename")
	if err != nil {
		t.Fatalf("first rename: %v", err)
	}
	if !verdict.Available {
		t.Fatalf("first rename should be allowed, got %q", verdict.Status)
	}

	verdict, err = applyUsernameChange(owner.Id, "second-rename")
	if err != nil {
		t.Fatalf("second rename: %v", err)
	}
	if verdict.Status != "cooldown" {
		t.Fatalf("second rename should hit the cooldown, got %q", verdict.Status)
	}
	if verdict.NextChangeAt <= time.Now().Unix() {
		t.Fatalf("cooldown verdict must say when the next rename is allowed, got %d", verdict.NextChangeAt)
	}

	var stored model.User
	if err := model.CoreDB().First(&stored, owner.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if stored.Username != "first-rename" {
		t.Fatalf("a refused rename must not write, username is %q", stored.Username)
	}
}

// An account that has never renamed — every row created before the column
// existed included — is free to rename now.
func TestUsernameCooldownIgnoresAccountsThatNeverRenamed(t *testing.T) {
	setupOIDCTestDB(t)
	owner := seedUsernameUser(t, "owner")
	if owner.UsernameChangedAt != 0 {
		t.Fatalf("a fresh account should carry no rename stamp, got %d", owner.UsernameChangedAt)
	}
	if _, locked := usernameChangeAllowedAt(owner); locked {
		t.Fatal("an account that never renamed must not be in cooldown")
	}
}

// The live check and the save are two round trips. Two accounts racing for the
// same free handle must produce one winner and one refusal, never two rows
// holding it or a raw unique-index error surfacing to the caller.
func TestConcurrentRenamesToTheSameHandleProduceOneWinner(t *testing.T) {
	setupOIDCTestDB(t)
	first := seedUsernameUser(t, "racer-one")
	second := seedUsernameUser(t, "racer-two")

	var wg sync.WaitGroup
	verdicts := make([]usernameVerdict, 2)
	errs := make([]error, 2)
	for index, userID := range []int{first.Id, second.Id} {
		wg.Add(1)
		go func(index, userID int) {
			defer wg.Done()
			verdicts[index], errs[index] = applyUsernameChange(userID, "contested")
		}(index, userID)
	}
	wg.Wait()

	winners := 0
	for index := range verdicts {
		if errs[index] != nil {
			t.Fatalf("rename %d returned an error instead of a verdict: %v", index, errs[index])
		}
		if verdicts[index].Available {
			winners++
		} else if verdicts[index].Status != "taken" {
			t.Fatalf("the losing rename should read as taken, got %q", verdicts[index].Status)
		}
	}
	if winners != 1 {
		t.Fatalf("exactly one rename should win, got %d", winners)
	}

	var holders int64
	if err := model.CoreDB().Model(&model.User{}).Where("username = ?", "contested").Count(&holders).Error; err != nil {
		t.Fatalf("count holders: %v", err)
	}
	if holders != 1 {
		t.Fatalf("exactly one account should hold the handle, got %d", holders)
	}
}

// The verdict the field renders and the verdict the save enforces come from one
// function; this pins that they cannot drift apart into a form that promises
// availability and then refuses it.
func TestApplyUsernameChangeRefusesWhatTheCheckRefuses(t *testing.T) {
	setupOIDCTestDB(t)
	owner := seedUsernameUser(t, "owner")
	seedUsernameUser(t, "occupied")

	for _, candidate := range []string{"occupied", "admin", "x", "bad name"} {
		checked, err := evaluateUsernameChange(model.CoreDB(), owner, candidate)
		if err != nil {
			t.Fatalf("check %q: %v", candidate, err)
		}
		applied, err := applyUsernameChange(owner.Id, candidate)
		if err != nil {
			t.Fatalf("apply %q: %v", candidate, err)
		}
		if checked.Status != applied.Status {
			t.Fatalf("candidate %q: check says %q but save says %q", candidate, checked.Status, applied.Status)
		}
		if applied.Available {
			t.Fatalf("candidate %q should never be savable", candidate)
		}
	}

	var stored model.User
	if err := model.CoreDB().First(&stored, owner.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if stored.Username != "owner" {
		t.Fatalf("no refused candidate may be written, username is %q", stored.Username)
	}
}
