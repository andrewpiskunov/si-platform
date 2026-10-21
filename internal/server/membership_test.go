package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"siplatform/examples"
	"siplatform/internal/game"
	"siplatform/internal/library"
	"siplatform/internal/packs"
	"siplatform/internal/playpack"
	"siplatform/internal/storage/postgres"
)

func membershipRequest(t *testing.T, method, endpoint, token string, cookie *http.Cookie, body any, status int) ([]byte, []*http.Cookie) {
	t.Helper()
	var input io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			t.Fatal(e)
		}
		input = bytes.NewReader(b)
	}
	req, e := http.NewRequest(method, endpoint, input)
	if e != nil {
		t.Fatal(e)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	response, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	b, e := io.ReadAll(response.Body)
	if e != nil {
		t.Fatal(e)
	}
	if response.StatusCode != status {
		t.Fatalf("%s %s: status=%d want=%d body=%s", method, endpoint, response.StatusCode, status, b)
	}
	return b, response.Cookies()
}
func membershipCookie(t *testing.T, cookies []*http.Cookie, room string) *http.Cookie {
	t.Helper()
	for _, c := range cookies {
		if c.Name == "si_guest" {
			if len(c.Value) != 64 || c.Path != "/api/rooms/"+room || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.MaxAge <= 0 {
				t.Fatalf("invalid guest cookie attributes: path=%s httponly=%v samesite=%v maxage=%d", c.Path, c.HttpOnly, c.SameSite, c.MaxAge)
			}
			return c
		}
	}
	t.Fatal("missing guest cookie")
	return nil
}
func membershipRoom(t *testing.T) (*Server, *httptest.Server, postgres.Credentials, *http.Cookie) {
	t.Helper()
	app, h, auth := appearancePlatform(t)
	owner, e := auth.Register(context.Background(), "membership_author", "long-enough-password", "Author")
	if e != nil {
		t.Fatal(e)
	}
	b, cookies := membershipRequest(t, "POST", h.URL+"/api/rooms", owner.Token, nil, map[string]any{"host_name": "Host"}, 201)
	var host postgres.Credentials
	if e = json.Unmarshal(b, &host); e != nil {
		t.Fatal(e)
	}
	return app, h, host, membershipCookie(t, cookies, host.RoomID)
}
func membershipJoin(t *testing.T, base string, host postgres.Credentials, cookie *http.Cookie, name string) (postgres.Credentials, *http.Cookie) {
	t.Helper()
	b, cookies := membershipRequest(t, "POST", base+"/api/rooms/"+host.RoomID+"/participants", "", cookie, map[string]any{"name": name, "role": "player", "invite_token": host.InviteToken}, 201)
	var c postgres.Credentials
	if e := json.Unmarshal(b, &c); e != nil {
		t.Fatal(e)
	}
	return c, membershipCookie(t, cookies, host.RoomID)
}
func membershipWatch(t *testing.T, base string, c postgres.Credentials) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ws, _, e := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/api/rooms/"+c.RoomID+"/ws?appearance_only=1", nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { ws.CloseNow() })
	if e = wsjson.Write(ctx, ws, map[string]any{"v": 1, "type": "authenticate", "token": c.Token}); e != nil {
		t.Fatal(e)
	}
	var m Message
	if e = wsjson.Read(ctx, ws, &m); e != nil || m.Type != "appearance_refresh" {
		t.Fatalf("watch authenticate: %v %+v", e, m)
	}
	return ws
}
func membershipRevokedSocket(t *testing.T, ws *websocket.Conn, revokedSeq int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var m Message
	var e error
	for {
		e = wsjson.Read(ctx, ws, &m)
		if e != nil || m.State == nil || m.State.Seq >= revokedSeq {
			break
		}
	}
	if e != nil {
		if ctx.Err() != nil {
			t.Fatal("revoked connection remained open")
		}
		return
	}
	if m.Type != "error" || m.Code != "unauthorized" || m.State != nil {
		t.Fatalf("revoked socket delivered %+v", m)
	}
}
func TestMembershipHTTPKickRevokesSocketsAndPrivateReads(t *testing.T) {
	app, h, host, _ := membershipRoom(t)
	player, cookie := membershipJoin(t, h.URL, host, nil, "Player")
	hc, _ := connect(t, h.URL, host)
	pc, _ := connect(t, h.URL, player)
	watch := membershipWatch(t, h.URL, player)
	path := h.URL + "/api/rooms/" + host.RoomID
	for _, suffix := range []string{"/board", "/results", "/appearance", "/roster"} {
		membershipRequest(t, "GET", path+suffix, player.Token, nil, nil, 200)
	}
	removed := sendV2(t, app, hc, host, "kick", "remove_participant", game.RemovalPayload{TargetID: player.ParticipantID, Mode: "kick"})
	membershipRevokedSocket(t, pc, removed.ResultSeq)
	membershipRevokedSocket(t, watch, removed.ResultSeq)
	for _, suffix := range []string{"/board", "/results", "/appearance", "/roster"} {
		membershipRequest(t, "GET", path+suffix, player.Token, nil, nil, 401)
	}
	_, first := connect(t, h.URL, player)
	if first.Code != "unauthorized" || first.State != nil {
		t.Fatalf("revoked authentication: %+v", first)
	}
	membershipRequest(t, "POST", path+"/session", player.Token, cookie, nil, 401)
	again, _ := membershipJoin(t, h.URL, host, cookie, "Returned")
	if again.ParticipantID == player.ParticipantID {
		t.Fatal("kick rejoin reused historical identity")
	}
	state, e := app.store.Load(context.Background(), host.RoomID)
	if e != nil {
		t.Fatal(e)
	}
	p, ok := state.ActiveParticipant(again.ParticipantID)
	if !ok || p.Score != 0 {
		t.Fatal("rejoin did not get fresh seat and score")
	}
}
func TestMembershipHTTPBanGroupAndCookieCannotAuthorize(t *testing.T) {
	app, h, host, _ := membershipRoom(t)
	first, cookie := membershipJoin(t, h.URL, host, nil, "First tab")
	second, _ := membershipJoin(t, h.URL, host, cookie, "Second tab")
	hc, _ := connect(t, h.URL, host)
	one, _ := connect(t, h.URL, first)
	two, _ := connect(t, h.URL, second)
	path := h.URL + "/api/rooms/" + host.RoomID
	membershipRequest(t, "GET", path+"/board", "", cookie, nil, 401)
	removed := sendV2(t, app, hc, host, "ban", "remove_participant", game.RemovalPayload{TargetID: first.ParticipantID, Mode: "ban_guest_session"})
	membershipRevokedSocket(t, one, removed.ResultSeq)
	membershipRevokedSocket(t, two, removed.ResultSeq)
	b, _ := membershipRequest(t, "POST", path+"/participants", "", cookie, map[string]any{"name": "Again", "role": "player", "invite_token": host.InviteToken}, 403)
	if !bytes.Contains(b, []byte("guest_banned")) {
		t.Fatalf("wrong ban response: %s", b)
	}
	for _, c := range []postgres.Credentials{first, second} {
		membershipRequest(t, "GET", path+"/results", c.Token, nil, nil, 401)
	}
	membershipJoin(t, h.URL, host, nil, "Different browser")
}
func TestMembershipHTTPBanHostGroupIsAtomic(t *testing.T) {
	app, h, host, cookie := membershipRoom(t)
	player, _ := membershipJoin(t, h.URL, host, cookie, "Host second tab")
	hc, _ := connect(t, h.URL, host)
	state, e := app.store.Load(context.Background(), host.RoomID)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(game.RemovalPayload{TargetID: player.ParticipantID, Mode: "ban_guest_session"})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if e = wsjson.Write(ctx, hc, game.Command{Version: 2, ID: "protect-host", Type: "remove_participant", ExpectedSeq: &state.Seq, Data: raw}); e != nil {
		t.Fatal(e)
	}
	m := result(t, hc, "protect-host")
	if m.Code != "forbidden" {
		t.Fatalf("host group not rejected: %+v", m)
	}
	for _, c := range []postgres.Credentials{host, player} {
		membershipRequest(t, "GET", h.URL+"/api/rooms/"+host.RoomID+"/board", c.Token, nil, nil, 200)
	}
	membershipJoin(t, h.URL, host, cookie, "Still allowed")
}
func TestMembershipHTTPBootstrapBindsOnce(t *testing.T) {
	app, h, host, _ := membershipRoom(t)
	legacy, e := app.store.Join(context.Background(), host.RoomID, host.InviteToken, "Legacy", game.Player)
	if e != nil {
		t.Fatal(e)
	}
	path := h.URL + "/api/rooms/" + host.RoomID + "/session"
	_, cookies := membershipRequest(t, "POST", path, legacy.Token, nil, map[string]any{}, 204)
	cookie := membershipCookie(t, cookies, host.RoomID)
	membershipRequest(t, "POST", path, legacy.Token, cookie, map[string]any{}, 204)
	wrong := *cookie
	wrong.Value = strings.Repeat("f", 64)
	b, _ := membershipRequest(t, "POST", path, legacy.Token, &wrong, map[string]any{}, 409)
	if !bytes.Contains(b, []byte("guest_session_mismatch")) {
		t.Fatalf("wrong bind error: %s", b)
	}
	membershipRequest(t, "POST", path, "", cookie, nil, 401)
}

