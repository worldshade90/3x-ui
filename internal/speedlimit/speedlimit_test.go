package speedlimit

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMarkUsesClientIDInPanelRange(t *testing.T) {
	got, ok := Mark(3)
	if !ok || got != 0x3A000003 {
		t.Fatalf("Mark(3) = %#x, %v; want 0x3a000003", got, ok)
	}
	if _, ok := Mark(0); ok {
		t.Fatal("Mark(0) should be rejected")
	}
	if _, ok := Mark(maxClientID + 1); ok {
		t.Fatal("Mark past 24 bits should be rejected")
	}
}

func TestRenderRulesMetersEachDirectionOnce(t *testing.T) {
	script := RenderRules([]Client{
		{ID: 7, Email: "b@x", DownMbit: 10, UpMbit: 2},
		{ID: 3, Email: "a@x", UpMbit: 5},
		{ID: 9, Email: "c@x", DownMbit: 0, UpMbit: 0},
	})
	want := strings.Join([]string{
		"add table inet xui_speed",
		"flush table inet xui_speed",
		"add chain inet xui_speed output { type filter hook output priority mangle; policy accept; }",
		"add rule inet xui_speed output meta mark & 0xff000000 == 0x3a000000 ct mark set meta mark",
		"add rule inet xui_speed output meta mark 0x3a000003 limit rate over 625 kbytes/second burst 62 kbytes drop",
		"add rule inet xui_speed output meta mark 0x3a000007 limit rate over 250 kbytes/second burst 25 kbytes drop",
		"add chain inet xui_speed prerouting { type filter hook prerouting priority mangle; policy accept; }",
		"add rule inet xui_speed prerouting ct mark 0x3a000007 limit rate over 1250 kbytes/second burst 125 kbytes drop",
		"",
	}, "\n")
	if script != want {
		t.Fatalf("script =\n%s\nwant =\n%s", script, want)
	}
	if RenderRules(nil) != "" {
		t.Fatal("no clients should render an empty script")
	}
}

