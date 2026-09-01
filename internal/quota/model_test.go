package quota

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestPercentRejectsNonFiniteValues(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if got := Percent(value); got != nil {
			t.Fatalf("Percent(%v) = %v, want nil", value, *got)
		}
	}
}

func TestAccountRetentionIdentityIsNeverSerialized(t *testing.T) {
	payload, err := json.Marshal(Account{RetentionID: "secret-auth-index-hash", Provider: "codex", Name: "account"})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(payload), "secret-auth-index-hash") || strings.Contains(string(payload), "RetentionID") {
		t.Fatalf("serialized account leaked retention identity: %s", payload)
	}
}
