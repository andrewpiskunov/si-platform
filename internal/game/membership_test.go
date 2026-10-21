package game

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func removalCommand(s State, target, mode string) Command {
	c := v2cmd("remove_participant", map[string]string{"target_id": target, "mode": mode})
	c.ExpectedSeq = &s.Seq
	return c
}
func TestMembershipKick(t *testing.T) {
	s := v2Fixture()
	n, err := ApplyAt(s, "host", removalCommand(s, "p1", "kick"), 1000, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(n.Participants[1])
	var p map[string]any
	_ = json.Unmarshal(b, &p)
	if p["removed"] != true {
		t.Fatal("kick must retain a removed historical participant")
	}
	if Project(n, "p1").Gameplay != nil {
		t.Fatal("removed participant received gameplay")
	}
	if _, err = ApplyAt(n, "p1", v2cmd("finish", EmptyPayload{}), 1000, 0); Code(err) != "unauthorized" {
		t.Fatal(err)
	}
	if reflect.DeepEqual(s, n) {
		t.Fatal("no transition")
	}
}
func TestMembershipStrictPayload(t *testing.T) {
	s := v2Fixture()
	for _, raw := range []string{`{"target_id":"p1","mode":"kick","targets":["host"]}`, `{"target_id":"p1","mode":"other"}`, `{"mode":"kick"}`} {
		c := removalCommand(s, "p1", "kick")
		c.Data = json.RawMessage(raw)
		if _, err := c.Payload(); err == nil {
			t.Fatal(raw)
		}
	}
	c := removalCommand(s, "p1", "kick")
	c.ExpectedSeq = nil
	if _, err := c.Payload(); err == nil {
		t.Fatal("missing seq accepted")
	}
}

func TestMembershipTrustedGroupAndDenials(t *testing.T) {
	s := v2Fixture()
	for _, tc := range []struct {
		actor, target, mode string
		ids                 []string
		phase               Phase
		stale               bool
		code                string
	}{
		{"p1", "p2", "kick", []string{"p2"}, Running, false, "forbidden"},
		{"missing", "p2", "kick", []string{"p2"}, Running, false, "unauthorized"},
		{"host", "host", "kick", []string{"host"}, Running, false, "forbidden"},
		{"host", "p1", "ban_guest_session", []string{"p1", "host"}, Running, false, "forbidden"},
		{"host", "p1", "kick", []string{"p1"}, Finished, false, "finished"},
		{"host", "p1", "kick", []string{"p1"}, Running, true, "stale_state"},
		{"host", "p1", "ban_guest_session", nil, Running, false, "invalid_target"},
	} {
		n := s.Clone()
		n.Phase = tc.phase
		c := removalCommand(n, tc.target, tc.mode)
		if tc.stale {
			v := int64(99)
			c.ExpectedSeq = &v
		}
		before := n.Clone()
		got, e := ApplyRemovalAt(n, tc.actor, c, tc.ids, 1000)
		if Code(e) != tc.code || !reflect.DeepEqual(got, before) || !reflect.DeepEqual(n, before) {
			t.Fatalf("%+v: %v", tc, e)
		}
	}
	c := removalCommand(s, "p1", "ban_guest_session")
	if _, e := ApplyAt(s, "host", c, 1000, 0); e == nil {
		t.Fatal("unresolved ban accepted")
	}
	n, e := ApplyRemovalAt(s, "host", c, []string{"p1", "p2"}, 1000)
	if e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{"p1", "p2"} {
		if _, ok := n.ActiveParticipant(id); ok {
			t.Fatal(id)
		}
	}
}

func TestMembershipFrozenRemoval(t *testing.T) {
	for _, kind := range []string{"stake", "secret", "stakeAll", "withButton"} {
		t.Run(kind, func(t *testing.T) {
			s := v2Fixture()
			s.Participants[1].Score = 500
			s.Participants[2].Score = 700
			s = begin(t, s, kind)
			q := s.Gameplay.Question
			q.Candidates = []string{"p1", "p2"}
			q.Seed = 17
			q.RecipientID = "p1"
			wager := int64(100)
			answer := "accepted"
			q.Players[0].Wager = &wager
			q.Players[0].Answer = &answer
			before := s.Clone()
			n, e := ApplyAt(s, "host", removalCommand(s, "p1", "kick"), 1500, 0)
			if e != nil {
				t.Fatal(e)
			}
			if n.Phase != Paused || !reflect.DeepEqual(n.Gameplay.Question, before.Gameplay.Question) || n.Gameplay.ChooserID != before.Gameplay.ChooserID {
				t.Fatal("frozen question changed or not paused")
			}
			if !reflect.DeepEqual(s, before) {
				t.Fatal("mutated input")
			}
		})
	}
}
func TestMembershipFinalSurvivesBelowMinimum(t *testing.T) {
	s := v2Fixture()
	s.Participants[1].Score = 500
	s.Participants[2].Score = 700
	s = do(t, s, "host", "begin_round", BeginRoundPayload{Round: RoundSpec{ID: "r", Type: "final", Themes: []ThemeSpec{{ID: "t", QuestionID: "q"}}}})
	frozen := s.Clone().Gameplay.Final
	n, e := ApplyAt(s, "host", removalCommand(s, "p1", "kick"), 1000, 0)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(n.Gameplay.Final, frozen) {
		t.Fatal("final changed")
	}
	n = do(t, n, "host", "resume", EmptyPayload{})
	n = begin(t, n, "stakeAll")
	if len(n.Gameplay.Question.Players) != 2 || n.Gameplay.Question.Players[0].Maximum != 500 {
		t.Fatal("lost frozen eligibility/maximum")
	}
}
func TestMembershipReadinessUnavailableAcrossSteps(t *testing.T) {
	s := begin(t, v2Fixture(), "withButton")
	s.Gameplay.Content = &ContentState{Readiness: []Readiness{{ID: "p1", Status: "ready"}, {ID: "p2", Status: "ready"}}}
	n, e := ApplyAt(s, "host", removalCommand(s, "p1", "kick"), 1000, 0)
	if e != nil {
		t.Fatal(e)
	}
	if contentReady(n.Gameplay.Content) || n.Gameplay.Content.Readiness[0].Status != "unavailable" {
		t.Fatal("removed readiness retained")
	}
	prepareContent(&n, "question", 0, "present_question", 1000)
	if n.Gameplay.Content.Readiness[0].Status != "unavailable" {
		t.Fatal("later readiness reset for removed participant")
	}
}
func TestMembershipChooserRepairsAtResolvedBoundary(t *testing.T) {
	s := begin(t, v2Fixture(), "withButton")
	n, e := ApplyAt(s, "host", removalCommand(s, "p1", "kick"), 1000, 0)
	if e != nil {
		t.Fatal(e)
	}
	n = do(t, n, "host", "resume", EmptyPayload{})
	n = do(t, n, "host", "open_buzzer", dep(n))
	n = do(t, n, "host", "reveal", dep(n))
	if n.Gameplay.ChooserID != "p2" || n.Gameplay.Question.ChooserBefore != "p1" {
		t.Fatal("chooser not safely repaired")
	}
}

func TestMembershipPruneFrozenAndHistory(t *testing.T) {
	s := v2Fixture()
	// Many unrelated removed identities must not consume seats or snapshot space.
	for i := 0; i < 200; i++ {
		s.Participants = append(s.Participants, Participant{ID: fmt.Sprintf("old%d", i), Name: strings.Repeat("Я", 32), Role: Player, Removed: true})
	}
	s.Gameplay.Decisions = []Decision{{ID: "d", ParticipantID: "old1"}}
	s.Gameplay.Appeals = []Appeal{{ID: "a", ParticipantID: "old2"}}
	s.Gameplay.Question = &ActiveQuestion{Players: []Submission{{ID: "old3"}}, ChooserBefore: "old4", Candidates: []string{"old5"}, RecipientID: "old6", AfterAnswerTurn: "old7", Auction: &AuctionState{LeaderID: "old8", Order: []string{"old9"}, ActiveIDs: []string{"old10"}}}
	s.Gameplay.Final = &FinalState{Order: []string{"old11"}, EligibleIDs: []string{"old12"}}
	s.Winner = "old13"
	s.Gameplay.Stage.TurnID = "old14"
	n := PruneParticipants(s)
	if len(n.Participants) != len(v2Fixture().Participants)+14 {
		t.Fatalf("retained %d", len(n.Participants))
	}
	if len(s.Participants) != len(v2Fixture().Participants)+200 {
		t.Fatal("mutated input")
	}
	for i := 1; i <= 14; i++ {
		if _, ok := n.Participant(fmt.Sprintf("old%d", i)); !ok {
			t.Fatal(i)
		}
	}
	n.Gameplay.Question = nil
	n.Gameplay.Final = nil
	n.Gameplay.Decisions = nil
	n.Gameplay.Appeals = nil
	n.Gameplay.Stage.TurnID = ""
	n.Winner = ""
	n = PruneParticipants(n)
	if len(n.Participants) != len(v2Fixture().Participants) {
		t.Fatal("stale references retained")
	}
}
func TestMembershipSeatsAndOldSnapshots(t *testing.T) {
	s := v2Fixture()
	b, _ := json.Marshal(s)
	var old State
	_ = json.Unmarshal(b, &old)
	if _, ok := old.ActiveParticipant("p1"); !ok {
		t.Fatal("old snapshot inactive")
	}
	for len(playerIDs(s)) < 12 {
		var e error
		s, e = AddParticipant(s, Participant{ID: fmt.Sprintf("new%d", len(s.Participants)), Name: "New", Role: Player})
		if e != nil {
			t.Fatal(e)
		}
	}
	n, e := ApplyAt(s, "host", removalCommand(s, "p1", "kick"), 1000, 0)
	if e != nil {
		t.Fatal(e)
	}
	n, e = AddParticipant(n, Participant{ID: "rejoined", Name: "Again", Role: Player, Score: 900, Removed: true})
	if e != nil {
		t.Fatal(e)
	}
	p, ok := n.ActiveParticipant("rejoined")
	if !ok || p.Score != 0 {
		t.Fatal(p)
	}
}

func TestMembershipScheduledMediaRequiresExplicitContinuation(t *testing.T) {
	s := begin(t, v2Fixture(), "withButton")
	s.Gameplay.Stage.Kind = "scheduled_content"
	s.Gameplay.Stage.DeadlineMS = 1750
	s.Gameplay.Content = &ContentState{StartAtMS: 1750, Readiness: []Readiness{{ID: "p1", Status: "ready"}, {ID: "p2", Status: "ready"}}}
	n, e := ApplyAt(s, "host", removalCommand(s, "p1", "kick"), 1200, 0)
	if e != nil {
		t.Fatal(e)
	}
	n = doAt(t, n, "host", "resume", EmptyPayload{}, 2000)
	if n.Gameplay.Stage.Kind != "prepare_content" || n.Gameplay.Content.StartAtMS != 0 {
		t.Fatal("scheduled media bypasses unavailable readiness")
	}
}
func TestMembershipFrozenRandomOutcomeAndCorrection(t *testing.T) {
	s := v2Fixture()
	s.Gameplay.Rules.BuzzerMode = "random"
	s = begin(t, s, "withButton")
	s = do(t, s, "host", "open_buzzer", dep(s))
	s = do(t, s, "p1", "buzz", dep(s))
	n, e := ApplyAt(s, "host", removalCommand(s, "p1", "kick"), 1100, 0)
	if e != nil {
		t.Fatal(e)
	}
	n = doAt(t, n, "host", "resume", EmptyPayload{}, 2000)
	n, changed := AdvanceAt(n, n.Gameplay.Stage.DeadlineMS)
	if !changed || n.Winner != "p1" {
		t.Fatal("removed candidate lost frozen random outcome")
	}
	n = judge(t, n, "p1", true)
	p, _ := n.Participant("p1")
	if p.Score != 100 {
		t.Fatal(p)
	}
	d := n.Gameplay.Decisions[len(n.Gameplay.Decisions)-1]
	n = do(t, n, "host", "correct_judgement", CorrectionPayload{JudgementID: d.ID, Correct: new(bool), Reason: "correction"})
	p, _ = n.Participant("p1")
	if p.Score != -100 {
		t.Fatal("removed score cannot be corrected", p)
	}
}
func TestMembershipSpectatorDoesNotPause(t *testing.T) {
	s := v2Fixture()
	s.Participants = append(s.Participants, Participant{ID: "spect", Name: "Spectator", Role: Spectator})
	s = begin(t, s, "withButton")
	n, e := ApplyAt(s, "host", removalCommand(s, "spect", "kick"), 1000, 0)
	if e != nil || n.Phase != Running {
		t.Fatal(e, n.Phase)
	}
}

func TestMembershipPruneBoundsPrivateHistoryAndTransport(t *testing.T) {
	s := v2Fixture()
	for i := 0; i < 1000; i++ {
		id := fmt.Sprintf("%032x", i+100)
		s.Participants = append(s.Participants, Participant{ID: id, Name: strings.Repeat("Я", 32), Role: Player, Removed: true})
		s.Gameplay.Decisions = append(s.Gameplay.Decisions, Decision{ID: fmt.Sprintf("adjustment-%d", i), ParticipantID: id, Reason: strings.Repeat("r", 1024)})
		s.Gameplay.Appeals = append(s.Gameplay.Appeals, Appeal{ID: fmt.Sprintf("appeal-%d", i), ParticipantID: id, Reason: strings.Repeat("r", 1024), Resolution: strings.Repeat("r", 1024)})
	}
	n := PruneParticipants(s)
	if len(n.Gameplay.Decisions) != 64 || len(n.Gameplay.Appeals) != 64 || len(n.Participants) != len(v2Fixture().Participants)+64 {
		t.Fatal("private history/roster not bounded")
	}
	b, e := json.Marshal(Project(n, "host"))
	if e != nil || len(b) >= 64*1024 {
		t.Fatalf("projection size %d: %v", len(b), e)
	}
}
func TestMembershipPruneKeepsLegacyWinner(t *testing.T) {
	s := fixture()
	s.Participants[1].Removed = true
	s.Winner = "p1"
	s.Question = &Question{ID: "q"}
	n := PruneParticipants(s)
	if _, ok := n.Participant("p1"); !ok {
		t.Fatal("legacy winner lost")
	}
	if Project(n, "p1").Question != nil {
		t.Fatal("legacy removed identity received question")
	}
}
