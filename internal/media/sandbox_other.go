//go:build !linux

package media

import "context"

func New(Options) (*Processor, error) { return nil, mediaError("unavailable") }
func (p *Processor) run(context.Context, string, string, string, []string) ([]byte, error) {
	return nil, mediaError("unavailable")
}
