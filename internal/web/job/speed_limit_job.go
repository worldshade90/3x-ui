package job

import (
	"context"
	"runtime"
	"sync"

	"github.com/mhsanaei/3x-ui/v3/internal/amneziawgkern"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/speedlimit"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// onlineIPSource is the Xray online-stats call the speed shaper needs.
type onlineIPSource interface {
	GetOnlineUsers() ([]xray.OnlineUser, bool, error)
}

// SpeedLimitJob reapplies per-client tc rules from the clients table and the
// addresses currently observed for those clients.
type SpeedLimitJob struct {
	mu     sync.Mutex
	online onlineIPSource
	api    map[string][]string
	have   bool
}

func NewSpeedLimitJob() *SpeedLimitJob {
	return &SpeedLimitJob{online: &service.XrayService{}}
}

func (j *SpeedLimitJob) Run() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if runtime.GOOS != "linux" {
		return
	}
	if !speedlimit.Enabled() {
		_ = speedlimit.Sync(context.Background(), nil)
		return
	}
	observed, _ := j.snapshot()
	limits, err := speedlimit.LoadActive(observed)
	if err != nil {
		logger.Warning("speed limit: ", err)
		return
	}
	if err := speedlimit.Sync(context.Background(), limits); err != nil {
		logger.Warning("speed limit sync: ", err)
	}
}

// snapshot keeps the last good Xray address list when the API misses a poll
// and always overlays the kernel AmneziaWG peers collected this run.
func (j *SpeedLimitJob) snapshot() (map[string][]string, bool) {
	users, ok, err := j.online.GetOnlineUsers()
	freshAPI := map[string][]string{}
	if err == nil && ok {
		for _, user := range users {
			if user.Email == "" {
				continue
			}
			for _, ip := range user.IPs {
				if ip.IP != "" {
					freshAPI[user.Email] = append(freshAPI[user.Email], ip.IP)
				}
			}
		}
		j.api = freshAPI
		j.have = true
	}
	kernel := amneziawgkern.GetCollector().ClientIPs()
	if !j.have && len(kernel) == 0 {
		return nil, false
	}
	out := map[string][]string{}
	if j.have {
		for email, ips := range j.api {
			out[email] = append(out[email], ips...)
		}
	}
	for email, ips := range kernel {
		for ip := range ips {
			out[email] = append(out[email], ip)
		}
	}
	return out, true
}
