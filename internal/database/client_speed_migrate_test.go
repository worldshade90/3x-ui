package database

import (
	"path/filepath"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestMigrateClientSpeedPreservesExistingRow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x-ui.db")
	raw, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := raw.Exec(`CREATE TABLE clients (
		id INTEGER PRIMARY KEY,
		email TEXT NOT NULL,
		total_gb INTEGER,
		comment TEXT
	)`).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := raw.Exec(`INSERT INTO clients (email, total_gb, comment) VALUES (?, ?, ?)`, "keep@x", 42, "kept").Error; err != nil {
		t.Fatalf("insert: %v", err)
	}
	sqlDB, err := raw.DB()
	if err != nil {
		t.Fatalf("sql: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	t.Setenv("XUI_DB_FOLDER", dir)
	if err := InitDB(path); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = CloseDB() })

	var rec model.ClientRecord
	if err := GetDB().Where("email = ?", "keep@x").Take(&rec).Error; err != nil {
		t.Fatalf("read: %v", err)
	}
	if rec.Email != "keep@x" || rec.TotalGB != 42 || rec.Comment != "kept" || rec.SpeedDown != 0 || rec.SpeedUp != 0 {
		t.Fatalf("row changed: %+v", rec)
	}

	if err := GetDB().Exec("UPDATE clients SET speed_down = NULL, speed_up = NULL WHERE email = ?", "keep@x").Error; err != nil {
		t.Fatalf("null speeds: %v", err)
	}
	if err := migrateClientSpeedColumns(); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	var speeds struct {
		SpeedDown int
		SpeedUp   int
		Comment   string
		TotalGB   int64
	}
	if err := GetDB().Model(&model.ClientRecord{}).
		Select("speed_down", "speed_up", "comment", "total_gb").
		Where("email = ?", "keep@x").
		Take(&speeds).Error; err != nil {
		t.Fatalf("reread: %v", err)
	}
	if speeds.SpeedDown != 0 || speeds.SpeedUp != 0 || speeds.Comment != "kept" || speeds.TotalGB != 42 {
		t.Fatalf("backfill changed the row: %+v", speeds)
	}
}

func TestMigrateSpeedMbitToBytesOnce(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dir)
	path := filepath.Join(dir, "x-ui.db")
	if err := InitDB(path); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = CloseDB() })

	if err := GetDB().Where("seeder_name = ?", "SpeedMbitToBytes").Delete(&model.HistoryOfSeeders{}).Error; err != nil {
		t.Fatalf("clear seeder: %v", err)
	}
	rec := &model.ClientRecord{Email: "mbit@x", SpeedDown: 10, SpeedUp: 8, Comment: "kept", TotalGB: 3}
	if err := GetDB().Create(rec).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := migrateClientSpeedMbitToBytes(); err != nil {
		t.Fatalf("convert: %v", err)
	}
	var got model.ClientRecord
	if err := GetDB().Where("email = ?", "mbit@x").Take(&got).Error; err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.SpeedDown != 10*model.SpeedBytesPerMbit || got.SpeedUp != 8*model.SpeedBytesPerMbit || got.Comment != "kept" || got.TotalGB != 3 {
		t.Fatalf("converted row = %+v", got)
	}
	if err := migrateClientSpeedMbitToBytes(); err != nil {
		t.Fatalf("second convert: %v", err)
	}
	var again model.ClientRecord
	if err := GetDB().Where("email = ?", "mbit@x").Take(&again).Error; err != nil {
		t.Fatalf("reread: %v", err)
	}
	if again.SpeedDown != got.SpeedDown || again.SpeedUp != got.SpeedUp {
		t.Fatalf("seeder ran twice: %+v", again)
	}
}

func TestMigrateSpeedUnitByteLabelsToMbit(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dir)
	path := filepath.Join(dir, "x-ui.db")
	if err := InitDB(path); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = CloseDB() })

	rows := []struct {
		email string
		unit  *string
		down  int64
		up    int64
	}{
		{email: "kb@x", unit: strPtr("kb"), down: 1_250_000, up: 1_048_576},
		{email: "mb@x", unit: strPtr("mb"), down: 125, up: 0},
		{email: "empty@x", unit: strPtr(""), down: 8, up: 9},
		{email: "nil@x", unit: nil, down: 3, up: 4},
		{email: "kbit@x", unit: strPtr("kbit"), down: 250, up: 500},
	}
	for _, row := range rows {
		rec := &model.ClientRecord{Email: row.email, SpeedDown: row.down, SpeedUp: row.up}
		if err := GetDB().Create(rec).Error; err != nil {
			t.Fatalf("seed %s: %v", row.email, err)
		}
		var err error
		if row.unit == nil {
			err = GetDB().Exec("UPDATE clients SET speed_unit = NULL WHERE email = ?", row.email).Error
		} else {
			err = GetDB().Exec("UPDATE clients SET speed_unit = ? WHERE email = ?", *row.unit, row.email).Error
		}
		if err != nil {
			t.Fatalf("unit %s: %v", row.email, err)
		}
	}
	if err := migrateClientSpeedColumns(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	want := map[string]model.ClientRecord{
		"kb@x":    {SpeedUnit: "mbit", SpeedDown: 1_250_000, SpeedUp: 1_048_576},
		"mb@x":    {SpeedUnit: "mbit", SpeedDown: 125, SpeedUp: 0},
		"empty@x": {SpeedUnit: "mbit", SpeedDown: 8, SpeedUp: 9},
		"nil@x":   {SpeedUnit: "mbit", SpeedDown: 3, SpeedUp: 4},
		"kbit@x":  {SpeedUnit: "kbit", SpeedDown: 250, SpeedUp: 500},
	}
	for email, exp := range want {
		var got model.ClientRecord
		if err := GetDB().Where("email = ?", email).Take(&got).Error; err != nil {
			t.Fatalf("read %s: %v", email, err)
		}
		if got.SpeedUnit != exp.SpeedUnit || got.SpeedDown != exp.SpeedDown || got.SpeedUp != exp.SpeedUp {
			t.Fatalf("%s = unit %q down %d up %d", email, got.SpeedUnit, got.SpeedDown, got.SpeedUp)
		}
	}
}

func strPtr(v string) *string { return &v }
