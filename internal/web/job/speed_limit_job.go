package job

import (
	"context"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/speedlimit"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// SpeedLimitJob reinstalls per-client nftables meters and reloads Xray when
// the fwmark routing no longer matches the stored caps.
type SpeedLimitJob struct {
	xrayService service.XrayService
	lastErr     string
}

// NewSpeedLimitJob schedules the host meter and the Xray routing refresh.
func NewSpeedLimitJob() *SpeedLimitJob {
	return &SpeedLimitJob{}
}

func (j *SpeedLimitJob) Run() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := speedlimit.Reconcile(ctx); err != nil {
		msg := err.Error()
		if msg != j.lastErr {
			logger.Warning("speed limit:", err)
			j.lastErr = msg
		}
	} else {
		j.lastErr = ""
	}
	if j.xrayService.SpeedLimitDrift() {
		if err := j.xrayService.RestartXray(false); err != nil {
			logger.Warning("speed limit: xray refresh:", err)
		}
	}
}
