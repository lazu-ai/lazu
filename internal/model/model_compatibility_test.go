package model

import (
	"reflect"
	"testing"
	"time"
)

func TestListedModelNamesKeepsOldOnlyScopes(t *testing.T) {
	for _, tt := range []struct{ names, want []string }{
		{[]string{"deepseek-v4-flash", "deepseek-flash", "deepseek-v4-pro", "deepseek-v4-flash-vision-exp"}, []string{"deepseek-flash", "deepseek-v4-pro"}},
		{[]string{"deepseek-v4-flash"}, []string{"deepseek-v4-flash"}},
		{[]string{"deepseek-v4-flash-vision-exp", "unrelated"}, []string{"deepseek-v4-flash-vision-exp", "unrelated"}},
	} {
		if got := ListedModelNames(tt.names); !reflect.DeepEqual(got, tt.want) {
			t.Fatalf("got %v want %v", got, tt.want)
		}
	}
}
func TestHiddenAliasesKeepRoutingAndPrice(t *testing.T) {
	db := withPricingTestDB(t, "deepseek_alias_price")
	if err := db.AutoMigrate(&Model{}, &Channel{}, &Ability{}, &Vendor{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&Channel{Id: 54, Name: "test", Type: 1, Status: 1, Models: "deepseek-flash,deepseek-v4-flash,deepseek-v4-flash-vision-exp", Group: ChannelLaneDirect}).Error; err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"deepseek-flash", "deepseek-v4-flash", "deepseek-v4-flash-vision-exp"} {
		if err := db.Create(&Model{ModelName: name, Status: 1}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&Ability{Model: name, Group: ChannelLaneDirect, ChannelId: 54, Enabled: true}).Error; err != nil {
			t.Fatal(err)
		}
		input, output := 0.1875, 0.75
		if err := db.Create(&ModelSellPrice{ModelName: name, ChannelGroup: ChannelLaneDirect, Active: true, InputPerMtok: &input, OutputPerMtok: &output, PricingVersion: 1}).Error; err != nil {
			t.Fatal(err)
		}
	}
	RefreshPricing()
	t.Cleanup(func() { lastGetPricingTime = time.Time{} })
	for _, name := range []string{"deepseek-v4-flash", "deepseek-v4-flash-vision-exp"} {
		if !IsModelRequestable(name) {
			t.Fatalf("hidden alias %s is no longer requestable", name)
		}
		if _, ok := GetActiveModelSellPrice(name, ChannelLaneDirect); !ok {
			t.Fatalf("hidden alias %s lost pricing", name)
		}
	}
	if got := ListedPricing(GetPricing()); len(got) != 1 || got[0].ModelName != "deepseek-flash" {
		t.Fatalf("unexpected listing: %+v", got)
	}
}
