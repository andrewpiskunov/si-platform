package postgres_test

import (
	"context"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"siplatform/internal/game"
	"siplatform/internal/storage/postgres"
	"siplatform/migrations"
	"testing"
	"time"
)

func TestRoomRetentionBoundariesAndActivity(t *testing.T) {
	s, h, _ := room(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		phase string
		days  int
	}{{"lobby", 7}, {"running", 30}, {"finished", 30}} {
		t.Run(tc.phase, func(t *testing.T) {
			_, err := s.Pool().Exec(ctx, `UPDATE rooms SET created_at=$2, last_activity_at=$3, finished_at=CASE WHEN $4='finished' THEN $3::timestamptz ELSE NULL END, snapshot=jsonb_set(snapshot,'{phase}',to_jsonb($4::text)) WHERE id=$1`, h.RoomID, now.Add(-40*24*time.Hour), now.Add(-time.Duration(tc.days)*24*time.Hour), tc.phase)
			if err != nil {
				t.Fatal(err)
			}
			info, err := s.Retention(ctx, h.RoomID)
			if err != nil {
				t.Fatal(err)
			}
			if !info.ExpiresAt.Equal(now) {
				t.Fatalf("expiry %s want %s", info.ExpiresAt, now)
			}
			deleted, err := s.RetireRoom(ctx, h.RoomID, now.Add(-time.Nanosecond))
			if err != nil || deleted {
				t.Fatalf("early retire %v %v", deleted, err)
			}
		})
	}
	_, err := s.Pool().Exec(ctx, `UPDATE rooms SET snapshot=jsonb_set(snapshot,'{phase}','"lobby"'), finished_at=NULL,last_activity_at=$2 WHERE id=$1`, h.RoomID, now.Add(-6*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Join(ctx, h.RoomID, h.InviteToken, "New player", game.Spectator); err != nil {
		t.Fatal(err)
	}
	info, err := s.Retention(ctx, h.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if info.LastActivityAt.Before(now) {
		t.Fatal("accepted join did not refresh activity")
	}
	_, err = s.Pool().Exec(ctx, `UPDATE rooms SET created_at=$2 WHERE id=$1`, h.RoomID, now.Add(-90*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := s.RetireRoom(ctx, h.RoomID, now)
	if err != nil || !deleted {
		t.Fatalf("hard bound retire %v %v", deleted, err)
	}
	if _, err = s.Load(ctx, h.RoomID); game.Code(err) != "not_found" {
		t.Fatalf("load retired: %v", err)
	}
	if _, err = s.Join(ctx, h.RoomID, h.InviteToken, "Late", game.Spectator); game.Code(err) != "not_found" {
		t.Fatalf("join retired: %v", err)
	}
	var n int
	if err = s.Pool().QueryRow(ctx, `SELECT count(*) FROM game_events WHERE room_id=$1`, h.RoomID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("events %d %v", n, err)
	}
}

func TestRoomRetentionSystemEventsAndFirstFinish(t *testing.T) {
	s, h, _ := room(t)
	ctx := context.Background()
	old := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	execute(t, s, h, game.Command{ID: "open-retention", Type: "open_question", QuestionID: "q1", Text: "Q", Answer: "A", Value: 100})
	if _, err := s.Pool().Exec(ctx, `UPDATE rooms SET last_activity_at=$2 WHERE id=$1`, h.RoomID, old); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pause(ctx, h.RoomID, "disconnected"); err != nil {
		t.Fatal(err)
	}
	if err := s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(ctx, h.RoomID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(ctx, h.RoomID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, h.RoomID, h.Token); err != nil {
		t.Fatal(err)
	}
	info, err := s.Retention(ctx, h.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if !info.LastActivityAt.Equal(old) {
		t.Fatalf("system event refreshed activity: %s", info.LastActivityAt)
	}
	cmd := game.Command{Version: 1, ID: "finish-retention", Type: "finish"}
	out, err := s.Execute(ctx, h.RoomID, h.ParticipantID, cmd)
	if err != nil || out.Code != "ok" {
		t.Fatalf("finish %+v %v", out, err)
	}
	var first time.Time
	if err = s.Pool().QueryRow(ctx, `SELECT finished_at FROM rooms WHERE id=$1`, h.RoomID).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if first.Before(old.Add(time.Minute)) {
		t.Fatal("first finish not recorded")
	}
	if _, err = s.Pool().Exec(ctx, `UPDATE rooms SET last_activity_at=$2 WHERE id=$1`, h.RoomID, old); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Execute(ctx, h.RoomID, h.ParticipantID, cmd); err != nil {
		t.Fatal(err)
	}
	var repeated time.Time
	if err = s.Pool().QueryRow(ctx, `SELECT finished_at FROM rooms WHERE id=$1`, h.RoomID).Scan(&repeated); err != nil {
		t.Fatal(err)
	}
	if !first.Equal(repeated) {
		t.Fatal("finish timestamp changed")
	}
	info, err = s.Retention(ctx, h.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ExpiresAt.Equal(first.Add(30 * 24 * time.Hour)) {
		t.Fatalf("finished expiry %s", info.ExpiresAt)
	}
}

func TestRoomRetirementFencingAndBoundedCandidates(t *testing.T) {
	s, h, _ := room(t)
	ctx := context.Background()
	now := time.Now()
	if _, err := s.Pool().Exec(ctx, `UPDATE rooms SET created_at=$2 WHERE id=$1`, h.RoomID, now.Add(-91*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	ids, err := s.ExpiredRooms(ctx, now, 1)
	if err != nil || len(ids) != 1 || ids[0] != h.RoomID {
		t.Fatalf("candidates %v %v", ids, err)
	}
	if _, err = s.Pool().Exec(ctx, `UPDATE engine_owner SET epoch=epoch+1`); err != nil {
		t.Fatal(err)
	}
	if deleted, err := s.RetireRoom(ctx, h.RoomID, now); deleted || game.Code(err) != "ownership_lost" {
		t.Fatalf("unfenced retirement %v %v", deleted, err)
	}
	var exists bool
	if err = s.Pool().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM rooms WHERE id=$1)`, h.RoomID).Scan(&exists); err != nil || !exists {
		t.Fatalf("fenced room deleted: %v", err)
	}
}

func TestExpiredRoomCannotBeRevivedBeforeSweep(t *testing.T) {
	s, h, _ := room(t)
	ctx := context.Background()
	if _, err := s.Pool().Exec(ctx, `UPDATE rooms SET last_activity_at=now()-interval '8 days' WHERE id=$1`, h.RoomID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Join(ctx, h.RoomID, h.InviteToken, "Late join", game.Spectator); game.Code(err) != "not_found" {
		t.Fatalf("revived by join: %v", err)
	}
	if _, err := s.Execute(ctx, h.RoomID, h.ParticipantID, game.Command{Version: 1, ID: "late-finish", Type: "finish"}); game.Code(err) != "not_found" {
		t.Fatalf("revived by command: %v", err)
	}
	if _, err := s.Load(ctx, h.RoomID); game.Code(err) != "not_found" {
		t.Fatalf("expired load: %v", err)
	}
	if _, err := s.Authenticate(ctx, h.RoomID, h.Token); game.Code(err) != "not_found" {
		t.Fatalf("expired auth: %v", err)
	}
	if err := s.Recover(ctx); err != nil {
		t.Fatalf("startup blocked by expired room: %v", err)
	}
}

func TestRoomExpiryMigrationAndPins(t *testing.T) {
	s, _, _ := room(t)
	ctx := context.Background()
	owner := appearanceIdentity(t, s, "expiry_owner")
	host := settingsRoom(t, s, owner, "public")
	a := storedAppearance(t, s, owner)
	state, err := s.Load(ctx, host.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ExecuteAppearance(ctx, host.RoomID, host.ParticipantID, themeCommand(state, "expiry-theme", a), postgres.AppearanceActor{UserID: owner.User.ID, CredentialID: owner.SessionID}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool().Exec(ctx, `UPDATE rooms SET created_at=now()-interval '91 days' WHERE id=$1`, host.RoomID); err != nil {
		t.Fatal(err)
	}
	if deleted, err := s.RetireRoom(ctx, host.RoomID, time.Now()); !deleted || err != nil {
		t.Fatalf("pin retirement %v %v", deleted, err)
	}
	for _, table := range []string{"room_packs", "room_theme_pins", "sessions", "game_events", "command_results", "room_participants"} {
		var count int
		if err = s.Pool().QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE room_id=$1`, host.RoomID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s retained %d %v", table, count, err)
		}
	}
	var count int
	if err = s.Pool().QueryRow(ctx, `SELECT count(*) FROM theme_versions WHERE theme_id=$1`, a.ThemeID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("source theme deleted %d %v", count, err)
	}
	sqlDB := stdlib.OpenDB(*s.Pool().Config().ConnConfig)
	defer sqlDB.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.DownTo(ctx, 15); err != nil {
		t.Fatal(err)
	}
	var id string
	if err = s.Pool().QueryRow(ctx, `INSERT INTO rooms(id,snapshot,seq,invite_hash,created_at) VALUES('legacy-retention','{"room_id":"legacy-retention","seq":1,"phase":"finished"}',1,'x',now()-interval '100 days') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	before := time.Now()
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	info, err := s.Retention(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if info.LastActivityAt.Before(before) {
		t.Fatal("migration did not grant conservative activity")
	}
	if !info.ExpiresAt.Equal(info.CreatedAt.Add(90 * 24 * time.Hour)) {
		t.Fatal("migration reset hard creation limit")
	}
	var finished time.Time
	if err = s.Pool().QueryRow(ctx, `SELECT finished_at FROM rooms WHERE id=$1`, id).Scan(&finished); err != nil || finished.Before(before) {
		t.Fatalf("finished backfill %s %v", finished, err)
	}
}

func TestRoomExpiryFreesRoomLimitAndBoundsSweep(t *testing.T) {
	s, h, _ := room(t)
	ctx := context.Background()
	_, err := s.Pool().Exec(ctx, `INSERT INTO rooms(id,snapshot,seq,invite_hash)
 SELECT md5(n::text),jsonb_set(r.snapshot,'{room_id}',to_jsonb(md5(n::text))),r.seq,'fixture'
 FROM rooms r CROSS JOIN generate_series(1,999) n WHERE r.id=$1`, h.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Create(ctx, "At limit"); game.Code(err) != "room_limit" {
		t.Fatalf("limit: %v", err)
	}
	if _, err = s.Pool().Exec(ctx, `UPDATE rooms SET created_at=now()-interval '91 days' WHERE id=$1`, h.RoomID); err != nil {
		t.Fatal(err)
	}
	if deleted, err := s.RetireRoom(ctx, h.RoomID, time.Now()); !deleted || err != nil {
		t.Fatalf("retire: %v %v", deleted, err)
	}
	if _, err = s.Create(ctx, "Slot reused"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool().Exec(ctx, `UPDATE rooms SET created_at=now()-interval '91 days' WHERE id IN(SELECT id FROM rooms ORDER BY id LIMIT 60)`); err != nil {
		t.Fatal(err)
	}
	ids, err := s.ExpiredRooms(ctx, time.Now(), 1000)
	if err != nil || len(ids) != 50 {
		t.Fatalf("unbounded candidates %d %v", len(ids), err)
	}
}

func TestRoomAutomaticDeadlineDoesNotRefreshActivity(t *testing.T) {
	rules := game.DefaultRules()
	rules.BuzzerMode = "random"
	rules.WindowMS = 100
	s, h, p := v2Room(t, rules)
	ctx := context.Background()
	stage := v2Question(t, s, h)
	out := v2(t, s, h, "open-retention-window", "open_buzzer", game.StagePayload{StageID: stage.ID, InstanceID: stage.InstanceID})
	stage = out.State.Gameplay.Stage
	v2(t, s, p[0], "press-retention-window", "buzz", game.StagePayload{StageID: stage.ID, InstanceID: stage.InstanceID})
	old := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	if _, err := s.Pool().Exec(ctx, `UPDATE rooms SET last_activity_at=$2 WHERE id=$1`, h.RoomID, old); err != nil {
		t.Fatal(err)
	}
	time.Sleep(120 * time.Millisecond)
	next, err := s.Advance(ctx, h.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if next.Gameplay.Stage.Kind != "collect_answer" {
		t.Fatal("automatic deadline did not fire")
	}
	info, err := s.Retention(ctx, h.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if !info.LastActivityAt.Equal(old) {
		t.Fatal("automatic timer extended activity")
	}
}
