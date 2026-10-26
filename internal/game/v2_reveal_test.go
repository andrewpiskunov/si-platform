package game

import (
	"encoding/json"
	"strings"
	"testing"
)

func revealFixture(t *testing.T, spec QuestionSpec) State {
	t.Helper()
	s := v2Fixture()
	s.Gameplay.Rules.ProgressiveReveal = true
	s.Gameplay.Rules.AutoOpen = true
	s.Gameplay.Rules.ReadingSeconds = 10
	return doAt(t, s, "host", "begin_question", BeginQuestionPayload{Question: spec}, 2000)
}
func publicReveal(t *testing.T, s State, now int64) *RevealView {
	t.Helper()
	r := ProjectDisplayAt(s, now).Gameplay.Question.Reveal
	if r == nil {
		t.Fatalf("missing reveal: stage=%+v", s.Gameplay.Stage)
	}
	return r
}
func TestProgressiveRevealPauseResumeAndPersistence(t *testing.T) {
	s := revealFixture(t, qspec("withButton"))
	r := publicReveal(t, s, 4500)
	if r.Kind != "text" || r.StartAtMS != 2000 || r.DurationMS != 10000 || r.ElapsedMS != 2500 || r.TextOffset != 0 {
		t.Fatalf("reveal: %+v", r)
	}
	s = doAt(t, s, "host", "pause", EmptyPayload{}, 4500)
	blob, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var restored State
	if err = json.Unmarshal(blob, &restored); err != nil {
		t.Fatal(err)
	}
	if got := publicReveal(t, restored, 99000).ElapsedMS; got != 2500 {
		t.Fatalf("pause moved: %d", got)
	}
	s = doAt(t, restored, "host", "resume", EmptyPayload{}, 10000)
	r = publicReveal(t, s, 10500)
	if r.StartAtMS != 7500 || r.ElapsedMS != 3000 || s.Gameplay.Stage.DeadlineMS != 17500 {
		t.Fatalf("resume: %+v %+v", r, s.Gameplay.Stage)
	}
	s, changed := AdvanceAt(s, 17500)
	if !changed || s.Gameplay.Stage.Kind != "buzz_open" || ProjectDisplayAt(s, 17500).Gameplay.Question.Reveal != nil {
		t.Fatal("server reading deadline did not fully reveal and open buzzer")
	}
}
func TestProgressiveRevealUnicodePrefixAndPrivateReplic(t *testing.T) {
	spec := qspec("withButton")
	spec.Question = []TextStep{{Text: "前🙂", DurationMS: 1000}, {Text: "HOST PRIVATE", Placement: "replic"}, {Text: "новый 👩‍🚀"}}
	s := revealFixture(t, spec)
	s = doAt(t, s, "host", "advance", dep(s), 2200)
	if v := ProjectDisplayAt(s, 2250).Gameplay.Question; v.Text != "前🙂" || v.Reveal != nil {
		t.Fatalf("replic leaked or animated: %+v", v)
	}
	s = doAt(t, s, "host", "advance", dep(s), 2300)
	v := ProjectDisplayAt(s, 2500).Gameplay.Question
	if v.Text != "前🙂\nновый 👩‍🚀" || v.Reveal.TextOffset != 3 || v.Reveal.ElapsedMS != 200 {
		t.Fatalf("public unicode offset: %+v %+v", v, v.Reveal)
	}
	if strings.Contains(v.Text, "PRIVATE") || v.Answer != nil {
		t.Fatal("private content leaked")
	}
	// A manual early transition releases the entire current fragment immediately.
	s = doAt(t, s, "host", "open_buzzer", dep(s), 2600)
	if v := ProjectDisplayAt(s, 2600).Gameplay.Question; v.Reveal != nil || v.Text != "前🙂\nновый 👩‍🚀" {
		t.Fatalf("early open left clipping: %+v", v)
	}
}
func TestProgressiveImageReadinessAndPause(t *testing.T) {
	spec := qspec("withButton")
	spec.Question = []TextStep{{Text: "Previous"}, {Text: "Image caption", Media: mediaFixture("image", "image"), DurationMS: 6000}}
	s := revealFixture(t, spec)
	s = doAt(t, s, "host", "advance", dep(s), 2500)
	for _, now := range []int64{2500, 2600} {
		v := ProjectDisplayAt(s, now).Gameplay.Question
		if v.Reveal != nil || v.Text != "Previous" {
			t.Fatalf("current caption escaped readiness: %+v", v)
		}
	}
	s = readyPlayersAt(t, s, 3000)
	if s.Gameplay.Stage.Kind != "scheduled_content" {
		t.Fatal("missing scheduled gate")
	}
	if v := ProjectDisplayAt(s, 3300).Gameplay.Question; v.Reveal != nil || v.Text != "Previous" {
		t.Fatalf("scheduled content exposed: %+v", v)
	}
	s = doAt(t, s, "host", "pause", EmptyPayload{}, 3400)
	s = doAt(t, s, "host", "resume", EmptyPayload{}, 5000)
	if s.Gameplay.Content.StartAtMS != 5350 {
		t.Fatal("scheduled start not shifted with pause")
	}
	s, changed := AdvanceAt(s, 5350)
	if !changed {
		t.Fatal("image did not start")
	}
	r := publicReveal(t, s, 6350)
	if r.Kind != "image" || r.StartAtMS != 5350 || r.DurationMS != 6000 || r.ElapsedMS != 1000 {
		t.Fatalf("image timing: %+v", r)
	}
	if ProjectDisplayAt(s, 6350).Gameplay.Question.Text != "Previous\nImage caption" {
		t.Fatal("caption missing after start")
	}
	s = doAt(t, s, "host", "pause", EmptyPayload{}, 6350)
	if publicReveal(t, s, 99999).ElapsedMS != 1000 {
		t.Fatal("paused image reveal moved")
	}
	s = doAt(t, s, "host", "resume", EmptyPayload{}, 8000)
	if publicReveal(t, s, 8500).ElapsedMS != 1500 {
		t.Fatal("image resume jumped")
	}
}
func TestProgressiveRevealDoesNotChangeFalseStartOrExtend(t *testing.T) {
	s := revealFixture(t, qspec("withButton"))
	s = doAt(t, s, "p1", "buzz", dep(s), 3000)
	if s.Gameplay.Stage.Kind != "present_question" || submission(s.Gameplay.Question, "p1").BlockedUntilMS != 6000 {
		t.Fatal("false start changed")
	}
	s = doAt(t, s, "host", "extend", map[string]any{"stage_id": s.Gameplay.Stage.ID, "seconds": 30}, 4000)
	if publicReveal(t, s, 7000).DurationMS != 10000 || publicReveal(t, s, 15000).ElapsedMS != 10000 {
		t.Fatal("extending stage rewound reveal")
	}
	s = doAt(t, s, "host", "restart_stage", map[string]any{"stage_id": s.Gameplay.Stage.ID, "reason": "Replay reading"}, 16000)
	if publicReveal(t, s, 16000).ElapsedMS != 0 || publicReveal(t, s, 16000).StartAtMS != 16000 {
		t.Fatal("restart did not reset reveal")
	}
}
func TestProgressiveRevealLegacyAndUnsupportedContent(t *testing.T) {
	legacy := begin(t, v2Fixture(), "withButton")
	if legacy.Gameplay.Reveal != nil || ProjectDisplayAt(legacy, 1200).Gameplay.Question.Reveal != nil {
		t.Fatal("old rules gained reveal")
	}
	for _, kind := range []string{"audio", "video"} {
		t.Run(kind, func(t *testing.T) {
			s := revealFixture(t, mediaQuestion(kind))
			s = readyPlayersAt(t, s, 2500)
			s, _ = AdvanceAt(s, 3250)
			if s.Gameplay.Reveal != nil || ProjectDisplayAt(s, 3500).Gameplay.Question.Reveal != nil {
				t.Fatal("recording gained progressive reveal")
			}
		})
	}
	s := revealFixture(t, qspec("withButton"))
	s = doAt(t, s, "host", "open_buzzer", dep(s), 3000)
	s = doAt(t, s, "host", "reveal", dep(s), 3100)
	if s.Gameplay.Reveal != nil || ProjectDisplayAt(s, 3200).Gameplay.Question.Reveal != nil {
		t.Fatal("answer disclosure animated")
	}
}
func TestProgressiveRevealDurationBoundsAndManualMode(t *testing.T) {
	for _, tc := range []struct {
		name           string
		duration, want int64
	}{{"short", 50, 50}, {"long", 600000, 120000}, {"manual", 0, 30000}} {
		t.Run(tc.name, func(t *testing.T) {
			s := v2Fixture()
			s.Gameplay.Rules.ProgressiveReveal = true // AutoOpen=false, ReadingSeconds=0 is valid.
			spec := qspec("withButton")
			spec.Question[0].DurationMS = tc.duration
			s = doAt(t, s, "host", "begin_question", BeginQuestionPayload{Question: spec}, 2000)
			if r := publicReveal(t, s, 2000); r.DurationMS != tc.want {
				t.Fatalf("duration: %+v", r)
			}
			if tc.name == "manual" {
				s, _ = AdvanceAt(s, 32000)
				if s.Gameplay.Stage.Kind != "present_question" || !s.Gameplay.Stage.Expired || publicReveal(t, s, 40000).ElapsedMS != 30000 {
					t.Fatal("manual reading should fully reveal without opening buzzer")
				}
			}
		})
	}
	// Missing timers in a restored/legacy manual state still yield bounded metadata.
	s := revealFixture(t, qspec("withButton"))
	s.Gameplay.Stage.DeadlineMS = 0
	s.Gameplay.Question.Rules.ReadingSeconds = 0
	startReveal(&s, 2000)
	if publicReveal(t, s, 2000).DurationMS != 10000 {
		t.Fatal("manual fallback missing")
	}
}
