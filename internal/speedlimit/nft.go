package speedlimit

import (
	"fmt"
	"sort"
	"strings"
)

const tableName = "xui_speed"

// kbytesPerSec converts Mbit/s to nftables kbytes/second.
// nftables 1.0.9 (Ubuntu 24.04) rejects "mbit"; 1 Mbit/s is 125 kB/s decimal, matching tc.
func kbytesPerSec(mbit int) (rate, burst int) {
	rate = mbit * 125
	if rate < 1 {
		rate = 1
	}
	burst = rate / 10
	if burst < 16 {
		burst = 16
	}
	return rate, burst
}

// RenderRules is the nft script that meters each client.
// An empty client list returns an empty script, which the applier treats as teardown.
func RenderRules(clients []Client) string {
	active := usable(clients)
	if len(active) == 0 {
		return ""
	}
	sort.Slice(active, func(i, j int) bool { return active[i].ID < active[j].ID })

	var b strings.Builder
	fmt.Fprintf(&b, "add table inet %s\n", tableName)
	fmt.Fprintf(&b, "flush table inet %s\n", tableName)
	fmt.Fprintf(&b, "add chain inet %s output { type filter hook output priority mangle; policy accept; }\n", tableName)
	// Save the socket mark onto the connection before any upload drop, so reply packets can be capped too.
	fmt.Fprintf(&b, "add rule inet %s output meta mark & 0xff000000 == 0x%x ct mark set meta mark\n", tableName, MarkBase)

	needDown := false
	for _, c := range active {
		mark, _ := Mark(c.ID)
		if c.UpMbit > 0 {
			rate, burst := kbytesPerSec(c.UpMbit)
			fmt.Fprintf(&b, "add rule inet %s output meta mark 0x%x limit rate over %d kbytes/second burst %d kbytes drop\n",
				tableName, mark, rate, burst)
		}
		if c.DownMbit > 0 {
			needDown = true
		}
	}
	if needDown {
		fmt.Fprintf(&b, "add chain inet %s prerouting { type filter hook prerouting priority mangle; policy accept; }\n", tableName)
		for _, c := range active {
			if c.DownMbit <= 0 {
				continue
			}
			mark, _ := Mark(c.ID)
			rate, burst := kbytesPerSec(c.DownMbit)
			fmt.Fprintf(&b, "add rule inet %s prerouting ct mark 0x%x limit rate over %d kbytes/second burst %d kbytes drop\n",
				tableName, mark, rate, burst)
		}
	}
	return b.String()
}