func TestInjectKeepsUnrelatedTrafficAndMarksLimitedClients(t *testing.T) {
	routing := []byte(`{
		"domainStrategy": "AsIs",
		"rules": [
			{"type": "field", "inboundTag": ["api"], "outboundTag": "api"},
			{"type": "field", "domain": ["geosite:ads"], "ip": ["10.0.0.0/8"], "outboundTag": "block"},
			{"type": "field", "user": ["other@x", "vip@x"], "ip": ["1.2.3.4"], "outboundTag": "direct"},
			{"type": "field", "network": "tcp", "balancerTag": "lb"},
			{"type": "field", "domain": ["geosite:netflix"], "outboundTag": "marked"}
		]
	}`)
	outbounds := []byte(`[
		{"protocol": "freedom", "tag": "direct", "sendThrough": "203.0.113.5", "streamSettings": {"sockopt": {"tcpFastOpen": true}}},
		{"protocol": "blackhole", "tag": "block"},
		{"protocol": "freedom", "tag": "marked", "streamSettings": {"sockopt": {"mark": 255}}},
		{"protocol": "dokodemo-door", "tag": "api"}
	]`)
	clients := []Client{
		{ID: 7, Email: "vip@x", DownMbit: 10, UpMbit: 2},
		{ID: 0, Email: "bad@x", DownMbit: 1},
		{ID: 8, Email: "plain@x"},
		{ID: 3, Email: "up@x", UpMbit: 5},
	}
	gotRouting, gotOut, err := Inject(routing, outbounds, clients)
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}

	var route map[string]any
	if err := json.Unmarshal(gotRouting, &route); err != nil {
		t.Fatalf("routing: %v", err)
	}
	if route["domainStrategy"] != "AsIs" {
		t.Fatalf("domainStrategy = %v", route["domainStrategy"])
	}
	rules, _ := route["rules"].([]any)
	// up@x misses the vip-only rule (4 copies + catch-all). vip@x matches all 5 + catch-all. Then 5 originals.
	if len(rules) != 16 {
		t.Fatalf("rule count = %d, want 16", len(rules))
	}
	first, _ := rules[0].(map[string]any)
	if outbound := first["outboundTag"]; outbound != "api" {
		t.Fatalf("first copy outbound = %v, want api (dokodemo is not cloned)", outbound)
	}
	users, _ := first["user"].([]any)
	if len(users) != 1 || users[0] != "up@x" {
		t.Fatalf("first copy user = %v", users)
	}
	// up@x's ads copy keeps the ip matcher and the blackhole tag.
	ads, _ := rules[1].(map[string]any)
	if ads["outboundTag"] != "block" {
		t.Fatalf("ads outbound = %v", ads["outboundTag"])
	}
	ips, _ := ads["ip"].([]any)
	if len(ips) != 1 || ips[0] != "10.0.0.0/8" {
		t.Fatalf("ads ip = %v", ips)
	}
	// up@x catch-all is the 5th prepended rule (index 4).
	catch, _ := rules[4].(map[string]any)
	if catch["outboundTag"] != "xui-sl-3-direct" {
		t.Fatalf("up@x catch-all = %v", catch["outboundTag"])
	}
	// vip matches the shared-user rule and must be cloned onto its own direct outbound.
	vipDirect, _ := rules[7].(map[string]any)
	if vipDirect["outboundTag"] != "xui-sl-7-direct" {
		t.Fatalf("vip direct copy = %v", vipDirect["outboundTag"])
	}
	vipNetflix, _ := rules[9].(map[string]any)
	if vipNetflix["outboundTag"] != "marked" {
		t.Fatalf("pre-marked outbound was cloned: %v", vipNetflix["outboundTag"])
	}
	originalAPI, _ := rules[11].(map[string]any)
	if _, hasUser := originalAPI["user"]; hasUser || originalAPI["outboundTag"] != "api" {
		t.Fatalf("original api rule changed: %#v", originalAPI)
	}

	var obs []map[string]any
	if err := json.Unmarshal(gotOut, &obs); err != nil {
		t.Fatalf("outbounds: %v", err)
	}
	if len(obs) != 6 {
		t.Fatalf("outbound count = %d, want 6", len(obs))
	}
	clone := obs[4]
	if clone["tag"] != "xui-sl-3-direct" || clone["sendThrough"] != "203.0.113.5" || clone["protocol"] != "freedom" {
		t.Fatalf("clone identity = %#v", clone)
	}
	ss := clone["streamSettings"].(map[string]any)
	so := ss["sockopt"].(map[string]any)
	if so["tcpFastOpen"] != true {
		t.Fatalf("tcpFastOpen lost: %#v", so)
	}
	if so["mark"] != float64(0x3A000003) {
		t.Fatalf("mark = %v, want %d", so["mark"], 0x3A000003)
	}
	if obs[5]["tag"] != "xui-sl-7-direct" {
		t.Fatalf("second clone = %v", obs[5]["tag"])
	}
	ss7 := obs[5]["streamSettings"].(map[string]any)
	so7 := ss7["sockopt"].(map[string]any)
	if so7["mark"] != float64(0x3A000007) {
		t.Fatalf("vip mark = %v", so7["mark"])
	}

	againR, againO, err := Inject(routing, outbounds, clients)
	if err != nil {
		t.Fatalf("second Inject: %v", err)
	}
	if Fingerprint(gotRouting, gotOut) == "" || Fingerprint(gotRouting, gotOut) != Fingerprint(againR, againO) {
		t.Fatalf("fingerprint not stable: %q vs %q", Fingerprint(gotRouting, gotOut), Fingerprint(againR, againO))
	}
	if Fingerprint(routing, outbounds) != "" {
		t.Fatal("original config should have an empty speed-limit fingerprint")
	}
}

func TestInjectWithoutClientsIsIdentity(t *testing.T) {
	routing := []byte(`{"rules":[{"type":"field","outboundTag":"direct"}]}`)
	outbounds := []byte(`[{"protocol":"freedom","tag":"direct"}]`)
	gotR, gotO, err := Inject(routing, outbounds, []Client{{ID: 1, Email: "a@x"}})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if string(gotR) != string(routing) || string(gotO) != string(outbounds) {
		t.Fatalf("unlimited client changed config\nrouting %s\nout %s", gotR, gotO)
	}
}
