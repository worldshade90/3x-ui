package speedlimit

// MarkBase is the high byte of every fwmark this panel owns.
// 0x3A is outside the marks operators usually pick for policy routing.
const MarkBase uint32 = 0x3A000000

// MarkMask selects that high byte. nftables and ip rule both match it.
const MarkMask uint32 = 0xFF000000

// MaxMbit is the largest cap the panel accepts. nftables can express it,
// and a typo past this is almost never intentional.
const MaxMbit = 100000

// maxClientID is the low 24 bits of the fwmark. Client ids above it cannot
// be given a mark without colliding with MarkBase.
const maxClientID = 0x00FFFFFF

// Mark is the fwmark for a client id. The id is the low 24 bits so the same
// client keeps the same mark across panel and host restarts.
func Mark(clientID int) (uint32, bool) {
	if clientID <= 0 || clientID > maxClientID {
		return 0, false
	}
	return MarkBase | uint32(clientID), true
}

// Client is one enabled client whose traffic should be capped.
// Rates are Mbit/s; zero means that direction is unlimited.
type Client struct {
	ID       int
	Email    string
	DownMbit int
	UpMbit   int
}

func usable(clients []Client) []Client {
	out := make([]Client, 0, len(clients))
	for _, c := range clients {
		if c.Email == "" || (c.DownMbit <= 0 && c.UpMbit <= 0) {
			continue
		}
		if _, ok := Mark(c.ID); !ok {
			continue
		}
		out = append(out, c)
	}
	return out
}
