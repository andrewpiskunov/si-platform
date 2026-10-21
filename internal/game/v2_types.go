package game

// Rules is the versioned, per-question rules snapshot. Durations are seconds.
type Rules struct {
	Preset            string `json:"preset"`
	SelectionSeconds  int    `json:"selection_seconds"`
	AnswerSeconds     int    `json:"answer_seconds"`
	SpecialSeconds    int    `json:"special_seconds"`
	WagerSeconds      int    `json:"wager_seconds"`
	FinalSeconds      int    `json:"final_seconds"`
	Penalties         bool   `json:"penalties"`
	BuzzerMode        string `json:"buzzer_mode"`
	WindowMS          int    `json:"window_ms"`
	FalseStart        bool   `json:"false_start"`
	FalseStartSeconds int    `json:"false_start_seconds"`
	NoRiskMultiplier  int    `json:"no_risk_multiplier"`
}
type TextStep struct {
	Media         *MediaSpec `json:"media,omitempty"`
	Text          string     `json:"text"`
	DurationMS    int64      `json:"duration_ms,omitempty"`
	WaitForFinish bool       `json:"wait_for_finish,omitempty"`
	Placement     string     `json:"placement,omitempty"`
}
type PriceRange struct {
	Minimum int64 `json:"minimum"`
	Maximum int64 `json:"maximum"`
	Step    int64 `json:"step"`
}
type QuestionSpec struct {
	ID            string      `json:"id"`
	RoundID       string      `json:"round_id"`
	ThemeID       string      `json:"theme_id"`
	Theme         string      `json:"theme"`
	Type          string      `json:"type"`
	Price         int64       `json:"price"`
	Question      []TextStep  `json:"question"`
	Answer        []TextStep  `json:"answer"`
	Right         []string    `json:"right"`
	SelectionMode string      `json:"selection_mode,omitempty"`
	PriceRange    *PriceRange `json:"price_range,omitempty"`
	AnswerSeconds int         `json:"answer_seconds,omitempty"`
}
type ThemeSpec struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	QuestionID string `json:"question_id"`
}
type RoundSpec struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	Type         string      `json:"type"`
	Themes       []ThemeSpec `json:"themes"`
	MinimumPrice int64       `json:"minimum_price"`
	MaximumPrice int64       `json:"maximum_price"`
}
type Stage struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	InstanceID  string `json:"instance_id,omitempty"`
	DeadlineMS  int64  `json:"deadline_ms,omitempty"`
	RemainingMS int64  `json:"remaining_ms,omitempty"`
	Expired     bool   `json:"expired"`
	TurnID      string `json:"turn_id,omitempty"`
}
type Decision struct {
	ID            string `json:"id"`
	ParticipantID string `json:"participant_id"`
	Correct       bool   `json:"correct"`
	Delta         int64  `json:"delta"`
	Reason        string `json:"reason"`
	QuestionID    string `json:"question_id"`
	InstanceID    string `json:"instance_id,omitempty"`
	Supersedes    string `json:"supersedes,omitempty"`
}
type Appeal struct {
	ID            string `json:"id"`
	JudgementID   string `json:"judgement_id"`
	ParticipantID string `json:"participant_id"`
	Reason        string `json:"reason"`
	Resolved      bool   `json:"resolved"`
	Resolution    string `json:"resolution,omitempty"`
}
type AuctionState struct {
	LeaderID  string   `json:"leader_id"`
	Amount    int64    `json:"amount"`
	AllIn     bool     `json:"all_in"`
	ActiveIDs []string `json:"active_ids"`
	Order     []string `json:"order"`
	Step      int64    `json:"step"`
}
type FinalState struct {
	Themes      []ThemeSpec `json:"themes"`
	EligibleIDs []string    `json:"eligible_ids"`
	Order       []string    `json:"order"`
	Turn        int         `json:"turn"`
}
type Submission struct {
	ID             string  `json:"id"`
	Maximum        int64   `json:"maximum"`
	Wager          *int64  `json:"wager,omitempty"`
	Answer         *string `json:"answer,omitempty"`
	Revealed       bool    `json:"revealed"`
	Correct        *bool   `json:"correct,omitempty"`
	Attempted      bool    `json:"attempted"`
	BlockedUntilMS int64   `json:"blocked_until_ms,omitempty"`
}
type ActiveQuestion struct {
	SkipReason       string        `json:"skip_reason,omitempty"`
	Spec             QuestionSpec  `json:"spec"`
	InstanceID       string        `json:"instance_id"`
	Rules            Rules         `json:"rules"`
	RulesVersion     int64         `json:"rules_version"`
	Players          []Submission  `json:"players"`
	RecipientID      string        `json:"recipient_id,omitempty"`
	Price            int64         `json:"price"`
	ContentIndex     int           `json:"content_index"`
	AnswerIndex      int           `json:"answer_index"`
	AfterAnswerKind  string        `json:"after_answer_kind,omitempty"`
	AfterAnswerTurn  string        `json:"after_answer_turn,omitempty"`
	Shown            bool          `json:"shown"`
	AnswerRevealed   bool          `json:"answer_revealed"`
	CollectionClosed bool          `json:"collection_closed"`
	Judged           bool          `json:"judged"`
	Candidates       []string      `json:"candidates,omitempty"`
	Seed             uint64        `json:"seed,omitempty"`
	Auction          *AuctionState `json:"auction,omitempty"`
	ChooserBefore    string        `json:"chooser_before"`
	CorrectedIDs     []string      `json:"corrected_ids,omitempty"`
}

