package dto

import (
	"encoding/json"
	"testing"
)

func TestChannelAccountItemDoesNotExposeLegacyKeyState(t *testing.T) {
	payload, err := json.Marshal(ChannelAccountItem{})
	if err != nil {
		t.Fatalf("marshal channel account item: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatalf("unmarshal channel account item: %v", err)
	}
	expected := map[string]struct{}{
		"id":           {},
		"channel_type": {},
		"account_uid":  {},
		"display_name": {},
		"avatar_url":   {},
		"created_at":   {},
	}
	for key := range raw {
		if _, ok := expected[key]; !ok {
			t.Fatalf("unexpected channel account payload field: %s", key)
		}
		delete(expected, key)
	}
	if len(expected) != 0 {
		t.Fatalf("missing expected channel account payload fields: %#v", expected)
	}
}
