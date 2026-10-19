package model

import (
	"fmt"
	"strings"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupAnnouncementTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sqlite db handle: %v", err)
	}
	previousDB := coreDB
	coreDB = db
	t.Cleanup(func() {
		coreDB = previousDB
		if err := sqlDB.Close(); err != nil {
			t.Fatalf("close sqlite: %v", err)
		}
	})
	if err := db.AutoMigrate(&Announcement{}, &AnnouncementUserState{}, &ReleaseDeployment{}, &Option{}); err != nil {
		t.Fatalf("migrate announcements: %v", err)
	}
	return db
}

func TestDocsChangelogAudiencePaginationAndBackfill(t *testing.T) {
	db := setupAnnouncementTestDB(t)
	base := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	makeRow := func(title string, audience []string, published *time.Time) *Announcement {
		t.Helper()
		row, err := UpsertAnnouncement(AnnouncementUpsert{
			Kind: AnnouncementKindReleaseNote, TitleI18N: map[string]string{"en": title, "zh": "中文"},
			SummaryI18N: map[string]string{"en": "Summary"}, BodyI18N: map[string]any{"en": []string{"Body"}},
			DefaultLocale: "en", Audiences: &audience, PublishedAt: published, PublishedAtSet: published != nil,
		})
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	newer := base.Add(24 * time.Hour)
	older := base.Add(-24 * time.Hour)
	first := makeRow("first", []string{"docs"}, &newer)
	_ = makeRow("console", []string{"console"}, &newer)
	second := makeRow("second", []string{"docs"}, &older)
	_ = makeRow("draft", []string{"docs"}, nil)
	consoleItems, err := ListPublishedAnnouncements(10, "en", 0)
	if err != nil || len(consoleItems) != 1 || consoleItems[0].Title != "console" {
		t.Fatalf("console feed: %#v %v", consoleItems, err)
	}
	page, cursor, err := ListDocsChangelog("ja", 1, 0)
	if err != nil || len(page) != 1 || page[0].ID != first.ID || cursor != first.ID {
		t.Fatalf("first page: %#v %d %v", page, cursor, err)
	}
	page, cursor, err = ListDocsChangelog("ja", 1, cursor)
	if err != nil || len(page) != 1 || page[0].ID != second.ID || page[0].Title != "second" || cursor != 0 {
		t.Fatalf("second page: %#v %d %v", page, cursor, err)
	}
	if err := db.Model(second).Updates(map[string]any{"audiences": nil, "version": ""}).Error; err != nil {
		t.Fatal(err)
	}
	deployment := ReleaseDeployment{Version: "v0.0.1", CommitSHA: "abc1234", ReleaseNoteStatus: ReleaseNoteStatusGenerated}
	if err := db.Create(&deployment).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(second).Updates(map[string]any{"source": AnnouncementSourceDeploy, "source_deployment_id": deployment.ID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := BackfillAnnouncementPublicationFields(db); err != nil {
		t.Fatal(err)
	}
	var backfilled Announcement
	if err := db.First(&backfilled, second.ID).Error; err != nil {
		t.Fatal(err)
	}
	if backfilled.Version != "v0.0.1" || string(backfilled.Audiences) != `["console"]` {
		t.Fatalf("backfill: %#v", backfilled)
	}
}

func TestAnnouncementLocaleDismissAndLegacyMigration(t *testing.T) {
	db := setupAnnouncementTestDB(t)

	row, err := UpsertAnnouncement(AnnouncementUpsert{
		Kind:          AnnouncementKindMaintenance,
		TitleI18N:     map[string]string{"zh-CN": "新状态页", "en-US": "New status page"},
		SummaryI18N:   map[string]string{"zh-CN": "上线状态和更新入口", "en-US": "Status and update surfaces"},
		BodyI18N:      map[string]any{"zh-CN": []string{"状态页上线"}, "en-US": []string{"Status is live"}},
		DefaultLocale: "zh-CN",
		Source:        AnnouncementSourceManual,
		Published:     true,
	})
	if err != nil {
		t.Fatalf("upsert announcement: %v", err)
	}

	unseen, err := ListUnseenAnnouncements(42, "zh-CN", 20)
	if err != nil {
		t.Fatalf("list unseen: %v", err)
	}
	if len(unseen) != 1 || unseen[0].ID != row.ID {
		t.Fatalf("expected one unseen announcement, got %#v", unseen)
	}
	if unseen[0].Title != "新状态页" || unseen[0].TitleI18N["zh"] != "新状态页" {
		t.Fatalf("locale normalization failed: %#v", unseen[0])
	}
	if _, ok := unseen[0].TitleI18N["zh-CN"]; ok {
		t.Fatalf("unexpected non-canonical locale persisted: %#v", unseen[0].TitleI18N)
	}

	var state AnnouncementUserState
	if err := db.Where("announcement_id = ? AND user_id = ?", row.ID, 42).First(&state).Error; err != nil {
		t.Fatalf("first shown state was not recorded: %v", err)
	}
	if state.FirstShownAt == nil || state.DismissedAt != nil {
		t.Fatalf("unexpected initial state: %#v", state)
	}

	if err := DismissAnnouncement(row.ID, 42); err != nil {
		t.Fatalf("dismiss announcement: %v", err)
	}
	unseen, err = ListUnseenAnnouncements(42, "zh-CN", 20)
	if err != nil {
		t.Fatalf("list unseen after dismiss: %v", err)
	}
	if len(unseen) != 0 {
		t.Fatalf("dismissed announcement should not be unseen: %#v", unseen)
	}

	publishDate := time.Now().UTC().AddDate(0, 0, -1).Format(time.RFC3339)
	legacy := fmt.Sprintf(
		`[{"title":"旧公告","content":"迁移到新公告表","type":"maintenance","publishDate":%q}]`,
		publishDate,
	)
	if err := db.Create(&Option{Key: "console_setting.announcements", Value: legacy}).Error; err != nil {
		t.Fatalf("create legacy option: %v", err)
	}
	if err := MigrateLegacyAnnouncements(db); err != nil {
		t.Fatalf("migrate legacy announcements: %v", err)
	}
	items, err := ListPublishedAnnouncements(20, "en-US", 0)
	if err != nil {
		t.Fatalf("list migrated announcements: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected original plus migrated announcements, got %d", len(items))
	}
}

func TestUpsertAnnouncementRespectsUnpublishedCreate(t *testing.T) {
	setupAnnouncementTestDB(t)

	row, err := UpsertAnnouncement(AnnouncementUpsert{
		Kind:          AnnouncementKindMaintenance,
		TitleI18N:     map[string]string{"zh": "未发布公告"},
		SummaryI18N:   map[string]string{"zh": "不应该出现在公开列表"},
		BodyI18N:      map[string]any{"zh": []string{"草稿内容"}},
		DefaultLocale: "zh",
		Source:        AnnouncementSourceManual,
		Published:     false,
	})
	if err != nil {
		t.Fatalf("create unpublished announcement: %v", err)
	}
	if row.PublishedAt != nil {
		t.Fatalf("unpublished create should not set published_at: %#v", row.PublishedAt)
	}
	items, err := ListPublishedAnnouncements(20, "zh", 0)
	if err != nil {
		t.Fatalf("list published announcements: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("unpublished announcement should not be listed: %#v", items)
	}

	if _, err := UpsertAnnouncement(AnnouncementUpsert{
		ID:            row.ID,
		Kind:          AnnouncementKindMaintenance,
		TitleI18N:     map[string]string{"zh": "已发布公告"},
		SummaryI18N:   map[string]string{"zh": "现在可以公开"},
		BodyI18N:      map[string]any{"zh": []string{"发布内容"}},
		DefaultLocale: "zh",
		Source:        AnnouncementSourceManual,
		Published:     true,
	}); err != nil {
		t.Fatalf("publish announcement: %v", err)
	}
	items, err = ListPublishedAnnouncements(20, "zh", 0)
	if err != nil {
		t.Fatalf("list published announcements after publish: %v", err)
	}
	if len(items) != 1 || items[0].ID != row.ID {
		t.Fatalf("published announcement should be listed: %#v", items)
	}

	if _, err := UpsertAnnouncement(AnnouncementUpsert{
		ID:            row.ID,
		Kind:          AnnouncementKindMaintenance,
		TitleI18N:     map[string]string{"zh": "再次隐藏"},
		SummaryI18N:   map[string]string{"zh": "仍然公开"},
		BodyI18N:      map[string]any{"zh": []string{"保存编辑内容"}},
		DefaultLocale: "zh",
		Source:        AnnouncementSourceManual,
		Published:     false,
	}); err != nil {
		t.Fatalf("save announcement draft through upsert: %v", err)
	}
	items, err = ListPublishedAnnouncements(20, "zh", 0)
	if err != nil {
		t.Fatalf("list published announcements after save: %v", err)
	}
	if len(items) != 1 || items[0].ID != row.ID {
		t.Fatalf("saving with published=false must preserve publication state: %#v", items)
	}
	if err := UnpublishAnnouncement(row.ID); err != nil {
		t.Fatalf("unpublish announcement: %v", err)
	}
	items, err = ListPublishedAnnouncements(20, "zh", 0)
	if err != nil {
		t.Fatalf("list published announcements after unpublish: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("dedicated unpublish should hide announcement: %#v", items)
	}
}

func TestListAdminAnnouncementsUsesStableCursor(t *testing.T) {
	setupAnnouncementTestDB(t)
	for index := 0; index < 5; index++ {
		if _, err := UpsertAnnouncement(AnnouncementUpsert{
			Kind:          AnnouncementKindOperational,
			TitleI18N:     map[string]string{"zh": fmt.Sprintf("公告 %d", index)},
			SummaryI18N:   map[string]string{"zh": "摘要"},
			BodyI18N:      map[string]any{"zh": []string{"内容"}},
			DefaultLocale: "zh",
			Source:        AnnouncementSourceManual,
		}); err != nil {
			t.Fatalf("create announcement %d: %v", index, err)
		}
	}

	first, cursor, err := ListAdminAnnouncements(2, "zh", 0)
	if err != nil {
		t.Fatalf("list first admin page: %v", err)
	}
	if len(first) != 2 || cursor == 0 {
		t.Fatalf("expected a full first page and cursor, got items=%d cursor=%d", len(first), cursor)
	}
	second, nextCursor, err := ListAdminAnnouncements(2, "zh", cursor)
	if err != nil {
		t.Fatalf("list second admin page: %v", err)
	}
	if len(second) != 2 || nextCursor == 0 || second[0].ID >= first[len(first)-1].ID {
		t.Fatalf("expected non-overlapping second page, got first=%#v second=%#v cursor=%d", first, second, nextCursor)
	}
	last, finalCursor, err := ListAdminAnnouncements(2, "zh", nextCursor)
	if err != nil {
		t.Fatalf("list final admin page: %v", err)
	}
	if len(last) != 1 || finalCursor != 0 {
		t.Fatalf("expected one final item and no cursor, got items=%d cursor=%d", len(last), finalCursor)
	}
}

func TestListUnseenAnnouncementsAppliesLimitAfterFilteringDismissed(t *testing.T) {
	setupAnnouncementTestDB(t)
	userID := 42
	for index := 0; index < 5; index++ {
		row, err := UpsertAnnouncement(AnnouncementUpsert{
			Kind:          AnnouncementKindMaintenance,
			TitleI18N:     map[string]string{"zh": fmt.Sprintf("公告 %d", index)},
			SummaryI18N:   map[string]string{"zh": "摘要"},
			BodyI18N:      map[string]any{"zh": []string{"内容"}},
			DefaultLocale: "zh",
			Source:        AnnouncementSourceManual,
			Published:     true,
			PublishedAt:   timePtr(time.Date(2026, 5, 30, index, 0, 0, 0, time.UTC)),
		})
		if err != nil {
			t.Fatalf("create announcement %d: %v", index, err)
		}
		if index >= 3 {
			if err := DismissAnnouncement(row.ID, userID); err != nil {
				t.Fatalf("dismiss newest announcement %d: %v", index, err)
			}
		}
	}

	unseen, err := ListUnseenAnnouncements(userID, "zh", 2)
	if err != nil {
		t.Fatalf("list unseen announcements: %v", err)
	}
	if len(unseen) != 2 {
		t.Fatalf("expected older unread announcements after filtering dismissed rows, got %#v", unseen)
	}
	if unseen[0].Title != "公告 2" || unseen[1].Title != "公告 1" {
		t.Fatalf("unexpected unseen announcements: %#v", unseen)
	}
}

func TestAnnouncementDeliveryControlsUnreadAttention(t *testing.T) {
	setupAnnouncementTestDB(t)
	userID := 42
	for _, delivery := range []string{
		AnnouncementDeliveryChangelog,
		AnnouncementDeliveryNotice,
		AnnouncementDeliveryCritical,
	} {
		if _, err := UpsertAnnouncement(AnnouncementUpsert{
			Kind:          AnnouncementKindReleaseNote,
			TitleI18N:     map[string]string{"zh": delivery},
			SummaryI18N:   map[string]string{"zh": "摘要"},
			BodyI18N:      map[string]any{"zh": []string{"内容"}},
			DefaultLocale: "zh",
			Source:        AnnouncementSourceDeploy,
			Delivery:      delivery,
			Published:     true,
		}); err != nil {
			t.Fatalf("create %s announcement: %v", delivery, err)
		}
	}

	unseen, err := ListUnseenAnnouncements(userID, "zh", 20)
	if err != nil {
		t.Fatalf("list unseen announcements: %v", err)
	}
	if len(unseen) != 2 {
		t.Fatalf("expected notice and critical announcements only, got %#v", unseen)
	}
	deliveries := map[string]bool{}
	for _, item := range unseen {
		deliveries[item.Delivery] = true
	}
	if !deliveries[AnnouncementDeliveryNotice] || !deliveries[AnnouncementDeliveryCritical] {
		t.Fatalf("unexpected unseen delivery levels: %#v", deliveries)
	}

	published, err := ListPublishedAnnouncements(20, "zh", userID)
	if err != nil {
		t.Fatalf("list published announcements: %v", err)
	}
	for _, item := range published {
		if item.Delivery == AnnouncementDeliveryChangelog && item.IsUnread {
			t.Fatalf("changelog should not be unread: %#v", item)
		}
	}
}

func TestHasUnseenCriticalAnnouncementIsNotLimitedByFeedPage(t *testing.T) {
	setupAnnouncementTestDB(t)
	userID := 42
	base := time.Date(2026, 5, 30, 0, 0, 0, 0, time.UTC)

	critical, err := UpsertAnnouncement(AnnouncementUpsert{
		Kind:          AnnouncementKindIncident,
		TitleI18N:     map[string]string{"zh": "紧急公告"},
		SummaryI18N:   map[string]string{"zh": "摘要"},
		BodyI18N:      map[string]any{"zh": []string{"内容"}},
		DefaultLocale: "zh",
		Source:        AnnouncementSourceManual,
		Delivery:      AnnouncementDeliveryCritical,
		DeliverySet:   true,
		Published:     true,
		PublishedAt:   timePtr(base),
	})
	if err != nil {
		t.Fatalf("create critical announcement: %v", err)
	}
	for index := 1; index <= 20; index++ {
		if _, err := UpsertAnnouncement(AnnouncementUpsert{
			Kind:          AnnouncementKindOperational,
			TitleI18N:     map[string]string{"zh": fmt.Sprintf("普通公告 %d", index)},
			SummaryI18N:   map[string]string{"zh": "摘要"},
			BodyI18N:      map[string]any{"zh": []string{"内容"}},
			DefaultLocale: "zh",
			Source:        AnnouncementSourceManual,
			Delivery:      AnnouncementDeliveryNotice,
			DeliverySet:   true,
			Published:     true,
			PublishedAt:   timePtr(base.Add(time.Duration(index) * time.Minute)),
		}); err != nil {
			t.Fatalf("create notice %d: %v", index, err)
		}
	}

	unseen, err := ListUnseenAnnouncements(userID, "zh", 20)
	if err != nil {
		t.Fatalf("list unseen announcements: %v", err)
	}
	if len(unseen) != 20 || unseen[len(unseen)-1].ID == critical.ID {
		t.Fatalf("expected the critical announcement beyond the first page, got %#v", unseen)
	}
	hasCritical, err := HasUnseenCriticalAnnouncement(userID)
	if err != nil || !hasCritical {
		t.Fatalf("expected an unread critical announcement, has=%t err=%v", hasCritical, err)
	}
	criticalItem, err := UnseenCriticalAnnouncement(userID, "zh")
	if err != nil || criticalItem == nil || criticalItem.ID != critical.ID {
		t.Fatalf("expected the older critical item, item=%#v err=%v", criticalItem, err)
	}
	if err := DismissAnnouncement(critical.ID, userID); err != nil {
		t.Fatalf("dismiss critical announcement: %v", err)
	}
	hasCritical, err = HasUnseenCriticalAnnouncement(userID)
	if err != nil || hasCritical {
		t.Fatalf("expected no unread critical announcement after dismiss, has=%t err=%v", hasCritical, err)
	}
}

func TestAnnouncementUpdatePreservesDeliveryUnlessExplicitlyChanged(t *testing.T) {
	setupAnnouncementTestDB(t)
	deploymentID := uint(7)
	row, err := UpsertAnnouncement(AnnouncementUpsert{
		Kind:               AnnouncementKindReleaseNote,
		TitleI18N:          map[string]string{"zh": "需要操作"},
		SummaryI18N:        map[string]string{"zh": "摘要"},
		BodyI18N:           map[string]any{"zh": []string{"内容"}},
		DefaultLocale:      "zh",
		Source:             AnnouncementSourceDeploy,
		SourceModel:        "gemini-3.7-flash",
		SourceCommitFrom:   "1234567",
		SourceCommitTo:     "abcdef1",
		SourceDeploymentID: &deploymentID,
		Delivery:           AnnouncementDeliveryCritical,
		DeliverySet:        true,
		Published:          false,
	})
	if err != nil {
		t.Fatalf("create critical draft: %v", err)
	}
	updated, err := UpsertAnnouncement(AnnouncementUpsert{
		ID:            row.ID,
		Kind:          row.Kind,
		TitleI18N:     map[string]string{"zh": "需要操作"},
		SummaryI18N:   map[string]string{"zh": "已审核"},
		BodyI18N:      map[string]any{"zh": []string{"内容"}},
		DefaultLocale: "zh",
		Published:     true,
	})
	if err != nil {
		t.Fatalf("publish generated draft: %v", err)
	}
	if updated.Delivery != AnnouncementDeliveryCritical {
		t.Fatalf("publishing should preserve draft delivery, got %#v", updated)
	}
	if updated.Source != AnnouncementSourceDeploy || updated.SourceModel != "gemini-3.7-flash" || updated.SourceCommitFrom != "1234567" || updated.SourceCommitTo != "abcdef1" || updated.SourceDeploymentID == nil || *updated.SourceDeploymentID != deploymentID {
		t.Fatalf("publishing should preserve deploy provenance, got %#v", updated)
	}
}

func TestSyncLegacyAnnouncementsReplacesRows(t *testing.T) {
	setupAnnouncementTestDB(t)

	firstPublishDate := time.Now().UTC().AddDate(0, 0, -2).Format(time.RFC3339)
	first := fmt.Sprintf(
		`[{"title":"旧公告 A","content":"第一版","type":"maintenance","publishDate":%q}]`,
		firstPublishDate,
	)
	if err := SyncLegacyAnnouncements(first); err != nil {
		t.Fatalf("sync first legacy announcements: %v", err)
	}
	firstItems, err := ListPublishedAnnouncements(20, "zh", 0)
	if err != nil {
		t.Fatalf("list first synced legacy announcements: %v", err)
	}
	if len(firstItems) != 1 {
		t.Fatalf("expected one first legacy announcement, got %#v", firstItems)
	}
	if err := DismissAnnouncement(firstItems[0].ID, 42); err != nil {
		t.Fatalf("dismiss first legacy announcement: %v", err)
	}
	secondPublishDate := time.Now().UTC().AddDate(0, 0, -1).Format(time.RFC3339)
	second := fmt.Sprintf(
		`[{"title":"旧公告 B","content":"第二版","type":"maintenance","publishDate":%q}]`,
		secondPublishDate,
	)
	if err := SyncLegacyAnnouncements(second); err != nil {
		t.Fatalf("sync replacement legacy announcements: %v", err)
	}
	items, err := ListPublishedAnnouncements(20, "zh", 0)
	if err != nil {
		t.Fatalf("list synced legacy announcements: %v", err)
	}
	if len(items) != 1 || items[0].Title != "旧公告 B" {
		t.Fatalf("legacy sync should replace rows, got %#v", items)
	}
	if items[0].ID != firstItems[0].ID {
		t.Fatalf("legacy sync should preserve row identity, before=%d after=%d", firstItems[0].ID, items[0].ID)
	}
	unseen, err := ListUnseenAnnouncements(42, "zh", 20)
	if err != nil {
		t.Fatalf("list unseen after legacy sync: %v", err)
	}
	if len(unseen) != 0 {
		t.Fatalf("legacy sync should preserve dismissal state: %#v", unseen)
	}
}

func TestRecordReleaseDeploymentWithAnnouncementIsIdempotent(t *testing.T) {
	db := setupAnnouncementTestDB(t)
	deployedAt := time.Date(2026, 5, 30, 3, 0, 0, 0, time.UTC)
	deployment := &ReleaseDeployment{
		Version:           "v1",
		CommitSHA:         "abcdef1",
		PreviousCommitSHA: "1234567",
		DeployedAt:        deployedAt,
		ReleaseNoteStatus: ReleaseNoteStatusSkipped,
	}
	announcementInput := &AnnouncementUpsert{
		Kind:             AnnouncementKindReleaseNote,
		TitleI18N:        map[string]string{"zh": "发布说明", "en": "Release notes"},
		SummaryI18N:      map[string]string{"zh": "状态页上线", "en": "Status page is live"},
		BodyI18N:         map[string]any{"zh": []string{"新增状态页"}, "en": []string{"Added status page"}},
		DefaultLocale:    "zh",
		Source:           AnnouncementSourceDeploy,
		SourceModel:      "deepseek-v4-flash",
		SourceCommitFrom: "1234567",
		SourceCommitTo:   "abcdef1",
		Published:        true,
	}

	savedDeployment, savedAnnouncement, err := RecordReleaseDeploymentWithAnnouncement(deployment, announcementInput)
	if err != nil {
		t.Fatalf("record deployment with announcement: %v", err)
	}
	if savedDeployment.ReleaseNoteStatus != ReleaseNoteStatusGenerated || savedAnnouncement == nil {
		t.Fatalf("expected generated release note, got deployment=%#v announcement=%#v", savedDeployment, savedAnnouncement)
	}
	if savedAnnouncement.SourceDeploymentID == nil || *savedAnnouncement.SourceDeploymentID != savedDeployment.ID {
		t.Fatalf("announcement should point at deployment: %#v", savedAnnouncement)
	}

	retryDeployment, retryAnnouncement, err := RecordReleaseDeploymentWithAnnouncement(&ReleaseDeployment{
		Version:           "v1-retry",
		CommitSHA:         "abcdef1",
		PreviousCommitSHA: "1234567",
		DeployedAt:        deployedAt.Add(time.Hour),
		ReleaseNoteStatus: ReleaseNoteStatusFailed,
		ReleaseNoteError:  "retry should not overwrite generated notes",
	}, nil)
	if err != nil {
		t.Fatalf("record duplicate deployment: %v", err)
	}
	if retryDeployment.ID != savedDeployment.ID || retryDeployment.ReleaseNoteStatus != ReleaseNoteStatusGenerated {
		t.Fatalf("duplicate range should preserve generated deployment: %#v", retryDeployment)
	}
	if retryAnnouncement == nil || retryAnnouncement.ID != savedAnnouncement.ID {
		t.Fatalf("duplicate range should return original announcement: %#v", retryAnnouncement)
	}

	var count int64
	if err := db.Model(&Announcement{}).Count(&count).Error; err != nil {
		t.Fatalf("count announcements: %v", err)
	}
	if count != 1 {
		t.Fatalf("duplicate deployment created %d announcements", count)
	}
}

func TestSilentReleaseDeploymentRetryReusesCommitRecord(t *testing.T) {
	db := setupAnnouncementTestDB(t)
	first, _, err := RecordReleaseDeploymentWithAnnouncement(&ReleaseDeployment{
		Version:           "v1.2.3",
		CommitSHA:         "abcdef1",
		PreviousCommitSHA: "1234567",
		ReleaseNoteStatus: ReleaseNoteStatusSkipped,
	}, nil)
	if err != nil {
		t.Fatalf("record initial deployment: %v", err)
	}
	retry, _, err := RecordReleaseDeploymentWithAnnouncement(&ReleaseDeployment{
		Version:           "v1.2.3",
		CommitSHA:         "abcdef1",
		PreviousCommitSHA: "abcdef1",
		ReleaseNoteStatus: ReleaseNoteStatusSkipped,
	}, nil)
	if err != nil {
		t.Fatalf("record silent retry: %v", err)
	}
	if retry.ID != first.ID || retry.PreviousCommitSHA != "1234567" {
		t.Fatalf("silent retry should reuse the original range, got %#v", retry)
	}
	var count int64
	if err := db.Model(&ReleaseDeployment{}).Count(&count).Error; err != nil {
		t.Fatalf("count deployments: %v", err)
	}
	if count != 1 {
		t.Fatalf("silent retry created %d deployment rows", count)
	}
}

func TestGeneratedReleaseDeploymentRetryUpdatesRequestedDelivery(t *testing.T) {
	setupAnnouncementTestDB(t)
	retryAt := time.Date(2026, 6, 1, 3, 0, 0, 0, time.UTC)
	deployment, announcement, err := RecordReleaseDeploymentWithAnnouncement(&ReleaseDeployment{
		Version:           "v1.2.3",
		CommitSHA:         "abcdef1",
		PreviousCommitSHA: "1234567",
		ReleaseNoteStatus: ReleaseNoteStatusSkipped,
	}, &AnnouncementUpsert{
		Kind:             AnnouncementKindReleaseNote,
		TitleI18N:        map[string]string{"zh": "发布说明"},
		SummaryI18N:      map[string]string{"zh": "摘要"},
		BodyI18N:         map[string]any{"zh": []string{"内容"}},
		DefaultLocale:    "zh",
		Source:           AnnouncementSourceDeploy,
		Delivery:         AnnouncementDeliveryNotice,
		DeliverySet:      true,
		SourceCommitFrom: "1234567",
		SourceCommitTo:   "abcdef1",
	})
	if err != nil || announcement == nil {
		t.Fatalf("record generated deployment: deployment=%#v announcement=%#v err=%v", deployment, announcement, err)
	}
	retry, updated, err := RecordReleaseDeploymentWithAnnouncement(&ReleaseDeployment{
		Version:           "v1.2.4",
		CommitSHA:         "abcdef1",
		PreviousCommitSHA: "1234567",
		DeployedAt:        retryAt,
		ReleaseNoteStatus: ReleaseNoteStatusSkipped,
	}, &AnnouncementUpsert{
		Delivery:    AnnouncementDeliveryCritical,
		DeliverySet: true,
	})
	if err != nil || updated == nil {
		t.Fatalf("retry generated deployment: deployment=%#v announcement=%#v err=%v", retry, updated, err)
	}
	if retry.ID != deployment.ID || updated.ID != announcement.ID || updated.Delivery != AnnouncementDeliveryCritical {
		t.Fatalf("retry should preserve the draft and update delivery, got deployment=%#v announcement=%#v", retry, updated)
	}
	if retry.Version != "v1.2.4" || !retry.DeployedAt.Equal(retryAt) {
		t.Fatalf("retry should refresh deployment metadata, got %#v", retry)
	}
}

func TestGeneratedReleaseDeploymentRetryRejectsPublishedAnnouncement(t *testing.T) {
	setupAnnouncementTestDB(t)
	publishedAt := time.Date(2026, 6, 1, 3, 0, 0, 0, time.UTC)
	deployment, announcement, err := RecordReleaseDeploymentWithAnnouncement(&ReleaseDeployment{
		Version:           "v1.2.3",
		CommitSHA:         "abcdef1",
		PreviousCommitSHA: "1234567",
		ReleaseNoteStatus: ReleaseNoteStatusSkipped,
	}, &AnnouncementUpsert{
		Kind:             AnnouncementKindReleaseNote,
		TitleI18N:        map[string]string{"zh": "发布说明"},
		SummaryI18N:      map[string]string{"zh": "摘要"},
		BodyI18N:         map[string]any{"zh": []string{"内容"}},
		DefaultLocale:    "zh",
		Source:           AnnouncementSourceDeploy,
		Delivery:         AnnouncementDeliveryNotice,
		DeliverySet:      true,
		SourceCommitFrom: "1234567",
		SourceCommitTo:   "abcdef1",
		Published:        true,
		PublishedAt:      &publishedAt,
	})
	if err != nil || announcement == nil {
		t.Fatalf("record published deployment: deployment=%#v announcement=%#v err=%v", deployment, announcement, err)
	}

	_, _, err = RecordReleaseDeploymentWithAnnouncement(&ReleaseDeployment{
		Version:           "v1.2.4",
		CommitSHA:         "abcdef1",
		PreviousCommitSHA: "1234567",
		ReleaseNoteStatus: ReleaseNoteStatusSkipped,
	}, &AnnouncementUpsert{Delivery: AnnouncementDeliveryCritical, DeliverySet: true})
	if err == nil || !strings.Contains(err.Error(), "already published") {
		t.Fatalf("published announcement delivery correction should be rejected, got %v", err)
	}

	var saved Announcement
	if err := coreDB.First(&saved, announcement.ID).Error; err != nil {
		t.Fatalf("reload announcement: %v", err)
	}
	if saved.Delivery != AnnouncementDeliveryNotice {
		t.Fatalf("published announcement delivery changed to %q", saved.Delivery)
	}
}

func TestLatestGeneratedReleaseDeploymentIgnoresSkippedAndFailed(t *testing.T) {
	setupAnnouncementTestDB(t)
	now := time.Date(2026, 5, 30, 3, 0, 0, 0, time.UTC)
	generatedAnnouncement := Announcement{
		Kind:          AnnouncementKindReleaseNote,
		TitleI18N:     marshalJSONValue(map[string]string{"zh": "发布说明"}, "{}"),
		SummaryI18N:   marshalJSONValue(map[string]string{"zh": "已生成"}, "{}"),
		BodyI18N:      marshalJSONValue(map[string]any{"zh": []string{"已生成"}}, "{}"),
		DefaultLocale: "zh",
		Source:        AnnouncementSourceDeploy,
		PublishedAt:   &now,
	}
	if err := coreDB.Create(&generatedAnnouncement).Error; err != nil {
		t.Fatalf("create generated announcement: %v", err)
	}
	announcementID := generatedAnnouncement.ID
	draftAnnouncement := Announcement{
		Kind:          AnnouncementKindReleaseNote,
		TitleI18N:     marshalJSONValue(map[string]string{"zh": "草稿发布说明"}, "{}"),
		SummaryI18N:   marshalJSONValue(map[string]string{"zh": "尚未发布"}, "{}"),
		BodyI18N:      marshalJSONValue(map[string]any{"zh": []string{"尚未发布"}}, "{}"),
		DefaultLocale: "zh",
		Source:        AnnouncementSourceDeploy,
	}
	if err := coreDB.Create(&draftAnnouncement).Error; err != nil {
		t.Fatalf("create generated draft: %v", err)
	}
	draftAnnouncementID := draftAnnouncement.ID
	rows := []ReleaseDeployment{
		{
			Version:                   "generated",
			CommitSHA:                 "sha-generated",
			PreviousCommitSHA:         "sha-before",
			DeployedAt:                now,
			ReleaseNoteStatus:         ReleaseNoteStatusGenerated,
			ReleaseNoteAnnouncementID: &announcementID,
		},
		{
			Version:                   "draft",
			CommitSHA:                 "sha-draft",
			PreviousCommitSHA:         "sha-generated",
			DeployedAt:                now.Add(30 * time.Minute),
			ReleaseNoteStatus:         ReleaseNoteStatusGenerated,
			ReleaseNoteAnnouncementID: &draftAnnouncementID,
		},
		{
			Version:           "failed",
			CommitSHA:         "sha-failed",
			PreviousCommitSHA: "sha-generated",
			DeployedAt:        now.Add(time.Hour),
			ReleaseNoteStatus: ReleaseNoteStatusFailed,
			ReleaseNoteError:  "model failed",
		},
		{
			Version:           "skipped",
			CommitSHA:         "sha-skipped",
			PreviousCommitSHA: "sha-failed",
			DeployedAt:        now.Add(2 * time.Hour),
			ReleaseNoteStatus: ReleaseNoteStatusSkipped,
			ReleaseNoteError:  "missing key",
		},
	}
	if err := coreDB.Create(&rows).Error; err != nil {
		t.Fatalf("create deployments: %v", err)
	}

	latest, err := LatestGeneratedReleaseDeployment()
	if err != nil {
		t.Fatalf("latest generated deployment: %v", err)
	}
	if latest == nil || latest.CommitSHA != "sha-generated" {
		t.Fatalf("expected latest generated deployment, got %#v", latest)
	}
}

func timePtr(value time.Time) *time.Time {
	return &value
}
