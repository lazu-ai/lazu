package operation_setting

import "testing"

func TestChannelTestFrequencyOverrideMatchesTheGetter(t *testing.T) {
	cases := []struct {
		value   string
		managed bool
	}{
		{"", false},
		{"0", false},
		{"-5", false},
		{"abc", false},
		{" 10", false}, // Atoi does not trim, so the getter ignores this too
		{"10", true},
	}
	for _, tc := range cases {
		t.Setenv("CHANNEL_TEST_FREQUENCY", tc.value)
		if _, managed := ChannelTestFrequencyOverride(); managed != tc.managed {
			t.Fatalf("CHANNEL_TEST_FREQUENCY=%q: expected managed=%v", tc.value, tc.managed)
		}
	}
}
