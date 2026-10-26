package game

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestV2MediaPayloadsAreStrict(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		valid bool
	}{
		{`{"v":2,"id":"c","type":"media_ready","data":{"stage_id":"s","instance_id":"i","status":"ready"}}`, true},
		{`{"v":2,"id":"c","type":"continue_media","data":{"stage_id":"s","instance_id":"i"}}`, true},
		{`{"v":2,"id":"c","type":"wait_media","data":{"stage_id":"s","instance_id":"i"}}`, true},
		{`{"v":2,"id":"c","type":"skip_question","data":{"stage_id":"s","instance_id":"i","reason":"File failed"}}`, true},
		{`{"v":2,"id":"c","type":"media_ready","data":{"stage_id":"s","instance_id":"i","status":"ready","key":"private"}}`, false},
		{`{"v":2,"id":"c","type":"media_ready","data":{"stage_id":"s","status":"ready"}}`, false},
		{`{"v":2,"id":"c","type":"media_ready","data":{"stage_id":"s","instance_id":"i","status":"arbitrary"}}`, false},
		{`{"v":2,"id":"c","type":"skip_question","data":{"stage_id":"s","instance_id":"i"}}`, false},
	} {
		var c Command
		if err := json.Unmarshal([]byte(tc.raw), &c); err != nil {
			t.Fatal(err)
		}
		if err := c.Validate(); (err == nil) != tc.valid {
			t.Errorf("%s: %v", tc.raw, err)
		}
	}
}

func mediaFixture(kind, id string) *MediaSpec {
	m := &MediaSpec{ID: id, Kind: kind, Standard: MediaVariant{Key: strings.Repeat("a", 64), Size: 2048}, Economy: MediaVariant{Key: strings.Repeat("b", 64), Size: 1024}}
	switch kind {
	case "image":
		m.Width = 640
		m.Height = 480
		m.Standard.MIME = "image/png"
	case "audio":
		m.DurationMS = 3000
		m.Standard.MIME = "audio/mp4"
	case "video":
		m.DurationMS = 3000
		m.Width = 640
		m.Height = 480
		m.Standard.MIME = "video/mp4"
	}
	m.Economy.MIME = m.Standard.MIME
	return m
}
func mediaQuestion(kind string) QuestionSpec {
	q := qspec("withButton")
	q.Question = []TextStep{{Media: mediaFixture(kind, "current-media")}}
	return q
}
func startMedia(t *testing.T, s State, q QuestionSpec) State {
	t.Helper()
	return do(t, s, "host", "begin_question", BeginQuestionPayload{Question: q})
}
func readyPlayers(t *testing.T, s State) State {
	t.Helper()
	return readyPlayersAt(t, s, 1000)
}
func readyPlayersAt(t *testing.T, s State, now int64) State {
	t.Helper()
	for _, p := range s.Gameplay.Content.Readiness {
		s = doAt(t, s, p.ID, "media_ready", with(s, "status", "ready"), now)
	}
	return s
}
func TestV2MediaReadinessSchedulesAllTwelvePlayers(t *testing.T) {
	s := v2Fixture()
	for i := 3; i <= 12; i++ {
		s.Participants = append(s.Participants, Participant{ID: fmt.Sprintf("p%d", i), Name: "Player", Role: Player})
	}
	s = startMedia(t, s, mediaQuestion("video"))
	if s.Gameplay.Stage.Kind != "prepare_content" || NextDeadline(s) != 11000 || len(s.Gameplay.Content.Readiness) != 12 {
		t.Fatal("missing readiness stage")
	}
	initial := dep(s)
	rejected(t, s, "s1", "media_ready", with(s, "status", "ready"), 1000)
	for i, p := range s.Gameplay.Content.Readiness {
		c := v2cmd("media_ready", map[string]any{"stage_id": initial["stage_id"], "instance_id": initial["instance_id"], "status": "ready"})
		stale := int64(999)
		c.ExpectedSeq = &stale
		var err error
		s, err = ApplyAt(s, p.ID, c, 1000, 7)
		if err != nil {
			t.Fatalf("simultaneous ready %d: %v", i, err)
		}
		if i < 11 && s.Gameplay.Stage.Kind != "prepare_content" {
			t.Fatal("started before every player ready")
		}
	}
	if s.Gameplay.Stage.Kind != "scheduled_content" || NextDeadline(s) != 1750 || s.Gameplay.Stage.ID != initial["stage_id"] {
		t.Fatal("start must be scheduled 750ms on same readiness stage")
	}
	if n, changed := AdvanceAt(s, 1749); changed || n.Gameplay.Stage.Kind != "scheduled_content" {
		t.Fatal("media began early")
	}
	s, changed := AdvanceAt(s, 3000)
	if !changed || s.Gameplay.Stage.Kind != "present_question" || NextDeadline(s) != 4750 {
		t.Fatalf("late tick rewrote scheduled time: %+v", s.Gameplay.Stage)
	}
	v := ProjectAt(s, "p1", 3000)
	if v.Gameplay.Question.Media.StartAtMS != 1750 {
		t.Fatal("client start timestamp drifted")
	}
	rejected(t, s, "p1", "media_ready", map[string]any{"stage_id": initial["stage_id"], "instance_id": initial["instance_id"], "status": "hidden"}, 3000)
}

