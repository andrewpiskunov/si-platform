//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package blob

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"time"
)

// Separate open descriptions make flock serialize independent Store instances
// and processes on the same directory. No network/database work under this lock
// except the short caller guard check; payload staging happens before locking.
func lockLocalMutation(ctx context.Context, root *os.Root) (func(), error) {
	file, err := root.Open(".")
	if err != nil {
		return nil, ErrStorage
	}
	for {
		if err = ctx.Err(); err != nil {
			file.Close()
			return nil, err
		}
		err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() { _ = unix.Flock(int(file.Fd()), unix.LOCK_UN); _ = file.Close() }, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EINTR) {
			file.Close()
			return nil, ErrStorage
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			file.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