// Session is private persisted state; transport must use SessionView instead.
type Session struct {
	Content      *ContentState   `json:"content,omitempty"`
	Version      int             `json:"version"`
	RulesVersion int64           `json:"rules_version"`
	Rules        Rules           `json:"rules"`
	ChooserID    string          `json:"chooser_id"`
	Stage        Stage           `json:"stage"`
	Counter      uint64          `json:"counter"`
	PausedTimer  bool            `json:"paused_timer,omitempty"`
	PausedAtMS   int64           `json:"paused_at_ms,omitempty"`
	Round        *RoundSpec      `json:"round,omitempty"`
	Question     *ActiveQuestion `json:"question,omitempty"`
	Final        *FinalState     `json:"final,omitempty"`
	Decisions    []Decision      `json:"decisions"`
	Appeals      []Appeal        `json:"appeals"`
}

type RoundView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}
type QuestionView struct {
	Media        *MediaView `json:"media,omitempty"`
	ID           string     `json:"id"`
	InstanceID   string     `json:"instance_id"`
	Type         string     `json:"type"`
	Theme        string     `json:"theme"`
	Price        int64      `json:"price"`
	Text         string     `json:"text"`
	Answer       *string    `json:"answer,omitempty"`
	ContentIndex int        `json:"content_index"`
	ContentTotal int        `json:"content_total"`
	AnswerIndex  int        `json:"answer_index"`
	AnswerTotal  int        `json:"answer_total"`
}
type PlayerView struct {
	ID              string `json:"id"`
	WagerSubmitted  bool   `json:"wager_submitted"`
	AnswerSubmitted bool   `json:"answer_submitted"`
	Judged          bool   `json:"judged"`
	BlockedUntilMS  int64  `json:"blocked_until_ms,omitempty"`
	Attempted       bool   `json:"attempted"`
}
type SelfView struct {
	Wager  *int64  `json:"wager,omitempty"`
	Answer *string `json:"answer,omitempty"`
}
type SubmissionView struct {
	ID       string  `json:"id"`
	Answer   *string `json:"answer,omitempty"`
	Wager    *int64  `json:"wager,omitempty"`
	Revealed bool    `json:"revealed"`
	Correct  *bool   `json:"correct,omitempty"`
}
type AuctionView struct {
	LeaderID  string   `json:"leader_id"`
	Amount    int64    `json:"amount"`
	AllIn     bool     `json:"all_in"`
	ActiveIDs []string `json:"active_ids"`
}
type ThemeView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type FinalView struct {
	Themes      []ThemeView `json:"themes"`
	EligibleIDs []string    `json:"eligible_ids"`
	Order       []string    `json:"order"`
}
type AllowedAction struct {
	Type        string   `json:"type"`
	Minimum     *int64   `json:"minimum,omitempty"`
	Maximum     *int64   `json:"maximum,omitempty"`
	Step        *int64   `json:"step,omitempty"`
	Modes       []string `json:"modes,omitempty"`
	Targets     []string `json:"targets,omitempty"`
	RequiresSeq bool     `json:"requires_seq,omitempty"`
}
type SessionView struct {
	Readiness      []Readiness      `json:"readiness"`
	Version        int              `json:"version"`
	RulesVersion   int64            `json:"rules_version"`
	Rules          Rules            `json:"rules"`
	ChooserID      string           `json:"chooser_id"`
	ServerTimeMS   int64            `json:"server_time_ms"`
	Stage          Stage            `json:"stage"`
	Round          *RoundView       `json:"round,omitempty"`
	Question       *QuestionView    `json:"question,omitempty"`
	Players        []PlayerView     `json:"players"`
	Self           SelfView         `json:"self"`
	Submissions    []SubmissionView `json:"submissions"`
	Auction        *AuctionView     `json:"auction,omitempty"`
	Final          *FinalView       `json:"final,omitempty"`
	Decisions      []Decision       `json:"decisions"`
	Appeals        []Appeal         `json:"appeals"`
	AllowedActions []AllowedAction  `json:"allowed_actions"`
}

