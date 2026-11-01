package speedlimit

import (
	"context"
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
		ID: 1, DownMbit: 1, IPs: []string{"203.0.113.1"},
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
