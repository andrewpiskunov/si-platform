package ingest

import (
	"bytes"
	"context"
	"encoding/binary"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"net"
	"sync/atomic"
	"testing"
)

// Drop PostgreSQL's COMMIT acknowledgement after the backend has committed it.
// Real server traffic is framed, without faking the transaction result in production code.
type lostCommitConn struct {
	net.Conn
	pending []byte
	armed   *atomic.Bool
}

func (c *lostCommitConn) Read(p []byte) (int, error) {
	if len(c.pending) == 0 {
		header := make([]byte, 5)
		if _, e := io.ReadFull(c.Conn, header); e != nil {
			return 0, e
		}
		size := int(binary.BigEndian.Uint32(header[1:]))
		if size < 4 || size > 1<<24 {
			return 0, io.ErrUnexpectedEOF
		}
		body := make([]byte, size-4)
		if _, e := io.ReadFull(c.Conn, body); e != nil {
			return 0, e
		}
		if header[0] == 'C' && string(body) == "COMMIT\x00" && c.armed.Swap(false) {
			c.Conn.Close()
			return 0, io.ErrUnexpectedEOF
		}
		c.pending = append(header, body...)
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}
func TestCatalogueLostCommitAcknowledgementRetainsResumableStage(t *testing.T) {
	f := setup(t)
	data := archive(t, textXML, nil)
	in := catalogueInput(data)
	if _, e := f.pool.Exec(bg, `UPDATE account_users SET role='admin' WHERE id=$1`, f.owner.User.ID); e != nil {
		t.Fatal(e)
	}
	var armed atomic.Bool
	cfg := f.pool.Config().Copy()
	dial := cfg.ConnConfig.DialFunc
	cfg.ConnConfig.DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, e := dial(ctx, network, addr)
		if e != nil {
			return nil, e
		}
		return &lostCommitConn{Conn: c, armed: &armed}, nil
	}
	p, e := pgxpool.NewWithConfig(bg, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	f.service.pool = p
	armed.Store(true)
	if _, e = f.service.CreateCatalogue(bg, f.owner, in); e == nil {
		t.Fatal("expected lost acknowledgement")
	}
	if armed.Load() {
		t.Fatal("COMMIT reply was not intercepted")
	}
	f.service.pool = f.pool
	replay, e := f.service.CreateCatalogue(bg, f.owner, in)
	if e != nil || replay.Status != "uploading" {
		t.Fatal(replay, e)
	}
	if _, e = f.service.Append(bg, f.owner, replay.ID, 0, bytes.NewReader(data), int64(len(data))); e != nil {
		t.Fatal("committed receipt cannot resume", e)
	}
	if _, e = f.service.Complete(bg, f.owner, replay.ID); e != nil {
		t.Fatal(e)
	}
	f.service.Start(bg)
	awaitStatus(t, f, replay.ID, "ready")
}
