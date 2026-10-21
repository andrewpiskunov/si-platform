//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly

package blob

import (
	"context"
	"os"
)

// The server deployment target is Linux. Never silently substitute an in-memory
// mutex on platforms without the cross-process filesystem fence.
func lockLocalMutation(ctx context.Context, root *os.Root) (func(), error) { return nil, ErrStorage }
