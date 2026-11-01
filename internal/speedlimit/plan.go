package speedlimit

import (
	"fmt"
	"sort"
	"strconv"
)

const (
	ifbName    = "ifbxui"
	ifbRoot    = "7e12"
	policeBase = 0x78750000
	// Parent class is far above any per-client cap so it is not the bottleneck.
	parentRate = "100000mbit"
)

// Limit is one client cap in bytes/s (0 = unlimited). Download is server egress;
// upload is redirected onto ifbxui so every address shares one HTB class.
type Limit struct {
	ID      int
	DownBps int64
	UpBps   int64
	IPs     []string
}

type ipRules struct {
	V6             bool
	DownHandle     uint32
	RedirectHandle uint32
	IFBHandle      uint32
}

type slot struct {
	Minor   int
	DownBps int64
	UpBps   int64
	IPs     map[string]ipRules
}

type handlePool struct {
	next uint32
	free []uint32
}

type minorPool struct {
	next int
	free []int
}

// State is the tc configuration the panel believes it installed.
type State struct {
	OwnsClsact bool
	HasIFB     bool
	Slots      map[int]*slot
	minors     minorPool
	handles    map[string]*handlePool
}

func newState() *State {
	return &State{
		Slots: map[int]*slot{},
		handles: map[string]*handlePool{
			"down4": {}, "down6": {},
			"redir4": {}, "redir6": {},
			"ifb4": {}, "ifb6": {},
		},
	}
}

func (p *handlePool) get() uint32 {
	if n := len(p.free); n > 0 {
		h := p.free[n-1]
		p.free = p.free[:n-1]
		return h
	}
	p.next++
	return p.next
}

func (p *handlePool) put(h uint32) {
	if h == 0 {
		return
	}
	p.free = append(p.free, h)
}

func (p *minorPool) get() int {
	if n := len(p.free); n > 0 {
		m := p.free[n-1]
		p.free = p.free[:n-1]
		return m
	}
	if p.next < 1 {
		p.next = 1
	}
	p.next++
	if p.next > 65534 {
		return 0
	}
	return p.next
}

func (p *minorPool) put(m int) {
	if m >= 2 {
		p.free = append(p.free, m)
	}
}

func (s *State) pool(key string) *handlePool {
	p := s.handles[key]
	if p == nil {
		p = &handlePool{}
		s.handles[key] = p
	}
	return p
}

func cloneState(prev *State) *State {
	if prev == nil {
		return newState()
	}
	next := newState()
	next.OwnsClsact = prev.OwnsClsact
	next.HasIFB = prev.HasIFB
	next.minors.next = prev.minors.next
	next.minors.free = append([]int(nil), prev.minors.free...)
	for key, p := range prev.handles {
		if p == nil {
			continue
		}
		next.handles[key] = &handlePool{next: p.next, free: append([]uint32(nil), p.free...)}
	}
	for id, sl := range prev.Slots {
		cp := &slot{Minor: sl.Minor, DownBps: sl.DownBps, UpBps: sl.UpBps, IPs: make(map[string]ipRules, len(sl.IPs))}
		for ip, rules := range sl.IPs {
			cp.IPs[ip] = rules
		}
		next.Slots[id] = cp
	}
	return next
}

