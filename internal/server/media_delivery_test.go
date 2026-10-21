package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"siplatform/examples"
	"siplatform/internal/game"
	"siplatform/internal/library"
	"siplatform/internal/media"
	"siplatform/internal/packs"
	"siplatform/internal/playpack"
	"siplatform/internal/storage/postgres"
)

func mediaImage(t *testing.T, app *Server, name string, shade uint8) (library.Asset, []byte) {
	t.Helper()
	put := func(side int, shade uint8) (media.Variant, []byte) {
		img := image.NewRGBA(image.Rect(0, 0, side, side))
		for y := 0; y < side; y++ {
			for x := 0; x < side; x++ {
				img.SetRGBA(x, y, color.RGBA{shade, 20, 30, 255})
			}
		}
		var data bytes.Buffer
		if err := png.Encode(&data, img); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data.Bytes())
		key := hex.EncodeToString(sum[:])
		if err := app.options.Objects.Put(context.Background(), key, bytes.NewReader(data.Bytes()), int64(data.Len()), "image/png"); err != nil {
			t.Fatal(err)
		}
		return media.Variant{Key: key, MIME: "image/png", Size: int64(data.Len())}, data.Bytes()
	}
	original, _ := put(16, shade)
	standard, _ := put(16, shade+1)
	economy, data := put(8, shade+1)
	return library.Asset{Name: "Images/" + name, Key: original.Key, Size: original.Size, ContentType: original.MIME, Prepared: &media.Prepared{Kind: "image", Width: 16, Height: 16, Standard: standard, Economy: economy}}, data
}

