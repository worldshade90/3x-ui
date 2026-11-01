package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestClientSpeedRoundTripAndMerge(t *testing.T) {
	raw, err := json.Marshal(Client{Email: "a@x", SpeedDown: 10, SpeedUp: 0})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"speedDown":10`) || !strings.Contains(string(raw), `"speedUp":0`) {
		t.Fatalf("speed fields dropped from client JSON: %s", raw)
	}

	rec := (&Client{Email: "a@x", SpeedDown: -4, SpeedUp: 6, SpeedUnit: "mb"}).ToRecord()
	if rec.SpeedDown != 0 || rec.SpeedUp != 6 || rec.SpeedUnit != "mb" {
		t.Fatalf("ToRecord = %+v", rec)
	}
	if got := (&Client{}).ToRecord().SpeedUnit; got != "kb" {
		t.Fatalf("empty unit = %q", got)
	}
	back := rec.ToClient()
	if back.SpeedDown != 0 || back.SpeedUp != 6 {
		t.Fatalf("ToClient = %+v", back)
	}

	existing := &ClientRecord{SpeedDown: 10, SpeedUp: 4}
	MergeClientRecord(existing, &ClientRecord{SpeedDown: 0, SpeedUp: 2})
	if existing.SpeedDown != 10 || existing.SpeedUp != 4 {
		t.Fatalf("partial merge cleared a cap: %+v", existing)
	}
	MergeClientRecord(existing, &ClientRecord{SpeedDown: 25, SpeedUp: 1})
	if existing.SpeedDown != 25 || existing.SpeedUp != 4 {
		t.Fatalf("merge = %+v", existing)
	}
	existing.SpeedUnit = "kb"
	MergeClientRecord(existing, &ClientRecord{SpeedUnit: "", UpdatedAt: 9})
	if existing.SpeedUnit != "kb" {
		t.Fatalf("empty unit wiped the display unit: %q", existing.SpeedUnit)
	}
	MergeClientRecord(existing, &ClientRecord{SpeedUnit: "mb", UpdatedAt: 10})
	if existing.SpeedUnit != "mb" {
		t.Fatalf("unit = %q", existing.SpeedUnit)
	}
}
