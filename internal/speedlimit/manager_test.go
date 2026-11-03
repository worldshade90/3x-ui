package speedlimit

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeRunner struct {
	cmds [][]string
	show string
}

func (f *fakeRunner) Run(_ context.Context, args ...string) (string, error) {
	f.cmds = append(f.cmds, append([]string(nil), args...))
	if len(args) >= 3 && args[1] == "filter" && args[2] == "show" {
		return f.show, nil
	}
	return "", nil
}

func TestSyncRefusesForeignClsact(t *testing.T) {
	fake := &fakeRunner{show: "filter protocol ip pref 1 u32 chain 0 handle 0x1\n"}
	m := NewManager(fake)
	m.linux = true
	m.pinIface = true
	m.iface = "eth0"
	if err := m.Sync(context.Background(), []Limit{{
		ID: 1, DownBps: 1, IPs: []string{"203.0.113.1"},
	}}); err != nil {
		t.Fatal(err)
	}
	got := joinCmds(fake.cmds)
	if strings.Contains(got, "flower") {
		t.Fatalf("installed filters on a foreign clsact:\n%s", got)
	}
	if !m.blocked {
		t.Fatal("expected the interface to stay blocked")
	}
}

type denyIFB struct {
	fakeRunner
}

func (d *denyIFB) Run(ctx context.Context, args ...string) (string, error) {
	if len(args) >= 3 && args[0] == "ip" && args[1] == "link" && args[2] == "add" {
		d.cmds = append(d.cmds, append([]string(nil), args...))
		return "", errors.New("RTNETLINK answers: Operation not permitted")
	}
	return d.fakeRunner.Run(ctx, args...)
}

func TestSyncSkipsUploadWhenIFBCannotBeCreated(t *testing.T) {
	fake := &denyIFB{}
	m := NewManager(fake)
	m.linux = true
	m.pinIface = true
	m.iface = "eth0"
	m.booted = true
	if err := m.Sync(context.Background(), []Limit{{
		ID: 3, DownBps: 102400, UpBps: 102400, IPs: []string{"203.0.113.9"},
	}}); err != nil {
		t.Fatal(err)
	}
	got := joinCmds(fake.cmds)
	if strings.Contains(got, "mirred") || strings.Contains(got, "redirect") {
		t.Fatalf("ingress redirect was installed without ifbxui:\n%s", got)
	}
	if !strings.Contains(got, "egress") {
		t.Fatalf("download policing was skipped with upload:\n%s", got)
	}
}

func TestSyncRemovesRedirectWhenIFBIsDown(t *testing.T) {
	show := "filter protocol ip pref 1 flower chain 0 handle 0x1\naction order 1: mirred (Egress Redirect to device *) stolen\n"
	fake := &fakeRunner{show: show}
	m := NewManager(fake)
	m.linux = true
	m.pinIface = true
	m.iface = "eth0"
	m.booted = true
	if err := m.Sync(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	got := joinCmds(fake.cmds)
	if strings.Contains(got, "action mirred") {
		t.Fatalf("a new redirect was installed while ifbxui is down:\n%s", got)
	}
	if !strings.Contains(got, "tc filter del dev eth0 ingress protocol ip pref 1 handle 0x1 flower") &&
		!strings.Contains(got, "tc qdisc del dev eth0 clsact") {
		t.Fatalf("dangling redirect was left installed:\n%s", got)
	}
}
