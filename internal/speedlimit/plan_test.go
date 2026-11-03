package speedlimit

import (
	"errors"
	"strings"
	"testing"
)

func joinCmds(cmds [][]string) string {
	parts := make([]string, 0, len(cmds))
	for _, c := range cmds {
		parts = append(parts, strings.Join(c, " "))
	}
	return strings.Join(parts, "\n")
}

func TestDiffShapesBothFamiliesWithoutHostRootQdisc(t *testing.T) {
	cmds, next := Diff("eth0", nil, []Limit{{
		ID: 7, DownBps: 1_250_000, UpBps: 625_000,
		IPs: []string{"203.0.113.5", "2001:db8::1", "203.0.113.5"},
	}})
	got := joinCmds(cmds)
	for _, want := range []string{
		"tc qdisc add dev eth0 clsact",
		"ip link add ifbxui type ifb",
		"tc qdisc add dev ifbxui root handle 7e12: htb default 1",
		"tc class add dev ifbxui parent 7e12: classid 7e12:1 htb rate 100000mbit ceil 100000mbit",
		"tc action replace police rate 1250000Bps burst 152kb conform-exceed drop index 2020933634",
		"tc class add dev ifbxui parent 7e12:1 classid 7e12:2 htb rate 625000Bps ceil 625000Bps",
		"tc filter add dev eth0 egress protocol ipv6 pref 2 handle 0x1 flower dst_ip 2001:db8::1/128 action police index 2020933634",
		"tc filter add dev eth0 ingress protocol ipv6 pref 2 handle 0x1 flower src_ip 2001:db8::1/128 action mirred egress redirect dev ifbxui",
		"tc filter add dev ifbxui parent 7e12: protocol ipv6 prio 2 handle 0x1 flower src_ip 2001:db8::1/128 classid 7e12:2",
		"tc filter add dev eth0 egress protocol ip pref 1 handle 0x1 flower dst_ip 203.0.113.5/32 action police index 2020933634",
		"tc filter add dev eth0 ingress protocol ip pref 1 handle 0x1 flower src_ip 203.0.113.5/32 action mirred egress redirect dev ifbxui",
		"tc filter add dev ifbxui parent 7e12: protocol ip prio 1 handle 0x1 flower src_ip 203.0.113.5/32 classid 7e12:2",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, " u32") || strings.Contains(got, "800::") {
		t.Fatalf("u32 filters are not used:\n%s", got)
	}
	if strings.Contains(got, "dev eth0 root") || strings.Contains(got, "dev eth0 handle") {
		t.Fatalf("host root qdisc was replaced:\n%s", got)
	}
	if strings.Count(got, "index 2020933634") != 3 {
		t.Fatalf("both addresses must share one police index, got:\n%s", got)
	}
	if strings.Count(got, "classid 7e12:2") != 3 {
		t.Fatalf("both addresses must share one HTB class, got:\n%s", got)
	}

	again, _ := Diff("eth0", next, []Limit{{
		ID: 7, DownBps: 1_250_000, UpBps: 625_000, IPs: []string{"203.0.113.5", "2001:db8::1"},
	}})
	if len(again) != 0 {
		t.Fatalf("unchanged desired emitted commands: %s", joinCmds(again))
	}
}

func TestDiffRecyclesFilterHandleAndClearsUnlimited(t *testing.T) {
	_, st := Diff("eth0", nil, []Limit{{ID: 4, DownBps: 8, UpBps: 0, IPs: []string{"198.51.100.10"}}})
	cmds, st := Diff("eth0", st, []Limit{{ID: 4, DownBps: 8, UpBps: 0, IPs: []string{"198.51.100.11"}}})
	got := joinCmds(cmds)
	if !strings.Contains(got, "filter del dev eth0 egress protocol ip pref 1 handle 0x1 flower") {
		t.Fatalf("old address was not removed:\n%s", got)
	}
	if !strings.Contains(got, "handle 0x1 flower dst_ip 198.51.100.11/32") {
		t.Fatalf("freed handle was not reused:\n%s", got)
	}
	if strings.Contains(got, "ifbxui") {
		t.Fatalf("upload is unlimited, ifb should stay unused:\n%s", got)
	}

	cleared, st := Diff("eth0", st, nil)
	got = joinCmds(cleared)
	if !strings.Contains(got, "tc actions del action police index") || !strings.Contains(got, "tc qdisc del dev eth0 clsact") {
		t.Fatalf("clearing the limit left rules behind:\n%s", got)
	}
	if st.OwnsClsact || st.HasIFB || len(st.Slots) != 0 {
		t.Fatalf("state not empty after clear: %+v", st)
	}
}

func TestDiffDisabledClientDropsRules(t *testing.T) {
	_, st := Diff("eth0", nil, []Limit{{ID: 2, DownBps: 1, UpBps: 1, IPs: []string{"203.0.113.8"}}})
	cmds, _ := Diff("eth0", st, nil)
	got := joinCmds(cmds)
	if !strings.Contains(got, "tc qdisc del dev ifbxui root") || !strings.Contains(got, "ip link del ifbxui") {
		t.Fatalf("upload device was not removed:\n%s", got)
	}
}

