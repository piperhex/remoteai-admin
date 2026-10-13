package tokenpricing

import (
	"encoding/json"
	"testing"
)

func TestLongContextThresholdRoundTrip(t *testing.T) {
	for _, threshold := range []string{"null", "1", "128000", "272000", "1000000000"} {
		t.Run(threshold, func(t *testing.T) {
			document := Defaults()
			if err := json.Unmarshal([]byte(`{"longContextThresholdTokens":`+threshold+`}`), &document.Models[0]); err != nil {
				t.Fatal(err)
			}
			if err := document.Validate(); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			var restored Document
			if err := json.Unmarshal(encoded, &restored); err != nil {
				t.Fatal(err)
			}
			before, after := document.Models[0].LongContextThresholdTokens, restored.Models[0].LongContextThresholdTokens
			if (before == nil) != (after == nil) || (before != nil && *before != *after) {
				t.Fatal("threshold changed during JSON persistence")
			}
		})
	}
}

func TestInvalidLongContextThreshold(t *testing.T) {
	for _, threshold := range []string{"0", "-1", "1.5", "1000000001", `"272000"`, "true"} {
		t.Run(threshold, func(t *testing.T) {
			document := Defaults()
			err := json.Unmarshal([]byte(`{"longContextThresholdTokens":`+threshold+`}`), &document.Models[0])
			if err == nil && document.Validate() == nil {
				t.Fatal("accepted invalid threshold even when long-context pricing is disabled")
			}
		})
	}
}
