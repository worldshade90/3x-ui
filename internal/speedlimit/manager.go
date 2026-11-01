package speedlimit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
)

// Runner executes ip/tc. The combined output is returned even when the command fails.
type Runner interface {
	Run(ctx context.Context, args ...string) (string, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, args ...string) (string, error) {
	if len(args) == 0 {
		return "", errors.New("empty command")
	}
	bin, err := exec.LookPath(args[0])
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, bin, args[1:]...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err = cmd.Run()
	out := buf.String()
	if err != nil {
		return out, fmt.Errorf("%s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(out))
	}
	return out, nil
}

// Manager installs and removes per-client tc rules on one Linux host.
type Manager struct {
	mu       sync.Mutex
	runner   Runner
	state    *State
	iface    string
	pinIface bool
	linux    bool
	stopped  bool
	booted   bool
	blocked  bool
	warned   bool
}

func NewManager(r Runner) *Manager {
	if r == nil {
		r = execRunner{}
	}
	return &Manager{runner: r, linux: runtime.GOOS == "linux"}
}

var shared = NewManager(nil)

// Enabled reports whether enforcement is on. XUI_SPEED_LIMIT=0 skips tc entirely.
func Enabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("XUI_SPEED_LIMIT"))) {
	case "0", "false", "off", "no":
		return false
	default:
		return true
	}
}

func forceReplace() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("XUI_TC_FORCE"))) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

// Sync makes the host match desired. A nil or empty list removes rules this panel owns.
func Sync(ctx context.Context, desired []Limit) error {
	return shared.Sync(ctx, desired)
}

// Cleanup removes rules owned by this process. Safe to call when none were installed.
func Cleanup() {
	shared.Cleanup()
}

// Allow resumes enforcement after Cleanup during an in-process panel restart.
func Allow() {
	shared.Allow()
}

func (m *Manager) Allow() {
	m.mu.Lock()
	m.stopped = false
	m.mu.Unlock()
}

func (m *Manager) Sync(ctx context.Context, desired []Limit) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped || !m.linux {
		return nil
	}
	if !Enabled() {
		return m.disable(ctx)
	}
	iface, err := m.ensureIface(ctx)
	if err != nil {
		m.warn(err.Error())
		return nil
	}
	if m.state != nil && m.iface != "" && m.iface != iface {
		if err := m.apply(ctx, m.iface, nil); err != nil {
			m.warn(err.Error())
		}
		m.booted = false
		m.state = nil
	}
	m.iface = iface
	// A redirect aimed at a missing ifbxui drops every packet. Clear those
	// before any other decision, including the "not our filters" stop.
	m.rollbackBlackhole(ctx, iface)
	if !m.booted || m.blocked {
		m.blocked = m.bootSweep(ctx, iface)
		m.booted = true
		m.state = nil
		m.rollbackBlackhole(ctx, iface)
		if m.blocked {
			m.warn(iface + " has tc filters this panel does not own; set XUI_TC_FORCE=1 to replace them")
			return nil
		}
	}
	upload := false
	if wantsUpload(desired) {
		upload = m.ensureIFB(ctx)
		if !upload {
			logger.Warning("speed limit: ifbxui is not up; upload shaping skipped so ingress redirects cannot blackhole traffic. Load the ifb module before starting x-ui (ProtectKernelModules blocks modprobe).")
		}
	}
	if !upload {
		desired = dropUpload(desired)
	}
	err = m.apply(ctx, iface, desired)
	m.rollbackBlackhole(ctx, iface)
	return err
}

func (m *Manager) disable(ctx context.Context) error {
	if m.state != nil && m.iface != "" {
		if err := m.apply(ctx, m.iface, nil); err != nil {
			return err
		}
	}
	if !m.booted {
		iface, err := m.ensureIface(ctx)
		if err != nil {
			return nil
		}
		m.iface = iface
		_ = m.bootSweep(ctx, iface)
		m.booted = true
	}
	return nil
}

func (m *Manager) Cleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = true
	if !m.linux || (!m.booted && m.state == nil) {
		m.booted = false
		m.blocked = false
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	iface := m.iface
	if iface == "" {
		iface, _ = m.ensureIface(ctx)
	}
	if iface != "" {
		_ = m.apply(ctx, iface, nil)
		_ = m.bootSweep(ctx, iface)
	}
	m.state = nil
	m.booted = false
	m.blocked = false
}

func (m *Manager) apply(ctx context.Context, iface string, desired []Limit) error {
	if iface == "" {
		return nil
	}
	cmds, next := Diff(iface, m.state, desired)
	if err := m.runAll(ctx, cmds); err != nil {
		m.booted = false
		m.state = nil
		return err
	}
	m.state = next
	return nil
}

func (m *Manager) bootSweep(ctx context.Context, iface string) bool {
	egress, _ := m.runner.Run(ctx, "tc", "filter", "show", "dev", iface, "egress")
	ingress, _ := m.runner.Run(ctx, "tc", "filter", "show", "dev", iface, "ingress")
	cmds, blocked := PlanSweep(iface, egress, ingress, forceReplace())
	_ = m.runAll(ctx, cmds)
	return blocked
}