func mediaPlaybackField(t *testing.T, s State, now int64, field string) (int64, bool) {
	t.Helper()
	b, err := json.Marshal(ProjectAt(s, "p1", now).Gameplay.Question.Media)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	raw, ok := fields[field]
	if !ok {
		return 0, false
	}
	var value int64
	if err = json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value, true
}

func TestV2MediaPlaybackLimitSurvivesLateTimer(t *testing.T) {
	for _, kind := range []string{"audio", "video"} {
		for _, wait := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/wait=%v", kind, wait), func(t *testing.T) {
				q := mediaQuestion(kind)
				q.Question[0].DurationMS, q.Question[0].WaitForFinish = 1000, wait
				s := readyPlayers(t, startMedia(t, v2Fixture(), q))
				s, _ = AdvanceAt(s, 1750)
				if limit, ok := mediaPlaybackField(t, s, 1800, "playback_end_position_ms"); !ok || limit != 1000 {
					t.Fatalf("client cannot stop at configured limit before next snapshot: %d %v", limit, ok)
				}
				s, _ = AdvanceAt(s, 4000)
				wantStage := "buzz_open"
				if wait {
					wantStage = "present_question"
				}
				if s.Gameplay.Stage.Kind != wantStage {
					t.Fatal(s.Gameplay.Stage)
				}
				position, stopped := mediaPlaybackField(t, s, 4500, "stopped_position_ms")
				if !wait && (!stopped || position != 1000) {
					t.Fatalf("late timer exceeded author limit: %d %v", position, stopped)
				}
			})
		}
	}
}

func TestV2MediaManualStopPersistsThroughPauseAndReconnect(t *testing.T) {
	for _, command := range []string{"advance", "open_buzzer", "finish"} {
		t.Run(command, func(t *testing.T) {
			s := readyPlayers(t, startMedia(t, v2Fixture(), mediaQuestion("video")))
			s, _ = AdvanceAt(s, 1750)
			var data any = dep(s)
			if command == "finish" {
				data = EmptyPayload{}
			}
			s = doAt(t, s, "host", command, data, 2250)
			if position, ok := mediaPlaybackField(t, s, 3000, "stopped_position_ms"); !ok || position != 500 {
				t.Fatalf("manual stop: %d %v", position, ok)
			}
			if command != "finish" {
				s = doAt(t, s, "host", "pause", EmptyPayload{}, 3500)
				s = doAt(t, s, "host", "resume", EmptyPayload{}, 8000)
			}
			b, _ := json.Marshal(s)
			var restored State
			if err := json.Unmarshal(b, &restored); err != nil {
				t.Fatal(err)
			}
			if position, ok := mediaPlaybackField(t, restored, 9000, "stopped_position_ms"); !ok || position != 500 {
				t.Fatalf("stop changed after pause/reconnect: %d %v", position, ok)
			}
		})
	}
}