// Diff returns the tc/ip commands that move prev to desired, and the resulting state.
// It never replaces the host root qdisc: download uses a clsact police, upload uses ifbxui.
func Diff(iface string, prev *State, desired []Limit) ([][]string, *State) {
	next := cloneState(prev)
	want := map[int]Limit{}
	for _, lim := range desired {
		if lim.ID <= 0 {
			continue
		}
		if lim.DownBps < 0 {
			lim.DownBps = 0
		}
		if lim.UpBps < 0 {
			lim.UpBps = 0
		}
		if lim.DownBps == 0 && lim.UpBps == 0 {
			continue
		}
		lim.IPs = NormalizeIPs(lim.IPs)
		if len(lim.IPs) == 0 {
			continue
		}
		want[lim.ID] = lim
	}

	var filterDels, classDels, classAdds, filterAdds [][]string
	ids := make([]int, 0, len(next.Slots))
	for id := range next.Slots {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		sl := next.Slots[id]
		lim, ok := want[id]
		if !ok {
			filterDels = append(filterDels, deleteSlotFilters(iface, next, sl)...)
			classDels = append(classDels, deleteSlotClasses(sl)...)
			next.minors.put(sl.Minor)
			delete(next.Slots, id)
			continue
		}
		fd, cd, ca, fa := updateSlot(iface, next, sl, lim)
		filterDels = append(filterDels, fd...)
		classDels = append(classDels, cd...)
		classAdds = append(classAdds, ca...)
		filterAdds = append(filterAdds, fa...)
		delete(want, id)
	}

	newIDs := make([]int, 0, len(want))
	for id := range want {
		newIDs = append(newIDs, id)
	}
	sort.Ints(newIDs)
	for _, id := range newIDs {
		ca, fa, sl, ok := next.addSlot(iface, want[id])
		if !ok {
			continue
		}
		next.Slots[id] = sl
		classAdds = append(classAdds, ca...)
		filterAdds = append(filterAdds, fa...)
	}

	needIFB := false
	for _, sl := range next.Slots {
		if sl.UpBps > 0 {
			needIFB = true
			break
		}
	}
	needClsact := len(next.Slots) > 0

	var pre, post [][]string
	if needClsact && !next.OwnsClsact {
		pre = append(pre, []string{"tc", "qdisc", "add", "dev", iface, "clsact"})
		next.OwnsClsact = true
	}
	if needIFB && !next.HasIFB {
		pre = append(pre,
			[]string{"ip", "link", "add", ifbName, "type", "ifb"},
			[]string{"ip", "link", "set", ifbName, "up"},
			[]string{"tc", "qdisc", "add", "dev", ifbName, "root", "handle", ifbRoot + ":", "htb", "default", "1"},
			[]string{"tc", "class", "add", "dev", ifbName, "parent", ifbRoot + ":", "classid", ifbRoot + ":1", "htb", "rate", parentRate, "ceil", parentRate},
		)
		next.HasIFB = true
	}
	if !needIFB && next.HasIFB {
		post = append(post,
			[]string{"tc", "qdisc", "del", "dev", ifbName, "root"},
			[]string{"ip", "link", "del", ifbName},
		)
		next.HasIFB = false
	}
	if !needClsact && next.OwnsClsact {
		post = append(post, []string{"tc", "qdisc", "del", "dev", iface, "clsact"})
		next.OwnsClsact = false
	}

	cmds := make([][]string, 0, len(pre)+len(filterDels)+len(classDels)+len(classAdds)+len(filterAdds)+len(post))
	cmds = append(cmds, pre...)
	cmds = append(cmds, filterDels...)
	cmds = append(cmds, classDels...)
	cmds = append(cmds, classAdds...)
	cmds = append(cmds, filterAdds...)
	cmds = append(cmds, post...)
	return cmds, next
}

func (s *State) addSlot(iface string, lim Limit) (classAdds, filterAdds [][]string, sl *slot, ok bool) {
	minor := s.minors.get()
	if minor == 0 {
		return nil, nil, nil, false
	}
	sl = &slot{Minor: minor, DownBps: lim.DownBps, UpBps: lim.UpBps, IPs: map[string]ipRules{}}
	if lim.DownBps > 0 {
		classAdds = append(classAdds, policeCmd(sl))
	}
	if lim.UpBps > 0 {
		classAdds = append(classAdds, classAdd(sl))
	}
	for _, ip := range lim.IPs {
		filterAdds = append(filterAdds, s.bindIP(iface, sl, ip)...)
	}
	return classAdds, filterAdds, sl, true
}

