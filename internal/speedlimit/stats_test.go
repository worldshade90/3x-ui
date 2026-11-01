package speedlimit

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func openStatsDB(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dir)
	dbtest.InitDB(t, filepath.Join(dir, "x-ui.db"))
}

func TestWriteSamplesKeepsMaxAverageAndBucket(t *testing.T) {
	openStatsDB(t)
	day := time.Date(2026, 11, 1, 0, 7, 0, 0, time.UTC)
	elapsed := 5 * time.Second
	// 5s * 2048 B/s = 10240 bytes. Second sample is three times that.
	if err := writeSamples(day, elapsed, []Sample{{Email: "a@x", DownBytes: 10240, UpBytes: 0}}, nil); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := writeSamples(day.Add(time.Minute), elapsed, []Sample{{Email: "a@x", DownBytes: 30720, UpBytes: 5120}}, nil); err != nil {
		t.Fatalf("second write: %v", err)
	}
	// Idle polls must not drag the average down.
	if err := writeSamples(day.Add(2*time.Minute), elapsed, []Sample{{Email: "a@x"}}, nil); err != nil {
		t.Fatalf("idle write: %v", err)
	}

	stats, err := ListDay("2026-11-01")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("rows = %d, want 1", len(stats))
	}
	got := stats[0]
	if got.Email != "a@x" || got.SamplesDown != 2 || got.SamplesUp != 1 {
		t.Fatalf("summary = %+v", got)
	}
	if got.MaxDownKBs != KBps(6144) || got.AvgDownKBs != avgKBs(2048+6144, 2) {
		t.Fatalf("down max=%v avg=%v", got.MaxDownKBs, got.AvgDownKBs)
	}
	if got.MaxUpKBs != KBps(1024) || got.AvgUpKBs != KBps(1024) {
		t.Fatalf("up max=%v avg=%v", got.MaxUpKBs, got.AvgUpKBs)
	}

	points, err := DaySeries("a@x", "2026-11-01")
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	if len(points) != buckets {
		t.Fatalf("points = %d", len(points))
	}
	// 00:07 and 00:08 share the 00:05 bucket, which keeps the higher rate.
	if points[1].Sec != 300 || points[1].DownKBs != KBps(6144) || points[1].UpKBs != KBps(1024) {
		t.Fatalf("bucket = %+v", points[1])
	}
	if points[0].DownKBs != 0 || points[2].DownKBs != 0 {
		t.Fatalf("neighbor buckets should stay empty: %+v %+v", points[0], points[2])
	}
}

func TestPruneDropsRowsOlderThanRetention(t *testing.T) {
	openStatsDB(t)
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	oldDay := now.AddDate(0, 0, -(RetentionDays + 1)).Format("2006-01-02")
	keepDay := now.AddDate(0, 0, -RetentionDays).Format("2006-01-02")
	db := database.GetDB()
	for _, day := range []string{oldDay, keepDay} {
		if err := db.Create(&model.ClientSpeedDay{Email: "a@x", Day: day, MaxDownBps: 1}).Error; err != nil {
			t.Fatalf("seed %s: %v", day, err)
		}
	}
	if err := pruneOld(db, now); err != nil {
		t.Fatalf("prune: %v", err)
	}
	var days []string
	if err := db.Model(&model.ClientSpeedDay{}).Order("day").Pluck("day", &days).Error; err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(days) != 1 || days[0] != keepDay {
		t.Fatalf("days = %v, want only %s", days, keepDay)
	}
}

func TestBytesPerSec(t *testing.T) {
	if got := BytesPerSec(10240, 5*time.Second); got != 2048 {
		t.Fatalf("got %d", got)
	}
	if got := BytesPerSec(0, time.Second); got != 0 {
		t.Fatalf("got %d", got)
	}
}

func TestObserveSkipsBaselineInterval(t *testing.T) {
	openStatsDB(t)
	start := time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC)
	obs.mu.Lock()
	obs.last = time.Time{}
	obs.pruned = time.Time{}
	obs.mu.Unlock()
	Observe(start, []Sample{{Email: "a@x", DownBytes: 99999}})
	stats, err := ListDay("2026-11-01")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(stats) != 0 {
		t.Fatalf("baseline interval was stored: %+v", stats)
	}
	Observe(start.Add(5*time.Second), []Sample{{Email: "a@x", DownBytes: 5120}})
	stats, err = ListDay("2026-11-01")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(stats) != 1 || stats[0].MaxDownKBs != KBps(1024) {
		t.Fatalf("got %+v", stats)
	}
}
