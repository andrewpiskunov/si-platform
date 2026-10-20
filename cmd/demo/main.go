// Demo exercises a real running server with twelve WebSocket players.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"siplatform/internal/game"
	"siplatform/internal/server"
	"siplatform/internal/storage/postgres"
)

func main() {
	url := flag.String("url", "http://127.0.0.1:8080", "server URL")
	flag.Parse()
	if err := run(strings.TrimRight(*url, "/")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func post(ctx context.Context, url string, body any) (postgres.Credentials, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return postgres.Credentials{}, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(b))
	if err != nil {
		return postgres.Credentials{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return postgres.Credentials{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		return postgres.Credentials{}, fmt.Errorf("HTTP status %d", resp.StatusCode)
	}
	var c postgres.Credentials
	err = json.NewDecoder(resp.Body).Decode(&c)
	return c, err
}
func connect(ctx context.Context, url string, c postgres.Credentials) (*websocket.Conn, server.Message, error) {
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(url, "http")+"/api/rooms/"+c.RoomID+"/ws", nil)
	if err != nil {
		return nil, server.Message{}, err
	}
	err = wsjson.Write(ctx, ws, map[string]any{"v": 1, "type": "authenticate", "token": c.Token})
	var m server.Message
	if err == nil {
		err = wsjson.Read(ctx, ws, &m)
	}
	if err == nil && m.State == nil {
		err = fmt.Errorf("authentication failed: %s", m.Code)
	}
	if err != nil {
		ws.CloseNow()
		return nil, m, err
	}
	return ws, m, nil
}
func result(ctx context.Context, ws *websocket.Conn, id string) (server.Message, error) {
	for {
		var m server.Message
		if err := wsjson.Read(ctx, ws, &m); err != nil {
			return m, err
		}
		if m.Type == "error" {
			return m, fmt.Errorf("command %s: %s", m.CommandID, m.Code)
		}
		if m.Type == "result" && m.CommandID == id {
			return m, nil
		}
	}
}
func send(ctx context.Context, ws *websocket.Conn, cmd game.Command) (server.Message, error) {
	cmd.Version = 2
	if err := wsjson.Write(ctx, ws, cmd); err != nil {
		return server.Message{}, err
	}
	return result(ctx, ws, cmd.ID)
}
func run(base string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var entropy [16]byte
	_, _ = rand.Read(entropy[:])
	password := hex.EncodeToString(entropy[:])
	var account struct {
		Token string `json:"token"`
	}
	if err := request(ctx, "POST", base+"/api/account/register", "", map[string]string{"username": "demo_" + password[:16], "password": password, "display_name": "Демонстрация"}, 201, &account); err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = request(cleanup, "DELETE", base+"/api/account/me", account.Token, map[string]string{"password": password}, 200, nil)
	}()
	var host postgres.Credentials
	err := request(ctx, "POST", base+"/api/rooms", account.Token, map[string]string{"host_name": "Демо-ведущий", "title": "Проверка 12 игроков", "theme": "classic"}, 201, &host)
	if err != nil {
		return err
	}
	var people []postgres.Credentials
	for i := 0; i < 12; i++ {
		p, err := post(ctx, base+"/api/rooms/"+host.RoomID+"/participants", map[string]string{"name": fmt.Sprintf("Игрок %d", i+1), "role": "player", "invite_token": host.InviteToken})
		if err != nil {
			return err
		}
		people = append(people, p)
	}
	hws, hostSnapshot, err := connect(ctx, base, host)
	if err != nil {
		return err
	}
	defer hws.CloseNow()
	var clients []*websocket.Conn
	for _, p := range people {
		ws, _, err := connect(ctx, base, p)
		if err != nil {
			return err
		}
		defer ws.CloseNow()
		clients = append(clients, ws)
	}
	if err = request(ctx, "POST", base+"/api/rooms/"+host.RoomID+"/round", host.Token, map[string]any{"round_id": "r1", "command_id": "round", "expected_seq": hostSnapshot.State.Seq}, 200, nil); err != nil {
		return err
	}
	round, err := awaitStage(ctx, hws, "select_question")
	if err != nil {
		return err
	}
	if err = request(ctx, "POST", base+"/api/rooms/"+host.RoomID+"/select", host.Token, map[string]any{"question_id": "r1-t1-q1", "command_id": "open", "expected_seq": round.State.Seq}, 200, nil); err != nil {
		return err
	}
	present, err := awaitStage(ctx, hws, "present_question")
	if err != nil {
		return err
	}
	opened, err := send(ctx, hws, game.Command{ID: "open-buzzer", Type: "open_buzzer", ExpectedSeq: &present.State.Seq, Data: scope(present)})
	if err != nil {
		return err
	}
	if opened.Code != "ok" {
		return fmt.Errorf("open buzzer: %s", opened.Code)
	}
	start := make(chan struct{})
	errs := make(chan error, 12)
	var wg sync.WaitGroup
	for _, ws := range clients {
		wg.Add(1)
		go func(ws *websocket.Conn) {
			defer wg.Done()
			<-start
			errs <- wsjson.Write(ctx, ws, game.Command{Version: 2, ID: "buzz", Type: "buzz", Data: scope(opened)})
		}(ws)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			return err
		}
	}
	winner := -1
	var winnerState server.Message
	for i, ws := range clients {
		m, err := result(ctx, ws, "buzz")
		if err != nil {
			return err
		}
		if m.Code == "ok" {
			if winner != -1 {
				return errors.New("multiple winners")
			}
			winner = i
			winnerState = m
		}
	}
	if winner < 0 {
		return errors.New("no winner")
	}
	data, err := json.Marshal(map[string]any{"stage_id": winnerState.State.Gameplay.Stage.ID, "instance_id": winnerState.State.Gameplay.Stage.InstanceID, "target_id": people[winner].ParticipantID, "correct": true})
	if err != nil {
		return err
	}
	cmd := game.Command{ID: "judge", Type: "judge", ExpectedSeq: &winnerState.State.Seq, Data: data}
	judged, err := send(ctx, hws, cmd)
	if err != nil {
		return err
	}
	retry, err := send(ctx, hws, cmd)
	if err != nil {
		return err
	}
	if judged.Code != "ok" || !retry.Duplicate || judged.ResultSeq != retry.ResultSeq || retry.State.Participants[winner+1].Score != 100 {
		return errors.New("durability/deduplication check failed")
	}
	clients[winner].CloseNow()
	reconnected, snapshot, err := connect(ctx, base, people[winner])
	if err != nil {
		return err
	}
	defer reconnected.CloseNow()
	if snapshot.State.You != people[winner].ParticipantID || snapshot.State.Participants[winner+1].Score != 100 {
		return errors.New("reconnect check failed")
	}
	finished, err := send(ctx, hws, game.Command{ID: "finish", Type: "finish", ExpectedSeq: &retry.State.Seq, Data: json.RawMessage(`{}`)})
	if err != nil {
		return err
	}
	if finished.Code != "ok" || finished.State.Phase != game.Finished {
		return errors.New("finish check failed")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"room_id": host.RoomID, "players": 12, "winners": 1, "winner": snapshot.State.Participants[winner+1].Name, "score": 100, "duplicate_safe": true, "reconnect_restored": true})
}

func awaitStage(ctx context.Context, ws *websocket.Conn, kind string) (server.Message, error) {
	for {
		var m server.Message
		if err := wsjson.Read(ctx, ws, &m); err != nil {
			return m, err
		}
		if m.Type == "error" {
			return m, fmt.Errorf("server: %s", m.Code)
		}
		if m.State != nil && m.State.Gameplay != nil && m.State.Gameplay.Stage.Kind == kind {
			return m, nil
		}
	}
}
func scope(m server.Message) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"stage_id": m.State.Gameplay.Stage.ID, "instance_id": m.State.Gameplay.Stage.InstanceID})
	return b
}

func request(ctx context.Context, method, url, token string, body any, want int, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		return fmt.Errorf("HTTP status %d", resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
