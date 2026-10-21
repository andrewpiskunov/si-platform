package server

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"siplatform/internal/accounts"
	"siplatform/internal/blob"
	"siplatform/internal/game"
	"siplatform/internal/library"
	"siplatform/internal/storage/postgres"
	"siplatform/internal/testutil"
	"siplatform/internal/themes"
)

func appearancePlatform(t *testing.T) (*Server, *httptest.Server, *accounts.Service) {
	t.Helper()
	pool := testutil.Pool(t)
	if err := postgres.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	objects, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	auth := accounts.New(pool, accounts.Options{Origin: "http://trusted.example"})
	app := New(postgres.New(pool), Options{Origin: "http://trusted.example", Accounts: auth, Library: library.New(pool), Objects: objects, Themes: themes.New(pool)})
	h := httptest.NewServer(app.Handler())
	t.Cleanup(func() { app.Close(); h.Close() })
	return app, h, auth
}
func appearanceFixture(t *testing.T, app *Server, actor accounts.Identity) (themes.Version, []byte) {
	t.Helper()
	manifest := themes.Manifest{SchemaVersion: 1, Name: "Тестовая тема", Author: "Автор", Provenance: "Собственные изображения", Font: "sans", Cards: themes.Shape{Radius: 12, BorderWidth: 1}, Buttons: themes.Shape{Radius: 8, BorderWidth: 1}, Background: "assets/bg.png", Palette: themes.Palette{Background: "#101725", Surface: "#192234", SurfaceAlt: "#202D43", Border: "#AAB6CA", Text: "#FFFFFF", Muted: "#EEEEEE", Accent: "#EFBC6E", AccentText: "#000000", Danger: "#FFA2A5", Success: "#91DBBC", Focus: "#FFFFFF"}}
	asset, _ := mediaImage(t, app, "theme.png", 30)
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	w, err := zw.Create("theme.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.NewEncoder(w).Encode(manifest); err != nil {
		t.Fatal(err)
	}
	if err = zw.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(archive.Bytes())
	key := hex.EncodeToString(sum[:])
	if err = app.options.Objects.Put(context.Background(), key, bytes.NewReader(archive.Bytes()), int64(archive.Len()), "application/zip"); err != nil {
		t.Fatal(err)
	}
	p := themes.Prepared{Manifest: manifest, Assets: []themes.Asset{{Name: "assets/bg.png", Prepared: *asset.Prepared}}, OriginalKey: key, OriginalSize: int64(archive.Len()), SizeBytes: int64(archive.Len()) + asset.Prepared.Standard.Size + asset.Prepared.Economy.Size}
	raw, _ := json.Marshal(struct {
		Manifest themes.Manifest `json:"manifest"`
		Assets   []themes.Asset  `json:"assets"`
	}{p.Manifest, p.Assets})
	sum = sha256.Sum256(raw)
	p.Hash = hex.EncodeToString(sum[:])
	if err = p.Validate(); err != nil {
		t.Fatal(err, themes.Issues(err))
	}
	raw, _ = json.Marshal(p)
	upload := strings.Repeat("e", 32)
	_, err = app.store.Pool().Exec(context.Background(), `INSERT INTO uploads(id,owner_id,credential_id,name,size,offset_bytes,status,purpose,result_theme) VALUES($1,$2,$3,'theme.zip',$4,$4,'ready','theme',$5)`, upload, actor.User.ID, actor.SessionID, p.OriginalSize, raw)
	if err != nil {
		t.Fatal(err)
	}
	v, err := app.options.Themes.Attach(context.Background(), actor, "", 0, upload)
	if err != nil {
		t.Fatal(err)
	}
	return v, archive.Bytes()
}
func roomAppearanceRequest(t *testing.T, method, url, roomToken, accountToken string, body any, want int) []byte {
	t.Helper()
	raw, _ := json.Marshal(body)
	r, err := http.NewRequest(method, url, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	if roomToken != "" {
		r.Header.Set("Authorization", "Bearer "+roomToken)
	}
	if accountToken != "" {
		r.AddCookie(&http.Cookie{Name: "si_session", Value: accountToken})
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != want {
		t.Fatalf("%s %s: %d want%d: %s", method, url, resp.StatusCode, want, data)
	}
	return data
}

func TestAppearanceHTTPPrivatePinAndFallback(t *testing.T) {
	app, h, auth := appearancePlatform(t)
	ctx := context.Background()
	owner, err := auth.Register(ctx, "theme_author", "long-enough-password", "Автор")
	if err != nil {
		t.Fatal(err)
	}
	other, err := auth.Register(ctx, "theme_other", "long-enough-password", "Другой")
	if err != nil {
		t.Fatal(err)
	}
	actor, err := auth.Authenticate(ctx, owner.Token)
	if err != nil {
		t.Fatal(err)
	}
	v, original := appearanceFixture(t, app, actor)
	a := game.Appearance{ThemeID: v.Card.ID, Revision: 1, Hash: v.Prepared.Hash}
	api(t, "GET", h.URL+"/api/themes", "", nil, 401)
	api(t, "GET", h.URL+"/api/themes/"+v.Card.ID, other.Token, nil, 404)
	api(t, "GET", h.URL+"/api/themes/"+v.Card.ID, owner.Token, nil, 200)
	archive := api(t, "GET", h.URL+"/api/themes/"+v.Card.ID+"/archive?revision=1", owner.Token, nil, 200)
	if !bytes.Equal(archive, original) {
		t.Fatal("original theme archive changed")
	}
	api(t, "GET", h.URL+"/api/themes/"+v.Card.ID+"/assets/0?revision=1&quality=economy", owner.Token, nil, 200)
	var host postgres.Credentials
	if err = json.Unmarshal(api(t, "POST", h.URL+"/api/rooms", owner.Token, map[string]any{"host_name": "Host", "appearance": a}, 201), &host); err != nil {
		t.Fatal(err)
	}
	player := join(t, h.URL, host, "Игрок")
	hc, initial := connect(t, h.URL, host)
	pc, _ := connect(t, h.URL, player)
	path := h.URL + "/api/rooms/" + host.RoomID + "/appearance"
	api(t, "GET", path, "", nil, 401)
	got := api(t, "GET", path, player.Token, nil, 200)
	if !bytes.Contains(got, []byte(`"status":"ready"`)) || bytes.Contains(got, []byte(v.Prepared.OriginalKey)) || bytes.Contains(got, []byte(v.Prepared.Assets[0].Prepared.Standard.Key)) {
		t.Fatal("projection missing or leaks keys", string(got))
	}
	assetURL := path + "/assets/0?quality=standard&hash=" + a.Hash
	api(t, "GET", assetURL, player.Token, nil, 200)
	api(t, "GET", strings.Replace(assetURL, a.Hash, strings.Repeat("0", 64), 1), player.Token, nil, 404)
	api(t, "GET", assetURL, owner.Token, nil, 401)
	api(t, "DELETE", h.URL+"/api/themes/"+v.Card.ID, owner.Token, nil, 200)
	api(t, "GET", assetURL, player.Token, nil, 200) // library deletion preserves pinned play.
	builtin := map[string]any{"appearance": game.Appearance{Builtin: "pirate"}, "command_id": "builtin", "expected_seq": initial.State.Seq}
	roomAppearanceRequest(t, "POST", path, player.Token, "", builtin, 403)
	roomAppearanceRequest(t, "POST", path, host.Token, "", builtin, 200)
	readUntil(t, pc, func(m Message) bool {
		return m.State != nil && m.State.Appearance != nil && m.State.Appearance.Builtin == "pirate"
	})
	state := readUntil(t, hc, func(m Message) bool {
		return m.State != nil && m.State.Appearance != nil && m.State.Appearance.Builtin == "pirate"
	}).State
	api(t, "GET", assetURL, player.Token, nil, 404)
	_, err = app.store.Pool().Exec(ctx, "UPDATE themes SET deleted_at=NULL WHERE id=$1", v.Card.ID)
	if err != nil {
		t.Fatal(err)
	}
	custom := map[string]any{"appearance": a, "command_id": "custom", "expected_seq": state.Seq}
	roomAppearanceRequest(t, "POST", path, host.Token, "", custom, 401)
	roomAppearanceRequest(t, "POST", path, host.Token, other.Token, custom, 403)
	roomAppearanceRequest(t, "POST", path, host.Token, owner.Token, custom, 200)
	readUntil(t, pc, func(m Message) bool {
		return m.State != nil && m.State.Appearance != nil && m.State.Appearance.ThemeID == a.ThemeID
	})
	if err = app.refreshAppearances(ctx); err != nil {
		t.Fatal(err)
	}
	readUntil(t, pc, func(m Message) bool { return m.Type == "appearance_refresh" })
	_, err = app.store.Pool().Exec(ctx, "UPDATE account_users SET role='moderator' WHERE id=$1", actor.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	api(t, "POST", h.URL+"/api/themes/"+v.Card.ID+"/block", owner.Token, map[string]any{"blocked": true, "reason": "Проверка отзыва"}, 200)
	api(t, "GET", assetURL, player.Token, nil, 403)
	got = api(t, "GET", path, player.Token, nil, 200)
	if !bytes.Contains(got, []byte(`"status":"unavailable"`)) || bytes.Contains(got, []byte(`"manifest"`)) {
		t.Fatal("blocked theme did not fall back", string(got))
	}
	if err = app.refreshAppearances(ctx); err != nil {
		t.Fatal(err)
	}
	readUntil(t, pc, func(m Message) bool { return m.Type == "appearance_refresh" })
	api(t, "POST", h.URL+"/api/themes/"+v.Card.ID+"/block", owner.Token, map[string]any{"blocked": false, "reason": "Проверка восстановления"}, 200)
	if err = app.refreshAppearances(ctx); err != nil {
		t.Fatal(err)
	}
	readUntil(t, pc, func(m Message) bool { return m.Type == "appearance_refresh" })
	api(t, "GET", assetURL, player.Token, nil, 200)
	api(t, "DELETE", h.URL+"/api/account/me", owner.Token, map[string]any{"password": "long-enough-password"}, 200)
	if err = app.refreshAppearances(ctx); err != nil {
		t.Fatal(err)
	}
	readUntil(t, pc, func(m Message) bool { return m.Type == "appearance_refresh" })
	api(t, "GET", assetURL, player.Token, nil, 404)
	got = api(t, "GET", path, player.Token, nil, 200)
	if !bytes.Contains(got, []byte(`"status":"unavailable"`)) {
		t.Fatal("deleted owner still serves theme")
	}
}

func TestAppearanceWatchCannotPlayOrKeepHostOnline(t *testing.T) {
	app, h, _ := setup(t)
	host := create(t, h.URL)
	player := join(t, h.URL, host, "Игрок")
	join(t, h.URL, host, "Второй")
	hc, _ := connect(t, h.URL, host)
	pc, _ := connect(t, h.URL, player)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	watch, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(h.URL, "http")+"/api/rooms/"+host.RoomID+"/ws?appearance_only=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer watch.CloseNow()
	if err = wsjson.Write(ctx, watch, map[string]any{"v": 1, "type": "authenticate", "token": host.Token}); err != nil {
		t.Fatal(err)
	}
	var first Message
	if err = wsjson.Read(ctx, watch, &first); err != nil {
		t.Fatal(err)
	}
	if first.Type != "appearance_refresh" || first.State != nil {
		t.Fatalf("watch exposed game state: %+v", first)
	}
	write(t, hc, game.Command{ID: "open", Type: "open_question", QuestionID: "question", Text: "2+2", Answer: "4", Value: 100})
	if m := result(t, hc, "open"); m.Code != "ok" {
		t.Fatal(m.Code)
	}
	write(t, watch, game.Command{ID: "watch-pause", Type: "pause"})
	// A game broadcast would be ahead of the rejection and expose a snapshot.
	if err = wsjson.Read(ctx, watch, &first); err != nil {
		t.Fatal(err)
	}
	if first.Type != "error" || first.CommandID != "watch-pause" || first.Code != "forbidden" || first.State != nil {
		t.Fatalf("watch accepted command/broadcast: %+v", first)
	}
	_ = hc.CloseNow()
	readUntil(t, pc, func(m Message) bool { return m.State != nil && m.State.Phase == game.Paused })
	state, err := app.store.Load(ctx, host.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != game.Paused {
		t.Fatal("appearance watch kept host online")
	}
}
