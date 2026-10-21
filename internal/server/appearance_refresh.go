package server

import (
	"context"
	"time"
)

func (s *Server) watchAppearances() {
	defer s.wg.Done()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(s.ctx, 4*time.Second)
			_ = s.refreshAppearances(ctx)
			cancel()
		}
	}
}

// One batched query per group of active rooms, never a timer/query per player.
// Delivery independently checks the current authorization on every HTTP request.
func (s *Server) refreshAppearances(ctx context.Context) error {
	s.appearanceMu.Lock()
	defer s.appearanceMu.Unlock()
	s.mu.Lock()
	rooms := make(map[string]*room, len(s.rooms))
	ids := make([]string, 0, len(s.rooms))
	for id, rm := range s.rooms {
		rooms[id] = rm
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for start := 0; start < len(ids); start += 256 {
		batch := ids[start:min(start+256, len(ids))]
		rows, err := s.store.Pool().Query(ctx, `SELECT p.room_id,p.theme_id||':'||p.revision::text||':'||p.hash||':'||(t.blocked OR u.deleted_at IS NOT NULL)::text FROM room_theme_pins p JOIN themes t ON t.id=p.theme_id JOIN account_users u ON u.id=t.owner_id WHERE p.room_id=ANY($1)`, batch)
		if err != nil {
			return err
		}
		values := make(map[string]string, len(batch))
		for rows.Next() {
			var id, value string
			if err = rows.Scan(&id, &value); err != nil {
				break
			}
			values[id] = value
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return err
		}
		for _, id := range batch {
			value := values[id]
			previous, known := s.appearanceKnown[id]
			if known && previous == value {
				continue
			}
			if !known && value == "" {
				s.appearanceKnown[id] = value
				continue
			}
			_, err = rooms[id].do(ctx, func(ctx context.Context, rm *room) (any, error) {
				for c := range rm.clients {
					c.send(Message{Type: "appearance_refresh"})
				}
				return nil, nil
			})
			if err != nil {
				return err
			}
			s.appearanceKnown[id] = value
		}
	}
	return nil
}