func TestV2AnswerMediaStopsAtTimedOrManualEnd(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		t.Run(fmt.Sprint(automatic), func(t *testing.T) {
			q := mediaQuestion("image")
			q.Answer = []TextStep{{Media: mediaFixture("audio", "answer-media"), DurationMS: 1000}}
			s := readyPlayers(t, startMedia(t, v2Fixture(), q))
			s, _ = AdvanceAt(s, 1750)
			s = doAt(t, s, "host", "open_buzzer", dep(s), 2000)
			s = doAt(t, s, "host", "reveal", dep(s), 2000)
			s = readyPlayersAt(t, s, 2000)
			s, _ = AdvanceAt(s, 2750)
			want := int64(0)
			if automatic {
				s, _ = AdvanceAt(s, 5000)
				want = 1000
			} else {
				s = doAt(t, s, "host", "advance", dep(s), 2750)
			}
			if s.Gameplay.Stage.Kind != "resolved" {
				t.Fatal(s.Gameplay.Stage)
			}
			if stop, ok := mediaPlaybackField(t, s, 5500, "stopped_position_ms"); !ok || stop != want {
				t.Fatalf("answer did not stop: %d %v", stop, ok)
			}
		})
	}
}

func TestV2MediaFinishWhilePausedKeepsPausedPosition(t *testing.T) {
	s := readyPlayers(t, startMedia(t, v2Fixture(), mediaQuestion("audio")))
	s, _ = AdvanceAt(s, 1750)
	s = doAt(t, s, "host", "pause", EmptyPayload{}, 2250)
	s = doAt(t, s, "host", "finish", EmptyPayload{}, 8000)
	if stop, ok := mediaPlaybackField(t, s, 9000, "stopped_position_ms"); !ok || stop != 500 {
		t.Fatalf("paused finish: %d %v", stop, ok)
	}
}
func TestV2MediaErrorHiddenAndDuplicateReadiness(t *testing.T) {
	s := startMedia(t, v2Fixture(), mediaQuestion("audio"))
	s = do(t, s, "p1", "media_ready", with(s, "status", "error"))
	s = do(t, s, "p2", "media_ready", with(s, "status", "ready"))
	if s.Gameplay.Stage.Kind != "prepare_content" {
		t.Fatal("error should block automatic start")
	}
	s = do(t, s, "p1", "media_ready", with(s, "status", "ready"))
	rejected(t, s, "p1", "media_ready", with(s, "status", "ready"), 1100)
	s = doAt(t, s, "p2", "media_ready", with(s, "status", "hidden"), 1200)
	if s.Gameplay.Stage.Kind != "prepare_content" || NextDeadline(s) != 11200 || s.Gameplay.Content.StartAtMS != 0 {
		t.Fatal("hidden player did not cancel scheduled start")
	}
	s = doAt(t, s, "p2", "media_ready", with(s, "status", "ready"), 1300)
	if NextDeadline(s) != 2050 {
		t.Fatal("retry did not reschedule common start")
	}
}
func TestV2MediaTimeoutRequiresExplicitHostChoice(t *testing.T) {
	s := startMedia(t, v2Fixture(), mediaQuestion("image"))
	s, changed := AdvanceAt(s, 11000)
	if !changed || !s.Gameplay.Stage.Expired || NextDeadline(s) != 0 {
		t.Fatal("readiness timeout missing")
	}
	s = doAt(t, s, "p1", "media_ready", with(s, "status", "ready"), 12000)
	s = doAt(t, s, "p2", "media_ready", with(s, "status", "ready"), 12000)
	if s.Gameplay.Stage.Kind != "prepare_content" {
		t.Fatal("late readiness bypassed explicit host decision")
	}
	rejected(t, s, "p1", "continue_media", dep(s), 12000)
	s = doAt(t, s, "host", "continue_media", dep(s), 12000)
	if NextDeadline(s) != 12750 {
		t.Fatal("continue did not schedule common start")
	}
	s = startMedia(t, v2Fixture(), mediaQuestion("image"))
	old := dep(s)
	s, _ = AdvanceAt(s, 11000)
	s = doAt(t, s, "host", "wait_media", dep(s), 12000)
	if NextDeadline(s) != 22000 || s.Gameplay.Stage.ID == old["stage_id"] {
		t.Fatal("wait must create a new 10-second interval")
	}
	rejected(t, s, "p1", "media_ready", map[string]any{"stage_id": old["stage_id"], "instance_id": old["instance_id"], "status": "ready"}, 12000)
	s, _ = AdvanceAt(s, 22000)
	s = doAt(t, s, "host", "skip_question", with(s, "reason", "Unavailable file"), 22000)
	v := ProjectAt(s, "s1", 22000)
	if s.Gameplay.Stage.Kind != "resolved" || v.Gameplay.Question.Media != nil || v.Gameplay.Question.Answer != nil || s.Participants[1].Score != 0 {
		t.Fatal("skip changed score or revealed answer")
	}
}
func TestV2MediaPausePreservesReadinessScheduleAndPlaybackPosition(t *testing.T) {
	for _, kind := range []string{"prepare_content", "scheduled_content", "present_question"} {
		t.Run(kind, func(t *testing.T) {
			s := startMedia(t, v2Fixture(), mediaQuestion("video"))
			s = do(t, s, "p1", "media_ready", with(s, "status", "ready"))
			pauseAt := int64(1250)
			if kind != "prepare_content" {
				s = do(t, s, "p2", "media_ready", with(s, "status", "ready"))
			}
			if kind == "present_question" {
				s, _ = AdvanceAt(s, 1750)
				pauseAt = 2250
			}
			before := s.Gameplay.Stage.ID
			paused, changed := PauseAt(s, pauseAt)
			if !changed || NextDeadline(paused) != 0 || paused.Gameplay.Content.Readiness[0].Status != "ready" {
				t.Fatal("pause lost readiness")
			}
			if kind == "present_question" {
				v := ProjectAt(paused, "p1", 9000)
				if v.Gameplay.Question.Media.PausedPositionMS != 500 {
					t.Fatal("playback position not frozen")
				}
			}
			resumed := doAt(t, paused, "host", "resume", EmptyPayload{}, 10000)
			if resumed.Gameplay.Stage.ID != before {
				t.Fatal("pause invalidated fragment identity")
			}
			want := int64(19750)
			if kind == "scheduled_content" {
				want = 10500
				if resumed.Gameplay.Content.StartAtMS != 10500 {
					t.Fatal("scheduled start not shifted")
				}
			}
			if kind == "present_question" {
				want = 12500
				if resumed.Gameplay.Content.StartAtMS != 9500 {
					t.Fatal("resume jumped playback")
				}
			}
			if NextDeadline(resumed) != want {
				t.Fatalf("deadline %d want %d", NextDeadline(resumed), want)
			}
		})
	}
}
func TestV2MediaProjectionOnlyAuthorizesCurrentVisibleFragment(t *testing.T) {
	q := mediaQuestion("image")
	q.Question = append(q.Question, TextStep{Media: mediaFixture("audio", "future-media")})
	q.Answer = []TextStep{{Media: mediaFixture("video", "answer-media")}}
	s := startMedia(t, v2Fixture(), q)
	for _, id := range []string{"host", "p1", "s1"} {
		v := ProjectAt(s, id, 1000)
		b, _ := json.Marshal(v)
		if v.Gameplay.Question.Media == nil || v.Gameplay.Question.Media.ID != "current-media" || strings.Contains(string(b), strings.Repeat("a", 64)) || strings.Contains(string(b), "future-media") || strings.Contains(string(b), "answer-media") {
			t.Fatalf("projection leak %s: %s", id, b)
		}
	}
	s = readyPlayers(t, s)
	s, _ = AdvanceAt(s, 1750)
	s = doAt(t, s, "host", "advance", dep(s), 2000)
	if ProjectAt(s, "p1", 2000).Gameplay.Question.Media.ID != "future-media" {
		t.Fatal("second fragment missing")
	}
	s = readyPlayersAt(t, s, 2000)
	s, _ = AdvanceAt(s, 2750)
	s = doAt(t, s, "host", "open_buzzer", dep(s), 3000)
	s = doAt(t, s, "host", "reveal", dep(s), 3000)
	v := ProjectAt(s, "p1", 3000)
	if s.Gameplay.Stage.Kind != "prepare_content" || v.Gameplay.Question.Media.ID != "answer-media" {
		t.Fatal("revealed answer did not prepare separately")
	}
	s = doAt(t, s, "host", "finish", EmptyPayload{}, 3000)
	if ProjectAt(s, "p1", 3000).Gameplay.Question.Media.ID != "answer-media" {
		t.Fatal("finish removed already authorized current file")
	}
}
func TestV2MediaReplicIsHostOnlyAndSpecialPreludeStaysFirst(t *testing.T) {
	q := mediaQuestion("audio")
	q.Question[0].Placement = "replic"
	s := startMedia(t, v2Fixture(), q)
	if len(s.Gameplay.Content.Readiness) != 1 || s.Gameplay.Content.Readiness[0].ID != "host" {
		t.Fatal("replic waits on players")
	}
	for _, id := range []string{"p1", "s1"} {
		if ProjectAt(s, id, 1000).Gameplay.Question.Media != nil {
			t.Fatal("replic media leaked")
		}
		rejected(t, s, id, "media_ready", with(s, "status", "ready"), 1000)
	}
	s = do(t, s, "host", "media_ready", with(s, "status", "ready"))
	if s.Gameplay.Stage.Kind != "scheduled_content" {
		t.Fatal("host readiness did not start replic")
	}
	for _, kind := range []string{"secret", "stake", "stakeAll"} {
		s = v2Fixture()
		s.Participants[1].Score = 500
		s.Participants[2].Score = 500
		q = mediaQuestion("image")
		q.Type = kind
		s = startMedia(t, s, q)
		if s.Gameplay.Stage.Kind == "prepare_content" || ProjectAt(s, "p1", 1000).Gameplay.Question.Media != nil {
			t.Fatal("media leaked before special prelude")
		}
	}
}
func TestV2MediaExplicitDurationAndWaitForFinish(t *testing.T) {
	for _, wait := range []bool{false, true} {
		q := mediaQuestion("video")
		q.Question[0].DurationMS = 1500
		q.Question[0].WaitForFinish = wait
		s := readyPlayers(t, startMedia(t, v2Fixture(), q))
		s, _ = AdvanceAt(s, 1750)
		if NextDeadline(s) != 3250 {
			t.Fatal("explicit duration lost")
		}
		s, _ = AdvanceAt(s, 3250)
		if wait {
			if s.Gameplay.Stage.Kind != "present_question" || !s.Gameplay.Stage.Expired {
				t.Fatal("waitForFinish must retain manual advance")
			}
			s = doAt(t, s, "host", "advance", dep(s), 3250)
		}
		if s.Gameplay.Stage.Kind != "buzz_open" {
			t.Fatal("media did not finish presentation")
		}
	}
}

