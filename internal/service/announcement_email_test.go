package service

import (
	"strings"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
)

func testAnnouncement(defaultLocale string) model.Announcement {
	published := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	return model.Announcement{
		ID:            42,
		Kind:          model.AnnouncementKindModelUpdate,
		TitleI18N:     model.JSONValue(`{"zh":"新模型上线","en":"New model available","zh-TW":"新模型上線"}`),
		BodyI18N:      model.JSONValue(`{"zh":["第一段","第二段 <b>"],"en":["First paragraph","Second paragraph"],"zh-TW":["第一段落"]}`),
		DefaultLocale: defaultLocale,
		PublishedAt:   &published,
	}
}

func TestAnnouncementMailContentPicksReaderLanguageThenDefaultLocale(t *testing.T) {
	a := testAnnouncement("en")
	for _, tc := range []struct {
		language, title, body string
	}{
		{"zh", "新模型上线", "第一段<br><br>第二段 &lt;b&gt;"},
		{"zh-TW", "新模型上線", "第一段落"},
		{"en-US", "New model available", "First paragraph<br><br>Second paragraph"},
		// No language chosen: the announcement's default locale, not zh.
		{"", "New model available", "First paragraph<br><br>Second paragraph"},
		// A language the announcement was not written in falls back to the
		// default locale too.
		{"ja", "New model available", "First paragraph<br><br>Second paragraph"},
	} {
		title, body := announcementMailContent(a, tc.language)
		if title != tc.title || string(body) != tc.body {
			t.Errorf("language %q: got %q / %q, want %q / %q", tc.language, title, body, tc.title, tc.body)
		}
	}
}

func TestAnnouncementMailableKinds(t *testing.T) {
	for kind, want := range map[string]bool{
		model.AnnouncementKindModelUpdate: true, model.AnnouncementKindOperational: true,
		model.AnnouncementKindMaintenance: true, model.AnnouncementKindIncident: true,
		model.AnnouncementKindReleaseNote: false, "": false,
	} {
		if got := AnnouncementMailable(kind); got != want {
			t.Errorf("AnnouncementMailable(%q) = %v, want %v", kind, got, want)
		}
	}
}

func TestSendPublishedAnnouncementEmailReachesEachReaderOnce(t *testing.T) {
	db := setupBalanceRunwayTestDB(t)
	oldSecret, oldResend, oldSMTP, oldRecorder := common.CryptoSecret, common.AppConfig.Resend.APIKey, common.SMTPServer, common.MailRecorder
	// No transport: every send fails locally and lands in the archive, which
	// is enough to see who was mailed what.
	common.CryptoSecret, common.AppConfig.Resend.APIKey, common.SMTPServer = "announcement-test", "", ""
	common.MailRecorder = model.RecordMailDelivery
	t.Cleanup(func() {
		common.CryptoSecret, common.AppConfig.Resend.APIKey, common.SMTPServer, common.MailRecorder = oldSecret, oldResend, oldSMTP, oldRecorder
	})

	users := []model.User{
		{Id: 21, Username: "zh", Email: "zh@example.test", Setting: `{"language":"zh"}`},
		{Id: 22, Username: "en", Email: "en@example.test", Setting: `{"language":"en"}`},
		{Id: 23, Username: "tw", Email: "tw@example.test", Setting: `{"language":"zh-TW"}`},
		{Id: 24, Username: "unset", Email: "unset@example.test"},
		{Id: 25, Username: "optout", Email: "optout@example.test", Setting: `{"product_updates_off":true}`},
		{Id: 26, Username: "noaddress"},
		{Id: 28, Username: "disabled", Email: "disabled@example.test", Status: common.UserStatusDisabled},
		{Id: 29, Username: "deleted", Email: "deleted@example.test"},
	}
	for i := range users {
		if users[i].Status == 0 {
			users[i].Status = common.UserStatusEnabled
		}
		if err := db.Create(&users[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Delete(&model.User{}, 29).Error; err != nil {
		t.Fatal(err)
	}

	a := testAnnouncement("en")
	stats, err := SendPublishedAnnouncementEmail(a)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Attempted != 4 || stats.Skipped != 2 {
		t.Fatalf("first publish stats = %+v", stats)
	}
	var rows []model.MailDelivery
	if err := db.Order("user_id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	wantSubjects := map[int]string{21: "新模型上线", 22: "New model available", 23: "新模型上線", 24: "New model available"}
	if len(rows) != len(wantSubjects) {
		t.Fatalf("archived %d rows, want %d: %+v", len(rows), len(wantSubjects), rows)
	}
	for _, row := range rows {
		if row.Kind != common.MailKindNotice || row.RefKey == nil || *row.RefKey != announcementMailRefKey(42, row.UserID) {
			t.Fatalf("row = %+v", row)
		}
		if row.Subject != wantSubjects[row.UserID] {
			t.Errorf("user %d subject = %q, want %q", row.UserID, row.Subject, wantSubjects[row.UserID])
		}
		body, err := row.HTML()
		if err != nil || !row.HasBody {
			t.Fatalf("user %d body not archived: %v", row.UserID, err)
		}
		if !strings.Contains(body, common.MailSiteURL("/console")) {
			t.Errorf("user %d body has no console link", row.UserID)
		}
	}

	// Publishing again reaches nobody already claimed, and a reader who
	// joined since gets it.
	if err := db.Create(&model.User{Id: 27, Username: "late", Email: "late@example.test", Status: common.UserStatusEnabled}).Error; err != nil {
		t.Fatal(err)
	}
	stats, err = SendPublishedAnnouncementEmail(a)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Attempted != 1 || stats.Skipped != 6 {
		t.Fatalf("republish stats = %+v", stats)
	}
	var count int64
	db.Model(&model.MailDelivery{}).Count(&count)
	if count != 5 {
		t.Fatalf("archive rows after republish = %d, want 5", count)
	}
}

func TestSendPublishedAnnouncementEmailRefusesUnpublishedAndReleaseNotes(t *testing.T) {
	draft := testAnnouncement("zh")
	draft.PublishedAt = nil
	if _, err := SendPublishedAnnouncementEmail(draft); err == nil {
		t.Fatal("an unpublished announcement was mailed")
	}
	note := testAnnouncement("zh")
	note.Kind = model.AnnouncementKindReleaseNote
	if _, err := SendPublishedAnnouncementEmail(note); err == nil {
		t.Fatal("a release note was mailed")
	}
}