// Each command has an exact payload. Pointer booleans/numbers distinguish omission.
type EmptyPayload struct{}
type BeginRoundPayload struct {
	Round RoundSpec `json:"round"`
}
type BeginQuestionPayload struct {
	Question QuestionSpec `json:"question"`
	Replay   bool         `json:"replay,omitempty"`
}
type RulesPayload struct {
	Rules Rules `json:"rules"`
}
type StagePayload struct {
	StageID    string `json:"stage_id"`
	InstanceID string `json:"instance_id"`
}
type ExtendPayload struct {
	StageID string `json:"stage_id"`
	Seconds int    `json:"seconds"`
}
type RestartPayload struct {
	StageID string `json:"stage_id"`
	Reason  string `json:"reason"`
}
type AnswerPayload struct {
	StageID    string `json:"stage_id"`
	InstanceID string `json:"instance_id"`
	Text       string `json:"text"`
}
type AmountPayload struct {
	StageID    string `json:"stage_id"`
	InstanceID string `json:"instance_id"`
	Amount     *int64 `json:"amount"`
}
type BidPayload struct {
	StageID    string `json:"stage_id"`
	InstanceID string `json:"instance_id"`
	Mode       string `json:"mode"`
	Amount     *int64 `json:"amount,omitempty"`
}
type RecipientPayload struct {
	StageID    string `json:"stage_id"`
	InstanceID string `json:"instance_id"`
	TargetID   string `json:"target_id"`
}
type EliminatePayload struct {
	StageID string `json:"stage_id"`
	ThemeID string `json:"theme_id"`
}
type RevealPayload struct {
	StageID    string `json:"stage_id"`
	InstanceID string `json:"instance_id"`
	TargetID   string `json:"target_id,omitempty"`
}
type JudgePayload struct {
	StageID    string `json:"stage_id"`
	InstanceID string `json:"instance_id"`
	TargetID   string `json:"target_id"`
	Correct    *bool  `json:"correct"`
}
type CorrectionPayload struct {
	JudgementID string `json:"judgement_id"`
	Correct     *bool  `json:"correct"`
	Reason      string `json:"reason"`
}
type AdjustPayload struct {
	TargetID string `json:"target_id"`
	Delta    *int64 `json:"delta"`
	Reason   string `json:"reason"`
}
type AppealPayload struct {
	JudgementID string `json:"judgement_id"`
	Reason      string `json:"reason"`
}
type ResolveAppealPayload struct {
	AppealID string `json:"appeal_id"`
	Reason   string `json:"reason"`
}
type TransferPayload struct {
	TargetID string `json:"target_id"`
}

// MediaVariant and MediaSpec are private immutable prepared-file metadata.
// Blob keys are never sent through a room projection.
type MediaVariant struct {
	Key  string `json:"key"`
	MIME string `json:"mime"`
	Size int64  `json:"size"`
}
type MediaSpec struct {
	ID         string       `json:"id"`
	Kind       string       `json:"kind"`
	DurationMS int64        `json:"duration_ms"`
	Width      int          `json:"width"`
	Height     int          `json:"height"`
	Standard   MediaVariant `json:"standard"`
	Economy    MediaVariant `json:"economy"`
}
type MediaVariantView struct {
	MIME string `json:"mime"`
	Size int64  `json:"size"`
}
type MediaView struct {
	ID                    string           `json:"id"`
	Kind                  string           `json:"kind"`
	DurationMS            int64            `json:"duration_ms"`
	Width                 int              `json:"width"`
	Height                int              `json:"height"`
	Standard              MediaVariantView `json:"standard"`
	Economy               MediaVariantView `json:"economy"`
	StartAtMS             int64            `json:"start_at_ms"`
	PausedPositionMS      int64            `json:"paused_position_ms"`
	PlaybackEndPositionMS int64            `json:"playback_end_position_ms"`
	StoppedPositionMS     *int64           `json:"stopped_position_ms,omitempty"`
}
type Readiness struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// ContentState refers to exactly one currently authorized question/answer step.
type ContentState struct {
	Group             string      `json:"group"`
	Index             int         `json:"index"`
	ReturnStage       string      `json:"return_stage"`
	Readiness         []Readiness `json:"readiness"`
	StartAtMS         int64       `json:"start_at_ms"`
	PausedPositionMS  int64       `json:"paused_position_ms"`
	StoppedPositionMS *int64      `json:"stopped_position_ms,omitempty"`
}
type MediaReadyPayload struct {
	StageID    string `json:"stage_id"`
	InstanceID string `json:"instance_id"`
	Status     string `json:"status"`
}
type SkipQuestionPayload struct {
	StageID    string `json:"stage_id"`
	InstanceID string `json:"instance_id"`
	Reason     string `json:"reason"`
}