func TestV2MediaHostTransferRestartsReplicReadinessWhilePaused(t *testing.T) {
	for _, scheduled := range []bool{false, true} {
		q := mediaQuestion("audio")
		q.Question[0].Placement = "replic"
		s := startMedia(t, v2Fixture(), q)
		if scheduled {
			s = readyPlayers(t, s)
		}
		old := dep(s)
		s, _ = PauseAt(s, 1200)
		s = doAt(t, s, "host", "transfer_host", TransferPayload{TargetID: "s1"}, 1300)
		if s.Phase != Paused || s.Gameplay.Stage.Kind != "prepare_content" || s.Gameplay.Content.Readiness[0] != (Readiness{ID: "s1", Status: "loading"}) || s.Gameplay.Content.StartAtMS != 0 || NextDeadline(s) != 0 {
			t.Fatal("transferred host retained old readiness or resumed playback")
		}
		if _, ok := CurrentMedia(s, "host"); ok {
			t.Fatal("old host retained private replic access")
		}
		s = doAt(t, s, "s1", "resume", EmptyPayload{}, 2000)
		if NextDeadline(s) != 12000 {
			t.Fatal("new host did not receive full preparation interval")
		}
		rejected(t, s, "s1", "media_ready", map[string]any{"stage_id": old["stage_id"], "instance_id": old["instance_id"], "status": "ready"}, 2000)
		s = doAt(t, s, "s1", "media_ready", with(s, "status", "ready"), 2000)
		if NextDeadline(s) != 2750 {
			t.Fatal("new host could not start replic")
		}
	}
}

