package speedlimit

import (
	"encoding/binary"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"

	"gorm.io/gorm"
)

const (
	// RetentionDays is how long daily speed rows are kept.
	RetentionDays = 60
	buckets       = 288
	bucketSec     = 300
	seriesSize    = buckets * 8
)

// Sample is one client's byte counters since the previous poll.
type Sample struct {
	Email     string
	DownBytes int64
	UpBytes   int64
}

// DayStat is the per-client summary for one UTC day. Speeds are KB/s.
// Averages count only polls that carried traffic in that direction.
type DayStat struct {
	Email       string  `json:"email"`
	MaxDownKBs  float64 `json:"maxDownKBs"`
	MaxUpKBs    float64 `json:"maxUpKBs"`
	AvgDownKBs  float64 `json:"avgDownKBs"`
	AvgUpKBs    float64 `json:"avgUpKBs"`
	SamplesDown int     `json:"samplesDown"`
	SamplesUp   int     `json:"samplesUp"`
}

// SeriesPoint is one 5-minute bucket. Sec is seconds from 00:00 UTC.
type SeriesPoint struct {
	Sec     int     `json:"sec"`
	DownKBs float64 `json:"downKBs"`
	UpKBs   float64 `json:"upKBs"`
}

type observer struct {
	mu     sync.Mutex
	last   time.Time
	pruned time.Time
}

var obs observer

// Observe turns Xray byte deltas into KB/s samples. The first call only stores
// a timestamp, matching GetTraffic's baseline pass which reports no bytes.
func Observe(now time.Time, rows []Sample) {
	obs.mu.Lock()
	defer obs.mu.Unlock()
	if obs.last.IsZero() {
		obs.last = now
		return
	}
	elapsed := now.Sub(obs.last)
	obs.last = now
	if elapsed < 500*time.Millisecond || elapsed > 2*time.Minute {
		return
	}
	if err := writeSamples(now, elapsed, rows, &obs.pruned); err != nil {
		logger.Warning("speed stats: ", err)
	}
}

// BytesPerSec converts a byte delta over elapsed into bytes/second.
func BytesPerSec(bytes int64, elapsed time.Duration) int64 {
	ms := elapsed.Milliseconds()
	if bytes <= 0 || ms <= 0 {
		return 0
	}
	return bytes * 1000 / ms
}

// KBps converts bytes/second to KB/s rounded to 2 decimals (1024-based).
func KBps(bps int64) float64 {
	if bps <= 0 {
		return 0
	}
	return math.Round(float64(bps)/1024*100) / 100
}

func avgKBs(sum int64, n int) float64 {
	if n <= 0 || sum <= 0 {
		return 0
	}
	return math.Round((float64(sum)/float64(n))/1024*100) / 100
}

// ValidDay reports whether day is YYYY-MM-DD.
func ValidDay(day string) bool {
	if len(day) != 10 {
		return false
	}
	_, err := time.Parse("2006-01-02", day)
	return err == nil
}

func TodayUTC(now time.Time) string {
	return now.UTC().Format("2006-01-02")
}

