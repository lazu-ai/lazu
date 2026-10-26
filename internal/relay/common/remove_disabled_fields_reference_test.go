package common

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/setting/model_setting"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// removeDisabledFieldsReference is RemoveDisabledFields before it learned to
// skip absent top-level keys; the current one must match it byte for byte.
func removeDisabledFieldsReference(jsonData []byte, channelOtherSettings dto.ChannelOtherSettings, channelPassThroughEnabled bool) ([]byte, error) {
	if model_setting.GetGlobalSettings().PassThroughRequestEnabled || channelPassThroughEnabled {
		return jsonData, nil
	}
	fields := []struct {
		path   string
		remove bool
	}{
		{"service_tier", !channelOtherSettings.AllowServiceTier},
		{"speed", !channelOtherSettings.AllowSpeed},
		{"inference_geo", !channelOtherSettings.AllowInferenceGeo},
		{"store", channelOtherSettings.DisableStore},
		{"safety_identifier", !channelOtherSettings.AllowSafetyIdentifier},
		{"stream_options.include_obfuscation", !channelOtherSettings.AllowIncludeObfuscation},
	}
	for _, field := range fields {
		if !field.remove || !gjson.GetBytes(jsonData, field.path).Exists() {
			continue
		}
		var err error
		jsonData, err = sjson.DeleteBytes(jsonData, field.path)
		if err != nil {
			return nil, err
		}
	}
	if options := gjson.GetBytes(jsonData, "stream_options"); options.IsObject() && len(options.Map()) == 0 {
		return sjson.DeleteBytes(jsonData, "stream_options")
	}
	return jsonData, nil
}

func TestRemoveDisabledFieldsMatchesReference(t *testing.T) {
	long := strings.Repeat("x", 50000)
	bodies := []string{
		`{"model":"m","messages":[]}`,
		`{"model":"m","service_tier":"priority","speed":"fast","inference_geo":"us","store":true,"safety_identifier":"u","stream_options":{"include_obfuscation":true}}`,
		`{"model":"m","stream_options":{"include_obfuscation":true,"include_usage":true}}`,
		`{"model":"m","stream_options":{}}`,
		`{"model":"m","stream_options":{"include_obfuscation":false}}`,
		`{ "store" : false , "content":"` + long + `", "service_tier":"auto" }`,
		`{"service_tier":"a","service_tier":"b","store":true}`,
		`{"nested":{"service_tier":"keep"},"text":"\"service_tier\":\"in a string\""}`,
		`[]`,
		`{"model":`,
		``,
	}
	settings := []dto.ChannelOtherSettings{
		{},
		{AllowServiceTier: true, AllowSpeed: true, AllowInferenceGeo: true, AllowSafetyIdentifier: true, AllowIncludeObfuscation: true},
		{DisableStore: true},
		{AllowServiceTier: true, DisableStore: true, AllowIncludeObfuscation: true},
	}
	for i, body := range bodies {
		for j, other := range settings {
			for _, passThrough := range []bool{false, true} {
				want, wantErr := removeDisabledFieldsReference([]byte(body), other, passThrough)
				got, gotErr := RemoveDisabledFields([]byte(body), other, passThrough)
				if (wantErr == nil) != (gotErr == nil) || !bytes.Equal(want, got) {
					t.Fatalf("%s: want %s (%v), got %s (%v)", fmt.Sprintf("body=%d settings=%d pass=%t", i, j, passThrough), want, wantErr, got, gotErr)
				}
			}
		}
	}
}
