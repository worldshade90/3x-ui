package database

import (
	"path/filepath"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestMigrateClientSpeedLimitColumnsPreservesRates(t *testing.T) {
	if err := InitDB(filepath.Join(t.TempDir(), "x-ui.db")); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = CloseDB() })

	kept := model.ClientRecord{Email: "kept@x", Enable: true, TotalGB: 5, SpeedDown: 12, SpeedUp: 8}
	fresh := model.ClientRecord{Email: "fresh@x", Enable: true, TotalGB: 9}
	if err := GetDB().Create(&kept).Error; err != nil {
		t.Fatalf("seed kept: %v", err)
	}
	if err := GetDB().Create(&fresh).Error; err != nil {
		t.Fatalf("seed fresh: %v", err)
	}
	if err := GetDB().Exec("UPDATE clients SET speed_down = NULL, speed_up = NULL WHERE email = ?", "fresh@x").Error; err != nil {
		t.Fatalf("null out fresh caps: %v", err)
	}
	if err := migrateClientSpeedLimitColumns(); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	var gotKept, gotFresh model.ClientRecord
	if err := GetDB().Where("email = ?", "kept@x").First(&gotKept).Error; err != nil {
		t.Fatal(err)
	}
	if gotKept.TotalGB != 5 || gotKept.SpeedDown != 12 || gotKept.SpeedUp != 8 {
		t.Fatalf("configured client changed: gb %d down %d up %d", gotKept.TotalGB, gotKept.SpeedDown, gotKept.SpeedUp)
	}
	if err := GetDB().Where("email = ?", "fresh@x").First(&gotFresh).Error; err != nil {
		t.Fatal(err)
	}
	if gotFresh.TotalGB != 9 || gotFresh.SpeedDown != 0 || gotFresh.SpeedUp != 0 {
		t.Fatalf("null cap did not become unlimited without touching quota: gb %d down %d up %d", gotFresh.TotalGB, gotFresh.SpeedDown, gotFresh.SpeedUp)
	}
	if err := migrateClientSpeedLimitColumns(); err != nil {
		t.Fatalf("second backfill: %v", err)
	}
}
