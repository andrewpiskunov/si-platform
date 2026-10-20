package postgres

import (
	"context"
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"siplatform/internal/game"
	"siplatform/internal/packs"
)

// GameSetup is trusted server input, never a client-supplied package document.
type GameSetup struct {
	ActorID, CredentialID string
	PackID                string
	Revision              int
	Title, Theme          string
	DemoDocument          *packs.Document
}

func (g GameSetup) authorize(ctx context.Context, tx pgx.Tx) error {
	if utf8.RuneCountInString(g.Title) < 1 || utf8.RuneCountInString(g.Title) > 80 || strings.ContainsFunc(g.Title, unicode.IsControl) {
		return game.Fault("invalid_title")
	}
	if g.Theme != "classic" && g.Theme != "pirate" && g.Theme != "anime" {
		return game.Fault("invalid_theme")
	}
	var user string
	err := tx.QueryRow(ctx, "SELECT id FROM account_users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE", g.ActorID).Scan(&user)
	if err != nil {
		return game.Fault("unauthorized")
	}
	var valid bool
	err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM account_credentials WHERE id=$1 AND user_id=$2 AND kind='session' AND revoked_at IS NULL AND expires_at>clock_timestamp())", g.CredentialID, user).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return game.Fault("unauthorized")
	}
	if g.PackID != "" {
		// Lock the pack through commit: moderation cannot invalidate permission
		// between ForPlay validation and making the new room durable.
		var owner string
		var published *int
		var blocked, deleted, ownerDeleted bool
		err = tx.QueryRow(ctx, `SELECT p.owner_id,p.published_revision,p.blocked,p.deleted_at IS NOT NULL,u.deleted_at IS NOT NULL FROM library_packs p JOIN account_users u ON u.id=p.owner_id WHERE p.id=$1 FOR SHARE OF p`, g.PackID).Scan(&owner, &published, &blocked, &deleted, &ownerDeleted)
		if err != nil {
			return normalize(err)
		}
		if blocked || deleted || ownerDeleted {
			return game.Fault("blocked")
		}
		if owner != user && (published == nil || *published != g.Revision) {
			return game.Fault("forbidden")
		}
	}
	return nil
}
func (g GameSetup) save(ctx context.Context, tx pgx.Tx, room string) error {
	var pack any
	var revision any
	var demo []byte
	if g.PackID != "" {
		pack = g.PackID
		revision = g.Revision
	} else {
		if g.DemoDocument == nil {
			return game.Fault("invalid_package")
		}
		var err error
		demo, err = json.Marshal(g.DemoDocument)
		if err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, "INSERT INTO room_packs(room_id,owner_id,pack_id,revision,title,theme,demo_document) VALUES($1,$2,$3,$4,$5,$6,$7)", room, g.ActorID, pack, revision, g.Title, g.Theme, demo)
	return err
}

type GamePin struct {
	PackID       string
	Revision     int
	Title, Theme string
	DemoDocument *packs.Document
}

func (s *Store) GamePin(ctx context.Context, id string) (GamePin, error) {
	var p GamePin
	var data []byte
	err := s.pool.QueryRow(ctx, "SELECT COALESCE(pack_id,''),COALESCE(revision,0),title,theme,demo_document FROM room_packs WHERE room_id=$1", id).Scan(&p.PackID, &p.Revision, &p.Title, &p.Theme, &data)
	if err != nil {
		return p, normalize(err)
	}
	if len(data) > 0 {
		var doc packs.Document
		if err = json.Unmarshal(data, &doc); err != nil {
			return p, err
		}
		p.DemoDocument = &doc
	}
	return p, nil
}
