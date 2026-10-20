package game

import (
	"encoding/json"
	"testing"
)

func TestV2CommandEnvelope(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		valid bool
	}{
		{`{"v":2,"id":"x","type":"pause","data":{}}`, true},
		{`{"v":2,"id":"x","type":"pause","data":{"unknown":1}}`, false},
		{`{"v":2,"id":"x","type":"pause","text":"mixed","data":{}}`, false},
		{`{"v":2,"id":"x","type":"pause","data":null}`, false},
		{`{"v":2,"id":"x","type":"buzz","data":{"stage_id":"s","instance_id":"i"}}`, true},
		{`{"v":2,"id":"x","type":"buzz","data":{"stage_id":"s"}}`, false},
		{`{"v":2,"id":"x","type":"judge","data":{"stage_id":"s","instance_id":"i","target_id":"p"}}`, false},
	} {
		var c Command
		if err := json.Unmarshal([]byte(tc.raw), &c); err != nil {
			t.Fatal(err)
		}
		if err := c.Validate(); (err == nil) != tc.valid {
			t.Errorf("%s: %v", tc.raw, err)
		}
	}
}
