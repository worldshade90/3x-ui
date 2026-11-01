package speedlimit

import (
	"net/netip"
	"regexp"
	"sort"
	"strings"
)

const maxIPsPerClient = 64

var ifaceName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,14}$`)

// NormalizeIPs keeps public and private unicast addresses, drops the rest, and
// caps the list so one client cannot install an unbounded set of filters.
func NormalizeIPs(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		raw = strings.TrimSpace(raw)
		if i := strings.IndexByte(raw, '%'); i >= 0 {
			raw = raw[:i]
		}
		addr, err := netip.ParseAddr(raw)
		if err != nil {
			continue
		}
		addr = addr.Unmap()
		if !addr.IsValid() || addr.IsUnspecified() || addr.IsLoopback() || addr.IsMulticast() {
			continue
		}
		s := addr.String()
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
		if len(out) == maxIPsPerClient {
			break
		}
	}
	sort.Strings(out)
	return out
}

func validIface(name string) bool {
	return ifaceName.MatchString(name)
}

func parseDev(route string) string {
	fields := strings.Fields(route)
	for i := 0; i < len(fields)-1; i++ {
		if fields[i] == "dev" && validIface(fields[i+1]) {
			return fields[i+1]
		}
	}
	return ""
}

func isV6(ip string) bool {
	addr, err := netip.ParseAddr(ip)
	return err == nil && addr.Is6()
}