func updateSlot(iface string, st *State, sl *slot, lim Limit) (filterDels, classDels, classAdds, filterAdds [][]string) {
	if sl.DownBps != lim.DownBps {
		if lim.DownBps == 0 {
			for _, ip := range sortedIPs(sl.IPs) {
				rules := sl.IPs[ip]
				if rules.DownHandle != 0 {
					filterDels = append(filterDels, delFilter(iface, "egress", rules.V6, rules.DownHandle))
					st.pool(poolKey("down", rules.V6)).put(rules.DownHandle)
					rules.DownHandle = 0
					sl.IPs[ip] = rules
				}
			}
			classDels = append(classDels, delPolice(sl))
			sl.DownBps = 0
		} else if sl.DownBps == 0 {
			sl.DownBps = lim.DownBps
			classAdds = append(classAdds, policeCmd(sl))
		} else {
			sl.DownBps = lim.DownBps
			classAdds = append(classAdds, policeCmd(sl))
		}
	}
	if sl.UpBps != lim.UpBps {
		if lim.UpBps == 0 {
			for _, ip := range sortedIPs(sl.IPs) {
				filterDels = append(filterDels, delUpload(iface, st, sl, ip)...)
			}
			classDels = append(classDels, delClass(sl))
			sl.UpBps = 0
		} else if sl.UpBps == 0 {
			sl.UpBps = lim.UpBps
			classAdds = append(classAdds, classAdd(sl))
		} else {
			sl.UpBps = lim.UpBps
			classAdds = append(classAdds, classChange(sl))
		}
	}

	want := make(map[string]struct{}, len(lim.IPs))
	for _, ip := range lim.IPs {
		want[ip] = struct{}{}
	}
	for _, ip := range sortedIPs(sl.IPs) {
		if _, ok := want[ip]; ok {
			continue
		}
		filterDels = append(filterDels, delIP(iface, st, sl, ip)...)
		delete(sl.IPs, ip)
	}
	for _, ip := range lim.IPs {
		rules, ok := sl.IPs[ip]
		if !ok {
			filterAdds = append(filterAdds, st.bindIP(iface, sl, ip)...)
			continue
		}
		if sl.DownBps > 0 && rules.DownHandle == 0 {
			rules.DownHandle = st.pool(poolKey("down", rules.V6)).get()
			filterAdds = append(filterAdds, addEgress(iface, sl, ip, rules))
			sl.IPs[ip] = rules
		}
		if sl.UpBps > 0 && rules.RedirectHandle == 0 {
			v6 := rules.V6
			rules.RedirectHandle = st.pool(poolKey("redir", v6)).get()
			rules.IFBHandle = st.pool(poolKey("ifb", v6)).get()
			filterAdds = append(filterAdds, addRedirect(iface, ip, rules), addIFB(sl, ip, rules))
			sl.IPs[ip] = rules
		}
	}
	return filterDels, classDels, classAdds, filterAdds
}

func (s *State) bindIP(iface string, sl *slot, ip string) [][]string {
	v6 := isV6(ip)
	rules := ipRules{V6: v6}
	var cmds [][]string
	if sl.DownBps > 0 {
		rules.DownHandle = s.pool(poolKey("down", v6)).get()
		cmds = append(cmds, addEgress(iface, sl, ip, rules))
	}
	if sl.UpBps > 0 {
		rules.RedirectHandle = s.pool(poolKey("redir", v6)).get()
		rules.IFBHandle = s.pool(poolKey("ifb", v6)).get()
		cmds = append(cmds, addRedirect(iface, ip, rules), addIFB(sl, ip, rules))
	}
	sl.IPs[ip] = rules
	return cmds
}

func deleteSlotFilters(iface string, st *State, sl *slot) [][]string {
	var cmds [][]string
	for _, ip := range sortedIPs(sl.IPs) {
		cmds = append(cmds, delIP(iface, st, sl, ip)...)
	}
	return cmds
}

func deleteSlotClasses(sl *slot) [][]string {
	var cmds [][]string
	if sl.UpBps > 0 {
		cmds = append(cmds, delClass(sl))
	}
	if sl.DownBps > 0 {
		cmds = append(cmds, delPolice(sl))
	}
	return cmds
}

func delIP(iface string, st *State, sl *slot, ip string) [][]string {
	rules := sl.IPs[ip]
	var cmds [][]string
	if rules.DownHandle != 0 {
		cmds = append(cmds, delFilter(iface, "egress", rules.V6, rules.DownHandle))
		st.pool(poolKey("down", rules.V6)).put(rules.DownHandle)
	}
	cmds = append(cmds, delUpload(iface, st, sl, ip)...)
	return cmds
}

func delUpload(iface string, st *State, sl *slot, ip string) [][]string {
	rules := sl.IPs[ip]
	var cmds [][]string
	if rules.RedirectHandle != 0 {
		cmds = append(cmds, delFilter(iface, "ingress", rules.V6, rules.RedirectHandle))
		st.pool(poolKey("redir", rules.V6)).put(rules.RedirectHandle)
		rules.RedirectHandle = 0
	}
	if rules.IFBHandle != 0 {
		cmds = append(cmds, delFilter(ifbName, "parent", rules.V6, rules.IFBHandle))
		st.pool(poolKey("ifb", rules.V6)).put(rules.IFBHandle)
		rules.IFBHandle = 0
	}
	sl.IPs[ip] = rules
	return cmds
}