func TestV2MediaAnswerSequenceAndEligiblePlayers(t *testing.T) {
	s := v2Fixture()
	s.Participants[1].Score = 100
	q := mediaQuestion("image")
	q.Type = "stakeAll"
	q.Answer = []TextStep{{Media: mediaFixture("audio", "answer-one")}, {Text: "Explanation"}, {Media: mediaFixture("image", "answer-two")}}
	s = startMedia(t, s, q)
	s = do(t, s, "p1", "wager", with(s, "amount", 50))
	if len(s.Gameplay.Content.Readiness) != 1 || s.Gameplay.Content.Readiness[0].ID != "p1" {
		t.Fatal("ineligible zero-score player blocks readiness")
	}
	rejected(t, s, "p2", "media_ready", with(s, "status", "ready"), 1000)
	s = readyPlayers(t, s)
	s, _ = AdvanceAt(s, 1750)
	s = doAt(t, s, "host", "advance", dep(s), 2000)
	s = doAt(t, s, "p1", "answer", with(s, "text", "A"), 2000)
	s = doAt(t, s, "host", "reveal", with(s, "target_id", "p1"), 2000)
	for _, id := range []string{"host", "p1", "p2", "s1"} {
		m, ok := CurrentMedia(s, id)
		if !ok || m.ID != "answer-one" {
			t.Fatal("first revealed answer media missing")
		}
		b, _ := json.Marshal(ProjectAt(s, id, 2000))
		if strings.Contains(string(b), "answer-two") || id != "host" && strings.Contains(string(b), "Explanation") {
			t.Fatal("future answer fragment leaked")
		}
	}
	if _, ok := CurrentMedia(s, "unknown"); ok {
		t.Fatal("unknown participant authorized")
	}
	s = readyPlayersAt(t, s, 2000)
	s, _ = AdvanceAt(s, 2750)
	s = doAt(t, s, "host", "advance", dep(s), 3000)
	if _, ok := CurrentMedia(s, "p1"); ok {
		t.Fatal("media access survived text fragment")
	}
	s = doAt(t, s, "host", "advance", dep(s), 3000)
	if m, ok := CurrentMedia(s, "p1"); !ok || m.ID != "answer-two" {
		t.Fatal("last answer media missing")
	}
}

