package game

// Validate accepts only the browser-ready immutable derivative profile.
func (m MediaSpec) Validate() error {
	if !ValidID(m.ID) || m.DurationMS < 0 || m.DurationMS > 600000 {
		return Fault("invalid_media")
	}
	mime := ""
	switch m.Kind {
	case "image":
		mime = "image/png"
		if m.DurationMS != 0 || m.Width < 1 || m.Height < 1 || m.Width > 16000000 || m.Height > 16000000 || int64(m.Width)*int64(m.Height) > 16000000 {
			return Fault("invalid_media")
		}
	case "audio":
		mime = "audio/mp4"
		if m.DurationMS == 0 || m.Width != 0 || m.Height != 0 {
			return Fault("invalid_media")
		}
	case "video":
		mime = "video/mp4"
		if m.DurationMS == 0 || m.Width < 1 || m.Height < 1 || m.Width > 1920 || m.Height > 1080 {
			return Fault("invalid_media")
		}
	default:
		return Fault("invalid_media")
	}
	for _, v := range []MediaVariant{m.Standard, m.Economy} {
		if v.MIME != mime || v.Size < 1 || v.Size > 100*1024*1024 || !mediaKey(v.Key) {
			return Fault("invalid_media")
		}
	}
	return nil
}
func mediaKey(key string) bool {
	if len(key) != 64 {
		return false
	}
	for _, c := range key {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func currentContentStep(s State) (TextStep, bool) {
	g := s.Gameplay
	if g == nil || g.Content == nil || g.Question == nil {
		return TextStep{}, false
	}
	c := g.Content
	steps := g.Question.Spec.Question
	if c.Group == "answer" {
		steps = g.Question.Spec.Answer
	} else if c.Group != "question" {
		return TextStep{}, false
	}
	if c.Index < 0 || c.Index >= len(steps) || steps[c.Index].Media == nil {
		return TextStep{}, false
	}
	return steps[c.Index], true
}

// CurrentMedia returns the private spec only when this participant can see the
// current fragment. HTTP must additionally verify its pinned canonical asset.
func CurrentMedia(s State, id string) (MediaSpec, bool) {
	step, ok := currentContentStep(s)
	if !ok {
		return MediaSpec{}, false
	}
	p, exists := s.Participant(id)
	if !exists || step.Placement == "replic" && p.Role != Host {
		return MediaSpec{}, false
	}
	if s.Gameplay.Content.Group == "answer" && !s.Gameplay.Question.AnswerRevealed {
		return MediaSpec{}, false
	}
	return *step.Media, true
}
func prepareContent(s *State, group string, index int, returnStage string, now int64) {
	g := s.Gameplay
	q := g.Question
	g.Content = &ContentState{Group: group, Index: index, ReturnStage: returnStage, Readiness: []Readiness{}}
	step, _ := currentContentStep(*s)
	if step.Placement == "replic" {
		for _, p := range s.Participants {
			if p.Role == Host {
				g.Content.Readiness = append(g.Content.Readiness, Readiness{ID: p.ID, Status: "loading"})
			}
		}
	} else {
		for _, p := range q.Players {
			g.Content.Readiness = append(g.Content.Readiness, Readiness{ID: p.ID, Status: "loading"})
		}
	}
	setStage(s, "prepare_content", "", now, 10)
	if contentReady(g.Content) {
		scheduleContent(s, now)
	}
}
func contentReady(c *ContentState) bool {
	if c == nil {
		return false
	}
	for _, p := range c.Readiness {
		if p.Status != "ready" {
			return false
		}
	}
	return true
}
func scheduleContent(s *State, now int64) {
	g := s.Gameplay
	g.Content.StartAtMS = now + 750
	g.Content.PausedPositionMS = 0
	g.Stage.Kind = "scheduled_content"
	g.Stage.Expired = false
	g.Stage.DeadlineMS = g.Content.StartAtMS
}
func updateReadiness(s *State, id, status string, now int64) error {
	g := s.Gameplay
	c := g.Content
	if c == nil || g.Stage.Kind != "prepare_content" && g.Stage.Kind != "scheduled_content" {
		return Fault("invalid_phase")
	}
	found := false
	for i := range c.Readiness {
		if c.Readiness[i].ID == id {
			if c.Readiness[i].Status == status {
				return Fault("already_submitted")
			}
			c.Readiness[i].Status = status
			found = true
			break
		}
	}
	if !found {
		return Fault("forbidden")
	}
	if g.Stage.Kind == "scheduled_content" {
		if now >= c.StartAtMS {
			return Fault("stage_expired")
		}
		if status != "ready" {
			g.Stage.Kind = "prepare_content"
			g.Stage.DeadlineMS = now + 10000
			g.Stage.Expired = false
			c.StartAtMS = 0
			c.PausedPositionMS = 0
		}
		return nil
	}
	if deadlinePassed(g, now) {
		g.Stage.Expired = true
		g.Stage.DeadlineMS = 0
		return nil
	}
	if contentReady(c) {
		scheduleContent(s, now)
	}
	return nil
}
func waitMedia(s *State, now int64) error {
	g := s.Gameplay
	if g.Content == nil || g.Stage.Kind != "prepare_content" || !deadlinePassed(g, now) {
		return Fault("invalid_phase")
	}
	setStage(s, "prepare_content", "", now, 10)
	if contentReady(g.Content) {
		scheduleContent(s, now)
	}
	return nil
}
func continueMedia(s *State, now int64) error {
	g := s.Gameplay
	if g.Content == nil || g.Stage.Kind != "prepare_content" || !deadlinePassed(g, now) {
		return Fault("invalid_phase")
	}
	scheduleContent(s, now)
	return nil
}
func skipMediaQuestion(s *State, reason string, now int64) error {
	g := s.Gameplay
	if g.Content == nil || g.Stage.Kind != "prepare_content" || !deadlinePassed(g, now) {
		return Fault("invalid_phase")
	}
	g.Question.SkipReason = reason
	g.Question.CollectionClosed = true
	g.Content = nil
	setStage(s, "resolved", g.ChooserID, now, resolvedSeconds(*s))
	return nil
}
func startPreparedContent(s *State) {
	g := s.Gameplay
	step, ok := currentContentStep(*s)
	if !ok {
		return
	}
	start := g.Content.StartAtMS
	setStage(s, g.Content.ReturnStage, "", start, 0)
	duration := step.DurationMS
	if duration == 0 {
		duration = step.Media.DurationMS
	}
	if duration == 0 {
		duration = int64(g.Question.Rules.SelectionSeconds) * 1000
	}
	g.Stage.DeadlineMS = start + duration
	if g.Content.Group == "question" {
		g.Question.Shown = true
	}
}
func pauseMedia(s *State, now int64) {
	g := s.Gameplay
	if g.Content == nil {
		return
	}
	step, ok := currentContentStep(*s)
	if !ok {
		return
	}
	g.Content.PausedPositionMS = 0
	if g.Content.StoppedPositionMS != nil {
		g.Content.PausedPositionMS = *g.Content.StoppedPositionMS
	} else if g.Content.StartAtMS > 0 && step.Media.DurationMS > 0 {
		g.Content.PausedPositionMS = min(playbackEndPosition(step), max(int64(0), now-g.Content.StartAtMS))
	}
}

// The transition deadline and the playback limit are distinct: wait-for-finish
// and host timer extensions may delay a transition but cannot disclose more of
// a recording than the author configured.
func playbackEndPosition(step TextStep) int64 {
	if step.Media == nil || step.Media.DurationMS == 0 {
		return 0
	}
	if step.DurationMS > 0 {
		return min(step.DurationMS, step.Media.DurationMS)
	}
	return step.Media.DurationMS
}

func stopMedia(s *State, now int64) {
	g := s.Gameplay
	step, ok := currentContentStep(*s)
	if !ok || g.Content.StoppedPositionMS != nil {
		return
	}
	c := g.Content
	position := int64(0)
	if s.Phase == Paused {
		position = c.PausedPositionMS
	} else if c.StartAtMS > 0 {
		position = min(playbackEndPosition(step), max(int64(0), now-c.StartAtMS))
	}
	c.StoppedPositionMS = &position
}
func resumeMedia(s *State, now int64) {
	g := s.Gameplay
	if g.Content == nil {
		return
	}
	if g.Content.StartAtMS > 0 {
		g.Content.StartAtMS += now - g.PausedAtMS
	}
	g.Content.PausedPositionMS = 0
}
func mediaReadinessActor(s State, id string) bool {
	if s.Gameplay == nil || s.Gameplay.Content == nil {
		return false
	}
	if _, ok := CurrentMedia(s, id); !ok {
		return false
	}
	for _, p := range s.Gameplay.Content.Readiness {
		if p.ID == id {
			return true
		}
	}
	return false
}

func transferMediaHost(s *State, id string, now int64) {
	step, ok := currentContentStep(*s)
	if !ok || step.Placement != "replic" {
		return
	}
	g := s.Gameplay
	g.Content.Readiness = []Readiness{{ID: id, Status: "loading"}}
	if g.Stage.Kind != "prepare_content" && g.Stage.Kind != "scheduled_content" {
		return
	}
	g.Content.StartAtMS = 0
	g.Content.PausedPositionMS = 0
	setStage(s, "prepare_content", "", now, 10)
	// Host transfers are allowed during pause. The new host gets a complete
	// preparation interval after resuming, without reviving a paused timer.
	if s.Phase == Paused {
		g.Stage.DeadlineMS = 0
		g.Stage.RemainingMS = 10000
		g.PausedTimer = true
	}
}
