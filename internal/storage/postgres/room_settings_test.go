package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"siplatform/examples"
	"siplatform/internal/accounts"
	"siplatform/internal/game"
	"siplatform/internal/storage/postgres"
	"siplatform/internal/testutil"
	"siplatform/migrations"
)

func settingsRoom(t *testing.T, s *postgres.Store, owner accounts.Identity, visibility string) postgres.Credentials {
	t.Helper()
	doc := examples.Demo()
	rules := game.DefaultRules()
	setup := postgres.GameSetup{ActorID: owner.User.ID, CredentialID: owner.SessionID, Title: "Open quiz", Theme: "classic", DemoDocument: &doc, Rules: &rules}
	if visibility != "" {
		setup.Settings = &game.RoomSettings{Visibility: visibility, VoiceURL: "https://voice.example/PRIVATE-VOICE"}
	}
	h, err := s.CreateGame(context.Background(), "PRIVATE-HOST-NAME", setup)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func settingsCommand(state game.State, id, visibility string) game.Command {
	raw, _ := json.Marshal(game.SettingsPayload{Settings: game.RoomSettings{Visibility: visibility, VoiceURL: "https://voice.example/PRIVATE-VOICE"}})
	return game.Command{Version: 2, ID: id, Type: "set_room_settings", ExpectedSeq: &state.Seq, Data: raw}
}

func TestPublicRoomListingAdmissionAndPrivacy(t *testing.T) {
	ctx := context.Background()
	s, unconfigured, _ := v2Room(t, game.DefaultRules())
	owner := appearanceIdentity(t, s, "room_owner")
	private := settingsRoom(t, s, owner, "")
	state, _ := s.Load(ctx, private.RoomID)
	if state.Settings.Visibility != "private" || state.Settings.VoiceURL != "" {
		t.Fatal("default not private", state.Settings)
	}
	if _, err := s.Join(ctx, private.RoomID, "", "Guest", game.Player); game.Code(err) != "unauthorized" {
		t.Fatal("private free entry", err)
	}
	var ids []string
	var hosts []postgres.Credentials
	for range 3 {
		h := settingsRoom(t, s, owner, "public")
		ids = append(ids, h.RoomID)
		hosts = append(hosts, h)
	}
	// A raw engine room is not a configured public game.
	v2(t, s, unconfigured, "open-unconfigured", "set_room_settings", game.SettingsPayload{Settings: game.RoomSettings{Visibility: "public"}})
	slices.Sort(ids)
	first, err := s.ListPublicRooms(ctx, "", 2)
	if err != nil || len(first.Items) != 2 || first.NextAfter != ids[1] {
		t.Fatal("first page", first, err)
	}
	second, err := s.ListPublicRooms(ctx, first.NextAfter, 2)
	if err != nil || len(second.Items) != 1 || second.NextAfter != "" || second.Items[0].RoomID != ids[2] {
		t.Fatal("second page", second, err)
	}
	for i, item := range first.Items {
		if item.RoomID != ids[i] || item.Title != "Open quiz" || item.Players != 0 || item.Spectators != 0 || item.PlayerLimit != 12 || item.SpectatorLimit != 20 || !item.CanJoinPlayer || !item.CanJoinSpectator {
			t.Fatal("listing", item)
		}
	}
	raw, _ := json.Marshal(first)
	for _, secret := range []string{private.RoomID, unconfigured.RoomID, "PRIVATE-HOST", "PRIVATE-VOICE", "participants", "settings", "snapshot", "pack_id", "token", "appearance", hosts[0].Token, hosts[0].InviteToken} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("public list leak", secret)
		}
	}
	for _, cursor := range []string{"bad", strings.Repeat("z", 32), strings.Repeat("0", 31), strings.Repeat("0", 33)} {
		if _, err = s.ListPublicRooms(ctx, cursor, 1); game.Code(err) != "invalid_input" {
			t.Fatal("invalid cursor", cursor, err)
		}
	}
	for _, limit := range []int{0, -1, 51} {
		if _, err = s.ListPublicRooms(ctx, "", limit); game.Code(err) != "invalid_input" {
			t.Fatal("invalid limit", limit, err)
		}
	}
	h := hosts[0]
	for i := range 12 {
		if _, err = s.Join(ctx, h.RoomID, "", fmt.Sprint("Player ", i), game.Player); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.Join(ctx, h.RoomID, "", "Thirteenth", game.Player); game.Code(err) != "room_full" {
		t.Fatal("player capacity", err)
	}
	for i := range 20 {
		if _, err = s.Join(ctx, h.RoomID, "", fmt.Sprint("Viewer ", i), game.Spectator); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.Join(ctx, h.RoomID, "", "Twentyfirst", game.Spectator); game.Code(err) != "room_full" {
		t.Fatal("spectator capacity", err)
	}
	page, _ := s.ListPublicRooms(ctx, "", 50)
	for _, item := range page.Items {
		if item.RoomID == h.RoomID && (item.Players != 12 || item.Spectators != 20 || item.CanJoinPlayer || item.CanJoinSpectator) {
			t.Fatal("full room advertised free seats", item)
		}
	}
	v2(t, s, h, "done", "finish", game.EmptyPayload{})
	page, _ = s.ListPublicRooms(ctx, "", 50)
	if len(page.Items) != 2 {
		t.Fatal("finished room still listed", page)
	}
}

func TestRoomPrivacyTransitionInvalidatesInviteExactlyOnce(t *testing.T) {
	ctx := context.Background()
	s, _, _ := v2Room(t, game.DefaultRules())
	owner := appearanceIdentity(t, s, "privacy_owner")
	h := settingsRoom(t, s, owner, "public")
	p, err := s.Join(ctx, h.RoomID, "", "Admitted", game.Player)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := s.Load(ctx, h.RoomID)
	cmd := settingsCommand(state, "close", "private")
	denied, err := s.Execute(ctx, h.RoomID, p.ParticipantID, cmd)
	if err != nil || denied.Code != "forbidden" {
		t.Fatal("player settings", denied, err)
	}
	out, err := s.Execute(ctx, h.RoomID, h.ParticipantID, cmd)
	if err != nil || out.Code != "ok" || out.State.Settings.Visibility != "private" {
		t.Fatal(out, err)
	}
	for _, invite := range []string{"", h.InviteToken} {
		if _, err = s.Join(ctx, h.RoomID, invite, "Denied", game.Spectator); game.Code(err) != "unauthorized" {
			t.Fatal("closure admitted guest", err)
		}
	}
	if id, err := s.Authenticate(ctx, h.RoomID, p.Token); err != nil || id != p.ParticipantID {
		t.Fatal("closure revoked member", err)
	}
	newInvite, _, err := s.RotateInvite(ctx, h.RoomID, h.ParticipantID)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.Execute(ctx, h.RoomID, h.ParticipantID, cmd)
	if err != nil || !replay.Duplicate || replay.Code != "ok" {
		t.Fatal("replay", replay, err)
	}
	if _, err = s.Join(ctx, h.RoomID, newInvite, "Invited", game.Spectator); err != nil {
		t.Fatal("replay revoked later invitation", err)
	}
	page, _ := s.ListPublicRooms(ctx, "", 50)
	if len(page.Items) != 0 {
		t.Fatal("private room discoverable", page)
	}
	state, _ = s.Load(ctx, h.RoomID)
	stale := settingsCommand(out.State, "stale", "public")
	out, err = s.Execute(ctx, h.RoomID, h.ParticipantID, stale)
	if err != nil || out.Code != "stale_state" {
		t.Fatal("stale settings", out, err)
	}
	results, err := s.Results(ctx, h.RoomID, p.ParticipantID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(results)
	for _, secret := range []string{newInvite, "PRIVATE-VOICE", "command", "payload", h.Token, h.InviteToken} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("journal leak", secret)
		}
	}
}