func TestV2MediaMaximumSnapshotWithTwelvePlayers(t *testing.T) {
	s := v2Fixture()
	for i := 3; i <= 12; i++ {
		s.Participants = append(s.Participants, Participant{ID: fmt.Sprintf("p%d", i), Name: strings.Repeat("Я", 32), Role: Player})
	}
	q := qspec("forAll")
	q.Question = []TextStep{{Text: strings.Repeat("Q", 4094)}, {Media: mediaFixture("image", "question-media")}}
	q.Right = []string{strings.Repeat("A", 4096)}
	q.Answer = []TextStep{{Text: strings.Repeat("E", 4094)}, {Media: mediaFixture("video", "answer-media")}}
	s = startMedia(t, s, q)
	s = do(t, s, "host", "advance", dep(s))
	s = readyPlayers(t, s)
	s, _ = AdvanceAt(s, 1750)
	s = doAt(t, s, "host", "advance", dep(s), 2000)
	for _, id := range playerIDs(s) {
		s = doAt(t, s, id, "answer", with(s, "text", strings.Repeat("x", 1024)), 2000)
	}
	for _, id := range playerIDs(s) {
		d := with(s, "target_id", id)
		d["correct"] = true
		s = doAt(t, s, "host", "judge", d, 2000)
	}
	s = doAt(t, s, "host", "advance", dep(s), 2000)
	reason := strings.Repeat("<&", 512)
	for i := 0; i < 64; i++ {
		s.Gameplay.Decisions = append(s.Gameplay.Decisions, Decision{ID: fmt.Sprintf("judgement-%d", i), ParticipantID: "p1", Reason: reason, QuestionID: "old"})
		s.Gameplay.Appeals = append(s.Gameplay.Appeals, Appeal{ID: fmt.Sprintf("appeal-%d", i), JudgementID: fmt.Sprintf("judgement-%d", i), ParticipantID: "p1", Reason: reason, Resolution: reason})
	}
	for _, id := range []string{"host", "p1", "s1"} {
		v := ProjectAt(s, id, 2000)
		b, _ := json.Marshal(v)
		if len(v.Gameplay.Readiness) != 12 || v.Gameplay.Question.Media == nil || len(b) > 60*1024 {
			t.Fatalf("%s maximum media snapshot invalid: %d bytes", id, len(b))
		}
	}
}