func TestPlanSweepRefusesForeignFilters(t *testing.T) {
	foreign := "filter protocol ip pref 1 u32 chain 0 handle 0x1\n  match ip dst 1.2.3.4\n"
	cmds, blocked := PlanSweep("eth0", foreign, "", false)
	if !blocked {
		t.Fatal("foreign u32 filter was treated as owned")
	}
	if strings.Contains(joinCmds(cmds), "qdisc del") {
		t.Fatalf("sweep deleted a foreign clsact: %s", joinCmds(cmds))
	}

	cmds, blocked = PlanSweep("eth0", foreign, "", true)
	if blocked || !strings.Contains(joinCmds(cmds), "tc qdisc del dev eth0 clsact") {
		t.Fatalf("force should replace clsact, blocked=%v cmds=%s", blocked, joinCmds(cmds))
	}

	own := "filter protocol ip pref 1 flower chain 0 handle 0x1\naction order 1: police 0x78750002 rate 10Mbit\n"
	mirred := "filter protocol ip pref 1 flower chain 0 handle 0x2\naction order 1: mirred (Egress Redirect to device ifbxui) stolen\n"
	cmds, blocked = PlanSweep("eth0", own, mirred, false)
	if blocked || !strings.Contains(joinCmds(cmds), "tc qdisc del dev eth0 clsact") {
		t.Fatalf("owned filters should be removable, blocked=%v cmds=%s", blocked, joinCmds(cmds))
	}

	cmds, blocked = PlanSweep("eth0", "", "", false)
	if blocked || strings.Contains(joinCmds(cmds), "qdisc del") {
		t.Fatalf("empty clsact should only drop ifbxui, blocked=%v cmds=%s", blocked, joinCmds(cmds))
	}
}

func TestPlanSweepKeepsDanglingRedirectUntilRemoved(t *testing.T) {
	egress := "filter protocol ip pref 1 flower chain 0 handle 0x2\naction order 1: police 0x78750002 rate 819200bit\n"
	ingress := "filter protocol ip pref 1 flower chain 0 handle 0x1\naction order 1: mirred (Egress Redirect to device *) stolen\n"
	cmds, blocked := PlanSweep("eth0", egress, ingress, false)
	if blocked {
		t.Fatal("panel filters, including a redirect to a missing device, were treated as foreign")
	}
	got := joinCmds(cmds)
	delAt := strings.Index(got, "tc filter del dev eth0 ingress")
	linkAt := strings.Index(got, "ip link del ifbxui")
	if delAt < 0 {
		t.Fatalf("dangling redirect was not deleted:\n%s", got)
	}
	if linkAt >= 0 && linkAt < delAt {
		t.Fatalf("ifbxui was deleted while the redirect still existed:\n%s", got)
	}
	if !strings.Contains(got, "handle 0x1") {
		t.Fatalf("delete did not name the filter handle:\n%s", got)
	}
}

func TestPlanSweepStillBlocksForeignFilters(t *testing.T) {
	foreign := "filter protocol ip pref 1 u32 chain 0 handle 0x1\n  match ip dst 1.2.3.4\n"
	dangling := "filter protocol ip pref 1 flower chain 0 handle 0x3\naction order 1: mirred (Egress Redirect to device *) stolen\n"
	cmds, blocked := PlanSweep("eth0", foreign, dangling, false)
	if !blocked {
		t.Fatal("a foreign u32 filter was ignored")
	}
	got := joinCmds(cmds)
	if strings.Contains(got, "qdisc del") || strings.Contains(got, "ip link del") {
		t.Fatalf("sweep tore down the qdisc or ifb while a foreign filter remains:\n%s", got)
	}
	if !strings.Contains(got, "tc filter del dev eth0 ingress") {
		t.Fatalf("dangling redirect was left in place:\n%s", got)
	}
}

func TestLinkIsUp(t *testing.T) {
	up := "3: ifbxui: <BROADCAST,NOARP,UP,LOWER_UP> mtu 1500 qdisc htb state UNKNOWN"
	if !linkIsUp("ifbxui", up) {
		t.Fatal("UP flag not recognized")
	}
	down := "3: ifbxui: <BROADCAST,NOARP,LOWER_UP> mtu 1500"
	if linkIsUp("ifbxui", down) {
		t.Fatal("LOWER_UP without UP was treated as up")
	}
	if linkIsUp("ifbxui", "Device \"ifbxui\" does not exist.") {
		t.Fatal("missing device was treated as up")
	}
}

func TestBenignClsactExclusivity(t *testing.T) {
	err := errors.New("tc qdisc add dev eth0 clsact: exit status 2 (Error: Exclusivity flag on, cannot modify.)")
	cmd := []string{"tc", "qdisc", "add", "dev", "eth0", "clsact"}
	if !benignCmd(cmd, err) {
		t.Fatal("existing clsact was treated as a failure")
	}
	if benignCmd([]string{"tc", "filter", "add", "dev", "eth0"}, err) {
		t.Fatal("exclusivity was ignored for a filter add")
	}
}

func TestNormalizeIPs(t *testing.T) {
	got := NormalizeIPs([]string{
		" 203.0.113.5 ",
		"203.0.113.5",
		"::ffff:203.0.113.9",
		"127.0.0.1",
		"::1",
		"0.0.0.0",
		"not-an-ip",
		"2001:db8::5%eth0",
	})
	want := []string{"2001:db8::5", "203.0.113.5", "203.0.113.9"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestParseDev(t *testing.T) {
	if got := parseDev("default via 192.0.2.1 dev eth0 proto dhcp src 192.0.2.10 metric 100"); got != "eth0" {
		t.Fatalf("got %q", got)
	}
	if got := parseDev("default via fe80::1 dev ens3 metric 1024"); got != "ens3" {
		t.Fatalf("got %q", got)
	}
	if got := parseDev(""); got != "" {
		t.Fatalf("got %q", got)
	}
}
