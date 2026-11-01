package model

import "testing"

func TestMergeClientRecordSpeedKeepsStricterRate(t *testing.T) {
	existing := &ClientRecord{SpeedDown: 20, SpeedUp: 5}
	MergeClientRecord(existing, &ClientRecord{SpeedDown: 8, SpeedUp: 0})
	if existing.SpeedDown != 8 {
		t.Fatalf("down = %d, want the stricter 8", existing.SpeedDown)
	}
	if existing.SpeedUp != 5 {
		t.Fatalf("zero incoming upload cleared the cap, got %d", existing.SpeedUp)
	}
	MergeClientRecord(existing, &ClientRecord{SpeedDown: 50, SpeedUp: 3})
	if existing.SpeedDown != 8 || existing.SpeedUp != 3 {
		t.Fatalf("after looser down and stricter up: down %d up %d", existing.SpeedDown, existing.SpeedUp)
	}
}