func TestRoomAdmissionRechecksSettingsAfterRowLock(t *testing.T) {
	ctx := context.Background()
	s, _, _ := v2Room(t, game.DefaultRules())
	owner := appearanceIdentity(t, s, "locked_owner")
	h := settingsRoom(t, s, owner, "public")
	tx, err := s.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var blocker int
	if err = tx.QueryRow(ctx, "SELECT pg_backend_pid() FROM rooms WHERE id=$1 FOR UPDATE", h.RoomID).Scan(&blocker); err != nil {
		t.Fatal(err)
	}
	joinCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, e := s.Join(joinCtx, h.RoomID, "", "Waiting", game.Player); done <- e }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var waiting bool
		if err = s.Pool().QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))", blocker).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-done:
			t.Fatal("join bypassed room lock", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("join never reached row lock")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err = tx.Exec(ctx, `UPDATE rooms SET snapshot=jsonb_set(snapshot,'{settings,visibility}','"private"'),invite_hash='invalidated' WHERE id=$1`, h.RoomID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; game.Code(err) != "unauthorized" {
		t.Fatal("stale public admission", err)
	}
}

func TestPublicRulesJournal(t *testing.T) {
	ctx := context.Background()
	s, h, p := v2Room(t, game.DefaultRules())
	rules := game.DefaultRules()
	rules.AnswerSeconds = 25
	rules.BuzzerMode = "random"
	v2(t, s, h, "rules", "set_rules", game.RulesPayload{Rules: rules})
	v2Question(t, s, h)
	result, err := s.Results(ctx, h.RoomID, p[0].ParticipantID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, event := range result.Items {
		if event.RulesChange == nil {
			continue
		}
		found++
		if event.Kind != "set_rules" || event.RulesChange.Version != 2 || event.RulesChange.Rules != rules {
			t.Fatal("incorrect rule delta", event)
		}
	}
	if found != 1 {
		t.Fatal("missing/duplicate rule change", found)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "PRIVATE-ANSWER") || strings.Contains(string(raw), "command") {
		t.Fatal("raw game payload leaked")
	}
}

func TestRoomSettingsMigrationDefaultsAndDerivedIndex(t *testing.T) {
	ctx := context.Background()
	pool := testutil.Pool(t)
	db := stdlib.OpenDB(*pool.Config().ConnConfig)
	defer db.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 10); err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 32)
	if _, err = pool.Exec(ctx, `INSERT INTO rooms(id,snapshot,seq,invite_hash) VALUES($1,jsonb_build_object('room_id',$1::text,'seq',1,'phase','lobby'),1,'old')`, id); err != nil {
		t.Fatal(err)
	}
	if err = postgres.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err = postgres.Migrate(ctx, pool); err != nil {
		t.Fatal("repeat", err)
	}
	var visibility string
	if err = pool.QueryRow(ctx, "SELECT visibility FROM rooms WHERE id=$1", id).Scan(&visibility); err != nil || visibility != "private" {
		t.Fatal("backfill", visibility, err)
	}
	state, err := postgres.New(pool).Load(ctx, id)
	if err != nil || state.Settings.Visibility != "private" {
		t.Fatal("legacy decode", state.Settings, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE rooms SET snapshot=snapshot||'{"settings":{"visibility":"public","voice_url":""}}'::jsonb WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, "SELECT visibility FROM rooms WHERE id=$1", id).Scan(&visibility); err != nil || visibility != "public" {
		t.Fatal("derived visibility", visibility, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE rooms SET snapshot=jsonb_set(snapshot,'{settings,visibility}','"invalid"') WHERE id=$1`, id); err == nil {
		t.Fatal("invalid stored visibility accepted")
	}
	if _, err = provider.DownTo(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if err = postgres.Migrate(ctx, pool); err != nil {
		t.Fatal("reapply", err)
	}
}
