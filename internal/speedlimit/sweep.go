package speedlimit

import (
	"strconv"
	"strings"
)

// ClsactOwned reports whether filter-show output is empty or only ours.
// "device *" is the mirred target tc prints after ifbxui disappears.
func ClsactOwned(output string) (empty, owned bool) {
	blocks := filterBlocks(output)
	if len(blocks) == 0 {
		return true, true
	}
	for _, block := range blocks {
		if !blockOwned(block) {
			return false, false
		}
	}
	return false, true
}

func blockOwned(block string) bool {
	lower := strings.ToLower(block)
	if strings.Contains(block, ifbName) || strings.Contains(lower, "0x7875") {
		return true
	}
	if strings.Contains(lower, "mirred") && strings.Contains(block, "device *") {
		return true
	}
	return containsOwnedPoliceIndex(block)
}

func containsOwnedPoliceIndex(block string) bool {
	fields := strings.Fields(block)
	for i, field := range fields {
		if field == "index" && i+1 < len(fields) {
			n, err := strconv.ParseUint(strings.TrimRight(fields[i+1], ":"), 10, 64)
			if err == nil && n >= policeBase && n <= policeBase+0xFFFF {
				return true
			}
		}
	}
	return false
}

func filterBlocks(output string) []string {
	var blocks []string
	var b strings.Builder
	for _, line := range strings.Split(output, "\n") {
		trim := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trim, "filter ") {
			if b.Len() > 0 {
				blocks = append(blocks, b.String())
				b.Reset()
			}
		}
		if b.Len() > 0 || strings.HasPrefix(trim, "filter ") {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	if b.Len() > 0 {
		blocks = append(blocks, b.String())
	}
	return blocks
}

// PlanSweep removes dangling redirects before ifbxui. Deleting the device
// first blackholes those packets. A foreign filter blocks clsact removal.
func PlanSweep(iface, egress, ingress string, force bool) (cmds [][]string, blocked bool) {
	cmds = append(cmds, danglingRedirectDeletes(iface, ingress)...)
	emptyE, ownedE := ClsactOwned(egress)
	emptyI, ownedI := ClsactOwned(ingress)
	removeClsact := false
	if emptyE && emptyI {
		blocked = false
	} else if (!ownedE || !ownedI) && !force {
		blocked = true
	} else {
		cmds = append(cmds, []string{"tc", "qdisc", "del", "dev", iface, "clsact"})
		removeClsact = true
	}
	// The device may go only after redirects are gone or clsact deletion
	// takes them with it. Never pull ifbxui out from under a live mirred.
	if !strings.Contains(ingress, "mirred") || removeClsact {
		cmds = append(cmds, []string{"ip", "link", "del", ifbName})
	}
	return cmds, blocked
}

// danglingRedirectDeletes removes flower mirred filters aimed at ifbxui or at
// a missing device ("device *"), which drops every matching packet.
func danglingRedirectDeletes(iface, ingress string) [][]string {
	var cmds [][]string
	for _, block := range filterBlocks(ingress) {
		lower := strings.ToLower(block)
		if !strings.Contains(lower, "mirred") {
			continue
		}
		if !strings.Contains(block, ifbName) && !strings.Contains(block, "device *") {
			continue
		}
		proto, pref, handle, ok := parseFlowerHandle(block)
		if !ok {
			continue
		}
		cmds = append(cmds, []string{
			"tc", "filter", "del", "dev", iface, "ingress",
			"protocol", proto, "pref", pref, "handle", handle, "flower",
		})
	}
	return cmds
}

func parseFlowerHandle(block string) (proto, pref, handle string, ok bool) {
	fields := strings.Fields(block)
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "protocol":
			if i+1 < len(fields) {
				proto = fields[i+1]
			}
		case "pref":
			if i+1 < len(fields) {
				pref = fields[i+1]
			}
		case "handle":
			if i+1 < len(fields) {
				handle = fields[i+1]
			}
		}
	}
	if proto == "" || pref == "" || handle == "" {
		return "", "", "", false
	}
	return proto, pref, handle, true
}

// linkIsUp reports whether ip-link show output lists name with the UP flag.
func linkIsUp(name, output string) bool {
	if !strings.Contains(output, name+":") && !strings.Contains(output, name+" ") {
		return false
	}
	start := strings.Index(output, "<")
	end := strings.Index(output, ">")
	if start < 0 || end <= start {
		return false
	}
	for _, flag := range strings.Split(output[start+1:end], ",") {
		if flag == "UP" {
			return true
		}
	}
	return false
}
