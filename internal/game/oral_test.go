package game

import "testing"

func TestOralImmediateJudgementAndPrivateAnswer(t *testing.T) {
	s := v2Fixture()
	s.Gameplay.Rules.AnswerMode = "oral"
	s.Gameplay.Rules.AnswerSeconds = 30
	spec := qspec("withButton")
	spec.Answer = []TextStep{{Text: "Explanation"}, {Text: "Full answer"}}
	s = do(t, s, "host", "begin_question", BeginQuestionPayload{Question: spec})
	host := ProjectAt(s, "host", 1000).Gameplay
	if host.Question.Answer == nil || *host.Question.Answer != "SECRET RIGHT\nExplanation\nFull answer" {
		t.Fatal("host must see full answer immediately", host.Question)
	}
	if ProjectAt(s, "p1", 1000).Gameplay.Question.Answer != nil {
		t.Fatal("answer leaked")
	}
	s = do(t, s, "host", "open_buzzer", dep(s))
	s = do(t, s, "p1", "buzz", dep(s))
	if s.Gameplay.Stage.DeadlineMS-1000 != 30000 {
		t.Fatal("wrong answer deadline")
	}
	for _, a := range ProjectAt(s, "p1", 1000).Gameplay.AllowedActions {
		if a.Type == "answer" {
			t.Fatal("oral text input allowed")
		}
	}
	found := false
	for _, a := range ProjectAt(s, "host", 1000).Gameplay.AllowedActions {
		if a.Type == "judge" {
			found = true
		}
	}
	if !found {
		t.Fatal("no immediate judgement")
	}
	s = judge(t, s, "p1", true)
	if s.Participants[1].Score != 100 {
		t.Fatal("oral score missing")
	}
}

func TestDisplayNeverProjectsHostSecrets(t *testing.T) {
	s := begin(t, v2Fixture(), "withButton")
	v := ProjectDisplayAt(s, 1000)
	if v.Gameplay.Question.Answer != nil || len(v.Gameplay.AllowedActions) != 0 || v.You != "" {
		t.Fatal("private screen data", v)
	}
	if len(v.Participants) != len(s.Participants) {
		t.Fatal("screen added a seat")
	}
	s = do(t, s, "host", "open_buzzer", dep(s))
	s = do(t, s, "host", "reveal", dep(s))
	if ProjectDisplayAt(s, 1000).Gameplay.Question.Answer == nil {
		t.Fatal("public answer missing")
	}
}