func (m *Manager) runAll(ctx context.Context, cmds [][]string) error {
	var failed error
	for _, c := range cmds {
		if len(c) == 0 {
			continue
		}
		if isIngressRedirect(c) && !m.ifbUp(ctx) {
			logger.Warning("speed limit: skipped ingress redirect because ifbxui is not up")
			if failed == nil {
				failed = errors.New("ifbxui is not up")
			}
			continue
		}
		_, err := m.runner.Run(ctx, c...)
		if err != nil && !benignCmd(c, err) {
			logger.Warningf("speed limit: %v", err)
			if failed == nil {
				failed = err
			}
		}
	}
	return failed
}

func (m *Manager) ensureIFB(ctx context.Context) bool {
	if m.ifbUp(ctx) {
		return true
	}
	if _, err := m.runner.Run(ctx, "ip", "link", "add", ifbName, "type", "ifb"); err != nil && !benign(err) {
		logger.Warningf("speed limit: cannot create %s: %v", ifbName, err)
		return false
	}
	if _, err := m.runner.Run(ctx, "ip", "link", "set", ifbName, "up"); err != nil && !benign(err) {
		logger.Warningf("speed limit: cannot set %s up: %v", ifbName, err)
		return false
	}
	return m.ifbUp(ctx)
}

func (m *Manager) ifbUp(ctx context.Context) bool {
	out, _ := m.runner.Run(ctx, "ip", "link", "show", ifbName)
	return linkIsUp(ifbName, out)
}

func (m *Manager) rollbackBlackhole(ctx context.Context, iface string) {
	if iface == "" || m.ifbUp(ctx) {
		return
	}
	ingress, _ := m.runner.Run(ctx, "tc", "filter", "show", "dev", iface, "ingress")
	cmds := danglingRedirectDeletes(iface, ingress)
	if len(cmds) == 0 {
		return
	}
	logger.Warning("speed limit: removing ingress redirects because ifbxui is not up")
	_ = m.runAll(ctx, cmds)
}

func wantsUpload(limits []Limit) bool {
	for _, lim := range limits {
		if lim.UpBps > 0 && len(lim.IPs) > 0 {
			return true
		}
	}
	return false
}

func dropUpload(limits []Limit) []Limit {
	out := make([]Limit, len(limits))
	copy(out, limits)
	for i := range out {
		out[i].UpBps = 0
	}
	return out
}

func isIngressRedirect(args []string) bool {
	joined := " " + strings.Join(args, " ") + " "
	return strings.Contains(joined, " mirred ") || strings.Contains(joined, " redirect ")
}

func (m *Manager) warn(msg string) {
	if m.warned {
		return
	}
	m.warned = true
	logger.Warning("speed limit: ", msg)
}

func (m *Manager) ensureIface(ctx context.Context) (string, error) {
	if m.pinIface {
		if !validIface(m.iface) {
			return "", fmt.Errorf("invalid interface %q", m.iface)
		}
		return m.iface, nil
	}
	if name := strings.TrimSpace(os.Getenv("XUI_TC_IFACE")); name != "" {
		if !validIface(name) {
			return "", fmt.Errorf("invalid XUI_TC_IFACE %q", name)
		}
		return name, nil
	}
	out, err4 := m.runner.Run(ctx, "ip", "route", "show", "default")
	if name := parseDev(out); name != "" {
		return name, nil
	}
	out, err6 := m.runner.Run(ctx, "ip", "-6", "route", "show", "default")
	if name := parseDev(out); name != "" {
		return name, nil
	}
	if err4 != nil {
		return "", err4
	}
	if err6 != nil {
		return "", err6
	}
	return "", errors.New("no default route; set XUI_TC_IFACE")
}

func benign(err error) bool {
	return benignCmd(nil, err)
}

func benignCmd(args []string, err error) bool {
	if err == nil {
		return true
	}
	s := err.Error()
	if clsactAdd(args) && (strings.Contains(s, "Exclusivity flag on") || strings.Contains(s, "File exists")) {
		return true
	}
	for _, frag := range []string{
		"File exists",
		"Cannot find device",
		"Cannot find device \"ifbxui\"",
		"No such file",
		"No such process",
		"Invalid handle",
		"doesn't exist",
		"does not exist",
		"not found",
	} {
		if strings.Contains(s, frag) {
			return true
		}
	}
	return false
}

func clsactAdd(args []string) bool {
	if len(args) < 5 || args[0] != "tc" || args[1] != "qdisc" || args[2] != "add" {
		return false
	}
	for _, arg := range args {
		if arg == "clsact" {
			return true
		}
	}
	return false
}

// LoadActive reads enabled clients that have a non-zero cap and attaches observed IPs.
func LoadActive(observed map[string][]string) ([]Limit, error) {
	db := database.GetDB()
	if db == nil {
		return nil, errors.New("database is not open")
	}
	var rows []model.ClientRecord
	err := db.Select("id", "email", "speed_down", "speed_up").
		Where("enable = ? AND (speed_down > 0 OR speed_up > 0)", true).
		Order("id").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]Limit, 0, len(rows))
	for _, row := range rows {
		down, up := row.SpeedDown, row.SpeedUp
		if down < 0 {
			down = 0
		}
		if up < 0 {
			up = 0
		}
		out = append(out, Limit{ID: row.Id, DownBps: down, UpBps: up, IPs: observed[row.Email]})
	}
	return out, nil
}
