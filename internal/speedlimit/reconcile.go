package speedlimit

import (
	"context"
	"runtime"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"

	"gorm.io/gorm"
)

// Reconcile loads the caps that should be in force and installs or removes
// the host rules. Safe to call on a timer and at startup.
func Reconcile(ctx context.Context) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	clients, err := LoadActive(database.GetDB())
	if err != nil {
		return err
	}
	return Apply(ctx, clients)
}

// LoadActive returns enabled clients on a local non-MTProto inbound.
// Node-hosted inbounds are enforced by that node, not by this process.
func LoadActive(db *gorm.DB) ([]Client, error) {
	if db == nil {
		return nil, nil
	}
	var rows []struct {
		ID        int
		Email     string
		SpeedDown int
		SpeedUp   int
	}
	err := db.Raw(`
SELECT c.id, c.email, c.speed_down, c.speed_up
FROM clients c
WHERE c.enable = ? AND (c.speed_down > 0 OR c.speed_up > 0)
AND EXISTS (
  SELECT 1 FROM client_inbounds ci
  JOIN inbounds i ON i.id = ci.inbound_id
  WHERE ci.client_id = c.id AND i.enable = ? AND i.node_id IS NULL AND i.protocol <> ?
)
ORDER BY c.id`, true, true, string(model.MTProto)).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]Client, 0, len(rows))
	for _, row := range rows {
		out = append(out, Client{
			ID:       row.ID,
			Email:    row.Email,
			DownMbit: row.SpeedDown,
			UpMbit:   row.SpeedUp,
		})
	}
	return out, nil
}
