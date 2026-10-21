package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"siplatform/internal/game"
)

type AppearanceActor struct{ UserID, CredentialID string }

// ExecuteAppearance is an HTTP-authorized command path. Ordinary Execute cannot
// apply this command: a room credential alone never authorizes a private theme.
func (s *Store) ExecuteAppearance(ctx context.Context, roomID, participantID string, c game.Command, actor AppearanceActor) (Outcome, error) {
	if c.Type != "set_appearance" {
		return Outcome{}, game.Fault("forbidden")
	}
	return s.execute(ctx, roomID, participantID, c, &actor)
}

// Locks follow account -> theme -> room, also used by creation and theme writes.
func authorizeAppearance(ctx context.Context, tx pgx.Tx, actor AppearanceActor, a game.Appearance) error {
	if err := a.Validate(); err != nil {
		return err
	}
	if a.Builtin != "" {
		return nil
	}
	var user string
	err := tx.QueryRow(ctx, "SELECT id FROM account_users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE", actor.UserID).Scan(&user)
	if errors.Is(err, pgx.ErrNoRows) {
		return game.Fault("unauthorized")
	}
	if err != nil {
		return err
	}
	var live bool
	err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM account_credentials WHERE id=$1 AND user_id=$2 AND kind='session' AND revoked_at IS NULL AND expires_at>clock_timestamp())", actor.CredentialID, user).Scan(&live)
	if err != nil {
		return err
	}
	if !live {
		return game.Fault("unauthorized")
	}
	var owner string
	var blocked, deleted bool
	err = tx.QueryRow(ctx, "SELECT owner_id,blocked,deleted_at IS NOT NULL FROM themes WHERE id=$1 FOR SHARE", a.ThemeID).Scan(&owner, &blocked, &deleted)
	if err != nil {
		return normalize(err)
	}
	if owner != user {
		return game.Fault("forbidden")
	}
	if blocked || deleted {
		return game.Fault("blocked")
	}
	var hash string
	err = tx.QueryRow(ctx, "SELECT prepared->>'hash' FROM theme_versions WHERE theme_id=$1 AND revision=$2", a.ThemeID, a.Revision).Scan(&hash)
	if err != nil {
		return normalize(err)
	}
	if hash != a.Hash {
		return game.Fault("invalid_appearance")
	}
	return nil
}

func saveAppearance(ctx context.Context, tx pgx.Tx, roomID string, a game.Appearance) error {
	if a.Builtin != "" {
		_, err := tx.Exec(ctx, "DELETE FROM room_theme_pins WHERE room_id=$1", roomID)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, "UPDATE room_packs SET theme=$2 WHERE room_id=$1", roomID, a.Builtin)
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO room_theme_pins(room_id,theme_id,revision,hash) VALUES($1,$2,$3,$4)
 ON CONFLICT(room_id) DO UPDATE SET theme_id=EXCLUDED.theme_id,revision=EXCLUDED.revision,hash=EXCLUDED.hash`, roomID, a.ThemeID, a.Revision, a.Hash)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "UPDATE room_packs SET theme='classic' WHERE room_id=$1", roomID)
	return err
}

func (g GameSetup) selectedAppearance() game.Appearance {
	if g.Appearance != nil {
		return *g.Appearance
	}
	builtin := g.Theme
	if builtin == "" {
		builtin = "classic"
	}
	return game.Appearance{Builtin: builtin}
}
