package postgres_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"siplatform/examples"
	"siplatform/internal/accounts"
	"siplatform/internal/game"
	"siplatform/internal/storage/postgres"
)

func appearanceIdentity(t *testing.T, s *postgres.Store, name string) accounts.Identity {
	t.Helper()
	a := accounts.New(s.Pool(), accounts.Options{})
	session, err := a.Register(context.Background(), name, "long-theme-password", name)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := a.Authenticate(context.Background(), session.Token)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}
func storedAppearance(t *testing.T, s *postgres.Store, owner accounts.Identity) game.Appearance {
	t.Helper()
	a := game.Appearance{ThemeID: strings.Repeat("c", 32), Revision: 1, Hash: strings.Repeat("d", 64)}
	// The store only consumes identity/revision/hash; parser/service tests own
	// validation of the immutable prepared manifest behind this fixture.
	raw, _ := json.Marshal(map[string]string{"hash": a.Hash})
	_, err := s.Pool().Exec(context.Background(), "INSERT INTO themes(id,owner_id,current_revision) VALUES($1,$2,1)", a.ThemeID, owner.User.ID)
	if err == nil {
		_, err = s.Pool().Exec(context.Background(), "INSERT INTO theme_versions(theme_id,revision,prepared,size_bytes) VALUES($1,1,$2,1)", a.ThemeID, raw)
	}
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func themeCommand(state game.State, id string, a game.Appearance) game.Command {
	raw, _ := json.Marshal(game.AppearancePayload{Appearance: a})
	return game.Command{Version: 2, ID: id, Type: "set_appearance", ExpectedSeq: &state.Seq, Data: raw}
}

func TestAppearanceTransactionPinsAndDeduplicates(t *testing.T) {
	ctx := context.Background()
	s, h, players := v2Room(t, game.DefaultRules())
	owner := appearanceIdentity(t, s, "theme_owner")
	other := appearanceIdentity(t, s, "theme_other")
	a := storedAppearance(t, s, owner)
	initial, _ := s.Load(ctx, h.RoomID)
	cmd := themeCommand(initial, "apply", a)
	if _, err := s.Execute(ctx, h.RoomID, h.ParticipantID, cmd); game.Code(err) != "forbidden" {
		t.Fatal("untrusted command bypass", err)
	}
	if _, err := s.ExecuteAppearance(ctx, h.RoomID, h.ParticipantID, cmd, postgres.AppearanceActor{UserID: other.User.ID, CredentialID: other.SessionID}); game.Code(err) != "forbidden" {
		t.Fatal("foreign theme", err)
	}
	if out, err := s.ExecuteAppearance(ctx, h.RoomID, players[0].ParticipantID, cmd, postgres.AppearanceActor{UserID: owner.User.ID, CredentialID: owner.SessionID}); err == nil && out.Code == "ok" {
		t.Fatal("player applied appearance")
	}
	out, err := s.ExecuteAppearance(ctx, h.RoomID, h.ParticipantID, cmd, postgres.AppearanceActor{UserID: owner.User.ID, CredentialID: owner.SessionID})
	if err != nil || out.Code != "ok" || out.State.Appearance == nil || *out.State.Appearance != a || out.Seq != initial.Seq+1 {
		t.Fatal(out, err)
	}
	var id, hash string
	var revision int
	if err = s.Pool().QueryRow(ctx, "SELECT theme_id,revision,hash FROM room_theme_pins WHERE room_id=$1", h.RoomID).Scan(&id, &revision, &hash); err != nil || id != a.ThemeID || revision != 1 || hash != a.Hash {
		t.Fatal("pin not committed", err)
	}
	repeated, err := s.ExecuteAppearance(ctx, h.RoomID, h.ParticipantID, cmd, postgres.AppearanceActor{UserID: owner.User.ID, CredentialID: owner.SessionID})
	if err != nil || !repeated.Duplicate || repeated.Seq != out.Seq {
		t.Fatal("repeat changed state", repeated, err)
	}
	restored, err := postgres.New(s.Pool()).Load(ctx, h.RoomID)
	if err != nil || restored.Appearance == nil || *restored.Appearance != a {
		t.Fatal("restore lost pin", err)
	}
	_, err = s.Pool().Exec(ctx, "UPDATE themes SET deleted_at=now(),current_revision=2 WHERE id=$1", a.ThemeID)
	if err != nil {
		t.Fatal(err)
	}
	restored, _ = s.Load(ctx, h.RoomID)
	if *restored.Appearance != a {
		t.Fatal("library change mutated room")
	}
	builtin := themeCommand(restored, "builtin", game.Appearance{Builtin: "anime"})
	out, err = s.ExecuteAppearance(ctx, h.RoomID, h.ParticipantID, builtin, postgres.AppearanceActor{})
	if err != nil || out.Code != "ok" {
		t.Fatal(out, err)
	}
	var pins int
	if err = s.Pool().QueryRow(ctx, "SELECT count(*) FROM room_theme_pins WHERE room_id=$1", h.RoomID).Scan(&pins); err != nil || pins != 0 {
		t.Fatal("builtin retained old pin", err)
	}
	_, err = s.ExecuteAppearance(ctx, h.RoomID, h.ParticipantID, themeCommand(out.State, "deleted", a), postgres.AppearanceActor{UserID: owner.User.ID, CredentialID: owner.SessionID})
	if game.Code(err) != "blocked" {
		t.Fatal("deleted theme newly applied", err)
	}
	_, err = s.Pool().Exec(ctx, "UPDATE themes SET deleted_at=NULL,blocked=true WHERE id=$1", a.ThemeID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ExecuteAppearance(ctx, h.RoomID, h.ParticipantID, themeCommand(out.State, "blocked", a), postgres.AppearanceActor{UserID: owner.User.ID, CredentialID: owner.SessionID}); game.Code(err) != "blocked" {
		t.Fatal("blocked theme applied", err)
	}
	_, err = s.Pool().Exec(ctx, "UPDATE themes SET blocked=false WHERE id=$1", a.ThemeID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Pool().Exec(ctx, "UPDATE account_credentials SET revoked_at=now() WHERE id=$1", owner.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ExecuteAppearance(ctx, h.RoomID, h.ParticipantID, themeCommand(out.State, "revoked", a), postgres.AppearanceActor{UserID: owner.User.ID, CredentialID: owner.SessionID}); game.Code(err) != "unauthorized" {
		t.Fatal("revoked credential applied", err)
	}
}

func TestCreateGamePinsCustomAppearance(t *testing.T) {
	ctx := context.Background()
	s, _, _ := v2Room(t, game.DefaultRules())
	owner := appearanceIdentity(t, s, "theme_creator")
	a := storedAppearance(t, s, owner)
	doc := examples.Demo()
	rules := game.DefaultRules()
	setup := postgres.GameSetup{ActorID: owner.User.ID, CredentialID: owner.SessionID, Title: "Custom room", Theme: "classic", Appearance: &a, DemoDocument: &doc, Rules: &rules}
	h, err := s.CreateGame(ctx, "Host", setup)
	if err != nil {
		t.Fatal(err)
	}
	state, err := s.Load(ctx, h.RoomID)
	if err != nil || state.Appearance == nil || *state.Appearance != a {
		t.Fatal("creation lost appearance", err)
	}
	pin, err := s.GamePin(ctx, h.RoomID)
	if err != nil || pin.Appearance == nil || *pin.Appearance != a {
		t.Fatal("board pin lost appearance", err)
	}
	_, err = s.Pool().Exec(ctx, "UPDATE themes SET blocked=true WHERE id=$1", a.ThemeID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateGame(ctx, "Host", setup); game.Code(err) != "blocked" {
		t.Fatal("blocked theme created room", err)
	}
}
