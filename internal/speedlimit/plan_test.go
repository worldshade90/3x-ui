package speedlimit

import (
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
		ID: 7, DownMbit: 10, UpMbit: 5,
		IPs: []string{"203.0.113.5", "2001:db8::1", "203.0.113.5"},
	}})
	got := joinCmds(cmds)
	for _, want := range []string{
		"tc qdisc add dev eth0 clsact",
		"ip link add ifbxui type ifb",
		"tc qdisc add dev ifbxui root handle 7e12: htb default 1",
		"tc class add dev ifbxui parent 7e12: classid 7e12:1 htb rate 100000mbit ceil 100000mbit",
		"tc action replace police rate 10mbit burst 152kb conform-exceed drop index 2020933634",
		"tc class add dev ifbxui parent 7e12:1 classid 7e12:2 htb rate 5mbit ceil 5mbit",
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
		ID: 7, DownMbit: 10, UpMbit: 5, IPs: []string{"203.0.113.5", "2001:db8::1"},
	}})
	if len(again) != 0 {
		t.Fatalf("unchanged desired emitted commands: %s", joinCmds(again))
	}
}

func TestDiffRecyclesFilterHandleAndClearsUnlimited(t *testing.T) {
	_, st := Diff("eth0", nil, []Limit{{ID: 4, DownMbit: 8, UpMbit: 0, IPs: []string{"198.51.100.10"}}})
	cmds, st := Diff("eth0", st, []Limit{{ID: 4, DownMbit: 8, UpMbit: 0, IPs: []string{"198.51.100.11"}}})
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
	if !strings.Contains(got, "tc action del police index") || !strings.Contains(got, "tc qdisc del dev eth0 clsact") {
		t.Fatalf("clearing the limit left rules behind:\n%s", got)
	}
	if st.OwnsClsact || st.HasIFB || len(st.Slots) != 0 {
		t.Fatalf("state not empty after clear: %+v", st)
	}
}

func TestDiffDisabledClientDropsRules(t *testing.T) {
	_, st := Diff("eth0", nil, []Limit{{ID: 2, DownMbit: 1, UpMbit: 1, IPs: []string{"203.0.113.8"}}})
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