func TestMembershipHTTPLegacyCreationIssuesGuestCookie(t *testing.T) {
	_, h, _ := setup(t)
	b, cookies := membershipRequest(t, "POST", h.URL+"/api/rooms", "", nil, map[string]any{"host_name": "Legacy host"}, 201)
	var host postgres.Credentials
	if e := json.Unmarshal(b, &host); e != nil {
		t.Fatal(e)
	}
	cookie := membershipCookie(t, cookies, host.RoomID)
	membershipRequest(t, "POST", h.URL+"/api/rooms/"+host.RoomID+"/session", host.Token, cookie, map[string]any{}, 204)
}
func TestMembershipHTTPRosterPagesRetainRemovedNames(t *testing.T) {
	app, h, host, _ := membershipRoom(t)
	removed, _ := membershipJoin(t, h.URL, host, nil, "Historical name")
	membershipJoin(t, h.URL, host, nil, "Active name")
	hc, _ := connect(t, h.URL, host)
	sendV2(t, app, hc, host, "kick-roster", "remove_participant", game.RemovalPayload{TargetID: removed.ParticipantID, Mode: "kick"})
	endpoint := h.URL + "/api/rooms/" + host.RoomID + "/roster?limit=1"
	seen := map[string]game.Participant{}
	cursor := ""
	for i := 0; i < 4; i++ {
		path := endpoint
		if cursor != "" {
			path += "&after=" + cursor
		}
		b, _ := membershipRequest(t, "GET", path, host.Token, nil, nil, 200)
		if bytes.Contains(b, []byte("guest")) || bytes.Contains(b, []byte("token")) {
			t.Fatal("roster exposed session identity")
		}
		var page struct {
			Items []game.Participant `json:"items"`
			Next  string             `json:"next_after"`
		}
		if e := json.Unmarshal(b, &page); e != nil {
			t.Fatal(e)
		}
		if len(page.Items) > 1 {
			t.Fatal("unbounded page")
		}
		for _, p := range page.Items {
			if _, ok := seen[p.ID]; ok {
				t.Fatal("repeated roster identity")
			}
			seen[p.ID] = p
		}
		if page.Next == "" {
			break
		}
		cursor = page.Next
	}
	if len(seen) != 3 || !seen[removed.ParticipantID].Removed || seen[removed.ParticipantID].Name != "Historical name" {
		t.Fatal("historical roster missing", seen)
	}
	membershipRequest(t, "GET", endpoint+"&after=NOT-A-CURSOR", host.Token, nil, nil, 400)
	membershipRequest(t, "GET", h.URL+"/api/rooms/"+host.RoomID+"/roster?limit=51", host.Token, nil, nil, 400)
}

