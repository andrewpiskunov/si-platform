//go:build unix

package catalogueimport

import (
	"errors"
	"os"
	"syscall"
)

// O_NONBLOCK prevents FIFO open from waiting for a peer. Validate the opened
// descriptor, not a pathname that can be replaced between stat and open.
func openRegular(root *os.Root, name string) (*os.File, error) {
	var f *os.File
	var e error
	flags := os.O_RDONLY | syscall.O_NONBLOCK | syscall.O_NOCTTY
	if root == nil {
		f, e = os.OpenFile(name, flags, 0)
	} else {
		f, e = root.OpenFile(name, flags, 0)
	}
	if e != nil {
		return nil, e
	}
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("not_regular_file")
	}
	return f, nil
}