func writeSamples(now time.Time, elapsed time.Duration, samples []Sample, pruned *time.Time) error {
	db := database.GetDB()
	if db == nil {
		return errors.New("database is not open")
	}
	if pruned == nil || pruned.IsZero() || now.Sub(*pruned) >= time.Hour {
		if err := pruneOld(db, now); err != nil {
			return err
		}
		if pruned != nil {
			*pruned = now
		}
	}
	active := samples[:0:0]
	for _, s := range samples {
		if s.Email == "" || (s.DownBytes <= 0 && s.UpBytes <= 0) {
			continue
		}
		active = append(active, s)
	}
	if len(active) == 0 {
		return nil
	}
	day := TodayUTC(now)
	idx := bucketIndex(now)
	return db.Transaction(func(tx *gorm.DB) error {
		for _, s := range active {
			var row model.ClientSpeedDay
			err := tx.Where("email = ? AND day = ?", s.Email, day).Take(&row).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				row = model.ClientSpeedDay{Email: s.Email, Day: day}
			} else if err != nil {
				return err
			}
			applySample(&row, idx, BytesPerSec(s.DownBytes, elapsed), BytesPerSec(s.UpBytes, elapsed))
			if row.Id == 0 {
				if err := tx.Create(&row).Error; err != nil {
					return err
				}
			} else if err := tx.Save(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func applySample(row *model.ClientSpeedDay, idx int, downBps, upBps int64) {
	if downBps > row.MaxDownBps {
		row.MaxDownBps = downBps
	}
	if upBps > row.MaxUpBps {
		row.MaxUpBps = upBps
	}
	if downBps > 0 {
		row.SumDownBps += downBps
		row.SamplesDown++
	}
	if upBps > 0 {
		row.SumUpBps += upBps
		row.SamplesUp++
	}
	row.Series = putMax(row.Series, idx, satU32(downBps), satU32(upBps))
}

func bucketIndex(t time.Time) int {
	t = t.UTC()
	sec := t.Hour()*3600 + t.Minute()*60 + t.Second()
	idx := sec / bucketSec
	if idx > buckets-1 {
		return buckets - 1
	}
	return idx
}

func putMax(series []byte, idx int, down, up uint32) []byte {
	if len(series) != seriesSize {
		next := make([]byte, seriesSize)
		copy(next, series)
		series = next
	}
	off := idx * 8
	if cur := binary.LittleEndian.Uint32(series[off : off+4]); down > cur {
		binary.LittleEndian.PutUint32(series[off:off+4], down)
	}
	if cur := binary.LittleEndian.Uint32(series[off+4 : off+8]); up > cur {
		binary.LittleEndian.PutUint32(series[off+4:off+8], up)
	}
	return series
}

func satU32(v int64) uint32 {
	if v <= 0 {
		return 0
	}
	if v > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(v)
}

func pruneOld(db *gorm.DB, now time.Time) error {
	cutoff := now.UTC().AddDate(0, 0, -RetentionDays).Format("2006-01-02")
	return db.Where("day < ?", cutoff).Delete(&model.ClientSpeedDay{}).Error
}

// ListDay returns every client that has a row on day, ordered by email.
func ListDay(day string) ([]DayStat, error) {
	db := database.GetDB()
	if db == nil {
		return nil, errors.New("database is not open")
	}
	var rows []model.ClientSpeedDay
	if err := db.Where("day = ?", day).Order("email").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]DayStat, 0, len(rows))
	for _, row := range rows {
		out = append(out, DayStat{
			Email:       row.Email,
			MaxDownKBs:  KBps(row.MaxDownBps),
			MaxUpKBs:    KBps(row.MaxUpBps),
			AvgDownKBs:  avgKBs(row.SumDownBps, row.SamplesDown),
			AvgUpKBs:    avgKBs(row.SumUpBps, row.SamplesUp),
			SamplesDown: row.SamplesDown,
			SamplesUp:   row.SamplesUp,
		})
	}
	return out, nil
}

// DaySeries returns the 288 five-minute buckets for one client, including zeros.
func DaySeries(email, day string) ([]SeriesPoint, error) {
	db := database.GetDB()
	if db == nil {
		return nil, errors.New("database is not open")
	}
	var row model.ClientSpeedDay
	err := db.Where("email = ? AND day = ?", email, day).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return emptySeries(), nil
	}
	if err != nil {
		return nil, err
	}
	return decodeSeries(row.Series), nil
}

func emptySeries() []SeriesPoint {
	out := make([]SeriesPoint, buckets)
	for i := range out {
		out[i].Sec = i * bucketSec
	}
	return out
}

func decodeSeries(series []byte) []SeriesPoint {
	out := emptySeries()
	if len(series) < seriesSize {
		return out
	}
	for i := 0; i < buckets; i++ {
		off := i * 8
		out[i].DownKBs = KBps(int64(binary.LittleEndian.Uint32(series[off : off+4])))
		out[i].UpKBs = KBps(int64(binary.LittleEndian.Uint32(series[off+4 : off+8])))
	}
	return out
}
