//go:build !linux

package speedlimit

import "context"

// Apply is a no-op off Linux. Caps are still stored; the host cannot meter them.
func Apply(ctx context.Context, clients []Client) error {
	return nil
}
