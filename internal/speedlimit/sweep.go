package speedlimit

import "strings"

// ClsactOwned reports whether filter-show output is empty or contains only
// filters this panel installs (ifbxui redirect or the 0x7875 police range).
func ClsactOwned(output string) (empty, owned bool) {
	blocks := filterBlocks(output)
	if len(blocks) == 0 {
		return true, true
	}
	for _, block := range blocks {
		if strings.Contains(block, ifbName) || strings.Contains(block, "0x7875") {
			continue
		}
		return false, false
	}
	return false, true
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

// PlanSweep removes ifbxui and, when every clsact filter is ours, the clsact
// qdisc. A foreign filter blocks the sweep unless force is set.
func PlanSweep(iface, egress, ingress string, force bool) (cmds [][]string, blocked bool) {
	cmds = append(cmds, []string{"ip", "link", "del", ifbName})
	_, ownedE := ClsactOwned(egress)
	emptyE, _ := ClsactOwned(egress)
	emptyI, ownedI := ClsactOwned(ingress)
	if emptyE && emptyI {
		return cmds, false
	}
	if (!ownedE || !ownedI) && !force {
		return cmds, true
	}
	cmds = append(cmds, []string{"tc", "qdisc", "del", "dev", iface, "clsact"})
	return cmds, false
}
