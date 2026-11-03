package job

import (
	"errors"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

type fakeOnline struct {
	users []xray.OnlineUser
	ok    bool
	err   error
}

func (f fakeOnline) GetOnlineUsers() ([]xray.OnlineUser, bool, error) {
	return f.users, f.ok, f.err
}

func TestSpeedLimitSnapshotKeepsAPIAddressesWhenStatsMiss(t *testing.T) {
	j := &SpeedLimitJob{online: fakeOnline{
		ok:    true,
		users: []xray.OnlineUser{{Email: "a@x", IPs: []xray.OnlineIP{{IP: "203.0.113.9"}}}},
	}}
	got, ok := j.snapshot()
	if !ok || len(got["a@x"]) != 1 || got["a@x"][0] != "203.0.113.9" {
		t.Fatalf("first snapshot = %#v ok=%v", got, ok)
	}

	j.online = fakeOnline{ok: false, err: errors.New("stats unavailable")}
	got, ok = j.snapshot()
	if !ok {
		t.Fatal("a missed poll dropped the last observation")
	}
	found := false
	for _, ip := range got["a@x"] {
		if ip == "203.0.113.9" {
			found = true
		}
	}
	if !found {
		t.Fatalf("api address missing after a failed poll: %#v", got)
	}
}
