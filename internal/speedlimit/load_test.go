package speedlimit

import (
	"path/filepath"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestLoadActiveSkipsRemoteMTProtoAndUnlimited(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	db := database.GetDB()
	node := 4
	inbounds := []model.Inbound{
		{UserId: 1, Port: 443, Protocol: model.VLESS, Enable: true, Tag: "local-vless", Settings: "{}"},
		{UserId: 1, Port: 8443, Protocol: model.MTProto, Enable: true, Tag: "local-mtg", Settings: "{}"},
		{UserId: 1, Port: 9443, Protocol: model.VLESS, Enable: true, Tag: "node-vless", Settings: "{}", NodeID: &node},
		{UserId: 1, Port: 10443, Protocol: model.VLESS, Enable: false, Tag: "off-vless", Settings: "{}"},
	}
	for i := range inbounds {
		if err := db.Create(&inbounds[i]).Error; err != nil {
			t.Fatalf("inbound %s: %v", inbounds[i].Tag, err)
		}
	}
	records := []model.ClientRecord{
		{Email: "capped@x", Enable: true, SpeedDown: 10, SpeedUp: 2},
		{Email: "mtg@x", Enable: true, SpeedDown: 10, SpeedUp: 2},
		{Email: "remote@x", Enable: true, SpeedDown: 10, SpeedUp: 2},
		{Email: "disabled@x", Enable: true, SpeedDown: 8, SpeedUp: 1},
		{Email: "free@x", Enable: true},
		{Email: "offib@x", Enable: true, SpeedDown: 4},
	}
	if err := db.Create(&records).Error; err != nil {
		t.Fatalf("clients: %v", err)
	}
	if err := db.Model(&model.ClientRecord{}).Where("email = ?", "disabled@x").UpdateColumn("enable", false).Error; err != nil {
		t.Fatal(err)
	}
	links := []model.ClientInbound{
		{ClientId: records[0].Id, InboundId: inbounds[0].Id},
		{ClientId: records[1].Id, InboundId: inbounds[1].Id},
		{ClientId: records[2].Id, InboundId: inbounds[2].Id},
		{ClientId: records[3].Id, InboundId: inbounds[0].Id},
		{ClientId: records[4].Id, InboundId: inbounds[0].Id},
		{ClientId: records[5].Id, InboundId: inbounds[3].Id},
	}
	if err := db.Create(&links).Error; err != nil {
		t.Fatalf("links: %v", err)
	}

	got, err := LoadActive(db)
	if err != nil {
		t.Fatalf("LoadActive: %v", err)
	}
	if len(got) != 1 || got[0].Email != "capped@x" || got[0].DownMbit != 10 || got[0].UpMbit != 2 || got[0].ID != records[0].Id {
		t.Fatalf("LoadActive = %+v, want only capped@x", got)
	}
}