func sortedIPs(ips map[string]ipRules) []string {
	out := make([]string, 0, len(ips))
	for ip := range ips {
		out = append(out, ip)
	}
	sort.Strings(out)
	return out
}

func poolKey(kind string, v6 bool) string {
	if v6 {
		return kind + "6"
	}
	return kind + "4"
}

func proto(v6 bool) string {
	if v6 {
		return "ipv6"
	}
	return "ip"
}

func pref(v6 bool) string {
	if v6 {
		return "2"
	}
	return "1"
}

func handleArg(h uint32) string {
	return "0x" + strconv.FormatUint(uint64(h), 16)
}

func classID(minor int) string {
	return fmt.Sprintf("%s:%x", ifbRoot, minor)
}

func ipMask(ip string, v6 bool) string {
	if v6 {
		return ip + "/128"
	}
	return ip + "/32"
}

func rateArg(bps int64) string {
	if bps < 0 {
		bps = 0
	}
	return strconv.FormatInt(bps, 10) + "Bps"
}

func burstArg(bps int64) string {
	if bps < 1 {
		bps = 1
	}
	kb := bps / 8 / 1024
	if kb < 16 {
		kb = 16
	}
	return strconv.FormatInt(kb, 10) + "kb"
}

func policeIndex(minor int) string {
	return strconv.Itoa(policeBase + minor)
}

func policeCmd(sl *slot) []string {
	return []string{"tc", "action", "replace", "police", "rate", rateArg(sl.DownBps), "burst", burstArg(sl.DownBps), "conform-exceed", "drop", "index", policeIndex(sl.Minor)}
}

func delPolice(sl *slot) []string {
	return []string{"tc", "action", "del", "police", "index", policeIndex(sl.Minor)}
}

func classAdd(sl *slot) []string {
	return []string{"tc", "class", "add", "dev", ifbName, "parent", ifbRoot + ":1", "classid", classID(sl.Minor), "htb", "rate", rateArg(sl.UpBps), "ceil", rateArg(sl.UpBps)}
}

func classChange(sl *slot) []string {
	cmd := classAdd(sl)
	cmd[2] = "change"
	return cmd
}

func delClass(sl *slot) []string {
	return []string{"tc", "class", "del", "dev", ifbName, "classid", classID(sl.Minor)}
}

func addEgress(iface string, sl *slot, ip string, rules ipRules) []string {
	return []string{"tc", "filter", "add", "dev", iface, "egress", "protocol", proto(rules.V6), "pref", pref(rules.V6), "handle", handleArg(rules.DownHandle), "flower", "dst_ip", ipMask(ip, rules.V6), "action", "police", "index", policeIndex(sl.Minor)}
}

func addRedirect(iface, ip string, rules ipRules) []string {
	return []string{"tc", "filter", "add", "dev", iface, "ingress", "protocol", proto(rules.V6), "pref", pref(rules.V6), "handle", handleArg(rules.RedirectHandle), "flower", "src_ip", ipMask(ip, rules.V6), "action", "mirred", "egress", "redirect", "dev", ifbName}
}

func addIFB(sl *slot, ip string, rules ipRules) []string {
	return []string{"tc", "filter", "add", "dev", ifbName, "parent", ifbRoot + ":", "protocol", proto(rules.V6), "prio", pref(rules.V6), "handle", handleArg(rules.IFBHandle), "flower", "src_ip", ipMask(ip, rules.V6), "classid", classID(sl.Minor)}
}

func delFilter(dev, where string, v6 bool, handle uint32) []string {
	if where == "parent" {
		return []string{"tc", "filter", "del", "dev", dev, "parent", ifbRoot + ":", "protocol", proto(v6), "prio", pref(v6), "handle", handleArg(handle), "flower"}
	}
	return []string{"tc", "filter", "del", "dev", dev, where, "protocol", proto(v6), "pref", pref(v6), "handle", handleArg(handle), "flower"}
}
