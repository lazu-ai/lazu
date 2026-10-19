package model

import (
	"testing"
	"time"
)

func TestModelsLastModified(t *testing.T) {
	db := withPricingTestDB(t, "models_last_modified")
	early := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	price := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	for _, row := range []*Model{
		{ModelName: "a", UpdatedTime: early.Unix()},
		{ModelName: "b", UpdatedTime: early.Unix()},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []*ModelSellPrice{
		{ModelName: "a", ChannelGroup: "direct", PricingVersion: 1, UpdatedAt: early},
		{ModelName: "a", ChannelGroup: "direct", PricingVersion: 2, Active: false, UpdatedAt: price},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	got, err := ModelsLastModified()
	if err != nil {
		t.Fatal(err)
	}
	if !got["a"].Equal(price) {
		t.Errorf("a = %v, want its latest price change %v", got["a"], price)
	}
	if !got["b"].Equal(early) {
		t.Errorf("b = %v, want its catalog edit %v", got["b"], early)
	}
}