func TestMembershipHTTPRevokesIssuedMediaTicketsAndThemeAssets(t *testing.T) {
	app, h, auth := appearancePlatform(t)
	ctx := context.Background()
	owner, e := auth.Register(ctx, "membership_media", "long-enough-password", "Author")
	if e != nil {
		t.Fatal(e)
	}
	actor, e := auth.Authenticate(ctx, owner.Token)
	if e != nil {
		t.Fatal(e)
	}
	theme, _ := appearanceFixture(t, app, actor)
	appearance := game.Appearance{ThemeID: theme.Card.ID, Revision: 1, Hash: theme.Prepared.Hash}
	asset, _ := mediaImage(t, app, "membership.png", 40)
	doc := examples.Demo()
	q := &doc.Rounds[0].Themes[0].Questions[0]
	q.Params[0].Items = []packs.Content{{Type: "image", Value: "membership.png", IsRef: true, WaitForFinish: true}}
	draft, e := app.options.Library.Create(ctx, actor, library.Save{Document: doc, Assets: []library.Asset{asset}})
	if e != nil {
		t.Fatal(e)
	}
	compiled, e := playpack.CompileMedia(doc, q.ID, library.MediaLookup(draft.Assets))
	if e != nil {
		t.Fatal(e)
	}
	mediaID := compiled.Question[0].Media.ID
	var host postgres.Credentials
	if e = json.Unmarshal(api(t, "POST", h.URL+"/api/rooms", owner.Token, map[string]any{"host_name": "Host", "pack_id": draft.Card.ID, "revision": 1, "appearance": appearance}, 201), &host); e != nil {
		t.Fatal(e)
	}
	player := join(t, h.URL, host, "Player")
	join(t, h.URL, host, "Other")
	hc, initial := connect(t, h.URL, host)
	path := h.URL + "/api/rooms/" + host.RoomID
	api(t, "POST", path+"/round", host.Token, map[string]any{"round_id": doc.Rounds[0].ID, "command_id": "start", "expected_seq": initial.State.Seq}, 200)
	state, e := app.store.Load(ctx, host.RoomID)
	if e != nil {
		t.Fatal(e)
	}
	api(t, "POST", path+"/select", host.Token, map[string]any{"question_id": q.ID, "command_id": "select", "expected_seq": state.Seq}, 200)
	var ticket struct {
		URL string `json:"url"`
	}
	if e = json.Unmarshal(api(t, "POST", path+"/media-ticket", player.Token, map[string]any{"media_id": mediaID, "quality": "economy"}, 200), &ticket); e != nil {
		t.Fatal(e)
	}
	mediaURL := h.URL + ticket.URL
	themeURL := path + "/appearance/assets/0?quality=standard&hash=" + appearance.Hash
	api(t, "GET", mediaURL, "", nil, 200)
	api(t, "GET", themeURL, player.Token, nil, 200)
	sendV2(t, app, hc, host, "media-kick", "remove_participant", game.RemovalPayload{TargetID: player.ParticipantID, Mode: "kick"})
	api(t, "POST", path+"/media-ticket", player.Token, map[string]any{"media_id": mediaID, "quality": "economy"}, 401)
	for _, method := range []string{"GET", "HEAD"} {
		for _, target := range []struct{ url, token string }{{mediaURL, ""}, {themeURL, player.Token}} {
			req, e := http.NewRequest(method, target.url, nil)
			if e != nil {
				t.Fatal(e)
			}
			req.Header.Set("Range", "bytes=0-7")
			if target.token != "" {
				req.Header.Set("Authorization", "Bearer "+target.token)
			}
			response, e := http.DefaultClient.Do(req)
			if e != nil {
				t.Fatal(e)
			}
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			want := 401
			if target.token == "" {
				want = 404
			} // Ticket visibility uses the existing not-found response.
			if response.StatusCode != want {
				t.Fatalf("revoked %s asset: %d want %d", method, response.StatusCode, want)
			}
		}
	}
}
