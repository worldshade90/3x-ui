package speedlimit

import (
	"path/filepath"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestLoadActiveSkipsDisabledAndUnlimited(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dir)
	dbtest.InitDB(t, filepath.Join(dir, "x-ui.db"))
	db := database.GetDB()
	rows := []model.ClientRecord{
		{Email: "shaped@x", Enable: true, SpeedDown: 10, SpeedUp: 2},
		{Email: "off@x", Enable: false, SpeedDown: 50, SpeedUp: 50},
		{Email: "free@x", Enable: true},
		{Email: "up-only@x", Enable: true, SpeedUp: 4},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	// default:true would otherwise store an explicit false as enabled.
	if err := db.Model(&model.ClientRecord{}).Where("email = ?", "off@x").UpdateColumn("enable", false).Error; err != nil {
		t.Fatalf("disable: %v", err)
	}
	got, err := LoadActive(map[string][]string{
		"shaped@x":  {"203.0.113.5", "203.0.113.6"},
		"off@x":     {"198.51.100.1"},
		"up-only@x": {"2001:db8::8"},
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %#v", got)
	}
	if got[0].DownMbit != 10 || got[0].UpMbit != 2 || len(got[0].IPs) != 2 || got[0].IPs[0] != "203.0.113.5" {
		t.Fatalf("shaped = %#v", got[0])
	}
	if got[1].DownMbit != 0 || got[1].UpMbit != 4 || len(got[1].IPs) != 1 {
		t.Fatalf("up-only = %#v", got[1])
	}
}