func TestRoomMediaOnlyDeliversCurrentPinnedProjection(t *testing.T) {
	app, h, auth := platform(t)
	ctx := context.Background()
	owner, err := auth.Register(ctx, "media_author", "long-enough-password", "Author")
	if err != nil {
		t.Fatal(err)
	}
	other, err := auth.Register(ctx, "media_other", "long-enough-password", "Other")
	if err != nil {
		t.Fatal(err)
	}
	actor, err := auth.Authenticate(ctx, owner.Token)
	if err != nil {
		t.Fatal(err)
	}
	questionAsset, economy := mediaImage(t, app, "PRIVATE-QUESTION.png", 40)
	answerAsset, _ := mediaImage(t, app, "PRIVATE-ANSWER.png", 80)
	doc := examples.Demo()
	q := &doc.Rounds[0].Themes[0].Questions[0]
	q.Params[0].Items = []packs.Content{{Type: "image", Value: "PRIVATE-QUESTION.png", IsRef: true, WaitForFinish: true}, {Type: "text", Value: "Следующий фрагмент", WaitForFinish: true}}
	q.Params = append(q.Params, packs.Parameter{Name: "answer", Type: "content", Items: []packs.Content{{Type: "image", Value: "PRIVATE-ANSWER.png", IsRef: true, WaitForFinish: true}}})
	draft, err := app.options.Library.Create(ctx, actor, library.Save{Document: doc, Assets: []library.Asset{questionAsset, answerAsset}})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := playpack.CompileMedia(doc, q.ID, library.MediaLookup(draft.Assets))
	if err != nil {
		t.Fatal(err)
	}
	mediaID, answerID := compiled.Question[0].Media.ID, compiled.Answer[0].Media.ID
	preview := h.URL + "/api/drafts/" + draft.Card.ID + "/prepared/" + questionAsset.Prepared.Standard.Key + "?revision=1"
	api(t, "GET", preview, "", nil, 401)
	api(t, "GET", preview, other.Token, nil, 404)
	api(t, "GET", preview, owner.Token, nil, 200)
	api(t, "GET", strings.Replace(preview, questionAsset.Prepared.Standard.Key, questionAsset.Key, 1), owner.Token, nil, 404)
	api(t, "GET", strings.Replace(preview, "revision=1", "revision=999", 1), owner.Token, nil, 404)
	var host postgres.Credentials
	json.Unmarshal(api(t, "POST", h.URL+"/api/rooms", owner.Token, map[string]any{"host_name": "Host", "pack_id": draft.Card.ID, "revision": 1}, 201), &host)
	player := join(t, h.URL, host, "Player")
	otherPlayer := join(t, h.URL, host, "Other")
	hc, initial := connect(t, h.URL, host)
	pc, _ := connect(t, h.URL, player)
	oc, _ := connect(t, h.URL, otherPlayer)
	path := h.URL + "/api/rooms/" + host.RoomID
	ticket := func(id, token string, want int) string {
		raw := api(t, "POST", path+"/media-ticket", token, map[string]any{"media_id": id, "quality": "economy"}, want)
		if want != 200 {
			return ""
		}
		var out struct {
			URL     string `json:"url"`
			Expires int64  `json:"expires_at"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		if out.URL == "" || out.Expires <= time.Now().Unix() || strings.Contains(out.URL, "PRIVATE") || strings.Contains(out.URL, questionAsset.Key) {
			t.Fatalf("unsafe ticket response: %s", raw)
		}
		return h.URL + out.URL
	}
	ticket(mediaID, player.Token, 404)
	api(t, "POST", path+"/round", host.Token, map[string]any{"round_id": doc.Rounds[0].ID, "command_id": "start", "expected_seq": initial.State.Seq}, 200)
	state, _ := app.store.Load(ctx, host.RoomID)
	api(t, "POST", path+"/select", host.Token, map[string]any{"question_id": q.ID, "command_id": "choose", "expected_seq": state.Seq}, 200)
	prepared := readUntil(t, pc, func(m Message) bool {
		return m.State != nil && m.State.Gameplay != nil && m.State.Gameplay.Question != nil && m.State.Gameplay.Question.Media != nil
	})
	if prepared.State.Gameplay.Stage.Kind != "prepare_content" {
		t.Fatal(prepared.State.Gameplay.Stage)
	}
	ticket(mediaID, "", 401)
	ticket(mediaID, other.Token, 401)
	ticket(answerID, player.Token, 404)
	ticket(answerID, host.Token, 404)
	api(t, "POST", path+"/media-ticket", player.Token, map[string]any{"media_id": mediaID, "quality": "original"}, 400)
	mediaURL := ticket(mediaID, player.Token, 200)
	req, _ := http.NewRequest("GET", mediaURL, nil)
	req.Header.Set("Range", "bytes=1-8")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 206 || !bytes.Equal(data, economy[1:9]) || resp.Header.Get("Content-Type") != "image/png" || resp.Header.Get("Cache-Control") != "private, no-store" || !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "inline") {
		t.Fatalf("range/headers: %d %+v %q", resp.StatusCode, resp.Header, data)
	}
	api(t, "GET", strings.Replace(mediaURL, "/media/"+mediaID, "/media/"+answerID, 1), "", nil, 401)
	api(t, "GET", strings.Replace(mediaURL, host.RoomID, strings.Repeat("a", 32), 1), "", nil, 401)
	u, _ := url.Parse(mediaURL)
	values := u.Query()
	rawTicket := values.Get("ticket")
	claims, err := app.readMediaTicket(rawTicket, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	claims.Instance = "stale-instance"
	stale, err := app.signMediaTicket(claims)
	if err != nil {
		t.Fatal(err)
	}
	values.Set("ticket", stale)
	u.RawQuery = values.Encode()
	api(t, "GET", u.String(), "", nil, 404)
	api(t, "GET", mediaURL+"X", "", nil, 401)
	// Changing a future draft does not change a room's immutable file authority.
	updated := doc
	updated.Name = "Edited after room pin"
	if _, err = app.options.Library.UpdateDocument(ctx, actor, draft.Card.ID, 1, updated); err != nil {
		t.Fatal(err)
	}
	api(t, "GET", mediaURL, "", nil, 200)
	// Blocking a package revokes even a correctly signed, already issued ticket.
	if _, err = app.store.Pool().Exec(ctx, "UPDATE library_packs SET blocked=true WHERE id=$1", draft.Card.ID); err != nil {
		t.Fatal(err)
	}
	api(t, "GET", mediaURL, "", nil, 403)
	if _, err = app.store.Pool().Exec(ctx, "UPDATE library_packs SET blocked=false WHERE id=$1", draft.Card.ID); err != nil {
		t.Fatal(err)
	}
	stage := prepared.State.Gameplay.Stage
	sendV2(t, app, pc, player, "ready1", "media_ready", game.MediaReadyPayload{StageID: stage.ID, InstanceID: stage.InstanceID, Status: "ready"})
	sendV2(t, app, oc, otherPlayer, "ready2", "media_ready", game.MediaReadyPayload{StageID: stage.ID, InstanceID: stage.InstanceID, Status: "ready"})
	presenting := readUntil(t, pc, func(m Message) bool {
		return m.State != nil && m.State.Gameplay != nil && m.State.Gameplay.Stage.Kind == "present_question"
	})
	stage = presenting.State.Gameplay.Stage
	advanced := sendV2(t, app, hc, host, "advance", "advance", game.StagePayload{StageID: stage.ID, InstanceID: stage.InstanceID})
	api(t, "GET", mediaURL, "", nil, 404)
	stage = advanced.State.Gameplay.Stage
	opened := sendV2(t, app, hc, host, "open", "open_buzzer", game.StagePayload{StageID: stage.ID, InstanceID: stage.InstanceID})
	stage = opened.State.Gameplay.Stage
	sendV2(t, app, hc, host, "reveal", "reveal", game.RevealPayload{StageID: stage.ID, InstanceID: stage.InstanceID})
	answerURL := ticket(answerID, player.Token, 200)
	api(t, "GET", answerURL, "", nil, 200)
	sendV2(t, app, hc, host, "finish", "finish", game.EmptyPayload{})
	api(t, "GET", answerURL, "", nil, 404)
}
