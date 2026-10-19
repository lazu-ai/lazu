package model

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/setting"
	"gorm.io/gorm"
)

func TestProvisionedLaneReferencesPreventDeletion(t *testing.T) {
	db := withPricingTestDB(t, t.Name())
	if err := db.AutoMigrate(&Channel{}); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"source", "price"} {
		t.Run(kind, func(t *testing.T) {
			db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&Channel{})
			db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&ModelSellPrice{})
			switch kind {
			case "source":
				db.Create(&Channel{Name: "provisioned", Group: "turbo"})
			case "price":
				db.Create(&ModelSellPrice{ModelName: "test", ChannelGroup: "turbo"})
			}
			referenced, err := ChannelLaneHasReferences("turbo")
			if err != nil || !referenced {
				t.Fatalf("missing %s reference: %v", kind, err)
			}
		})
	}
}

func TestDisabledLaneTokenPreferenceRemainsReadable(t *testing.T) {
	before := setting.ChannelLanes2JSONString()
	t.Cleanup(func() { _ = setting.UpdateChannelLanesByJSONString(before) })
	if _, err := setting.UpsertChannelLane(setting.ChannelLane{Slug: "turbo", Name: "Turbo"}); err != nil {
		t.Fatal(err)
	}
	if _, err := setting.SetChannelLaneEnabled("turbo", true); err != nil {
		t.Fatal(err)
	}
	token := TokenVendorGroupMap{"vendor-fixture": "turbo"}
	stored, err := token.Value()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := setting.SetChannelLaneEnabled("turbo", false); err != nil {
		t.Fatal(err)
	}
	var restored TokenVendorGroupMap
	if err := restored.Scan(stored); err != nil {
		t.Fatal(err)
	}
	if restored.Normalize()["vendor-fixture"] != "turbo" {
		t.Fatal("stored preference lost")
	}
	if restored.LaneForVendor("vendor-fixture") != ChannelLaneDirect {
		t.Fatal("disabled lane routed")
	}
}
