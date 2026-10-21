package game

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

func (c Command) Payload() (any, error) {
	invalid := Fault("invalid_command")
	if c.Version != 2 || !ValidID(c.ID) || c.QuestionID != "" || c.Text != "" || c.Answer != "" || c.Value != 0 || c.Correct != nil || c.ExpectedSeq != nil && *c.ExpectedSeq < 1 {
		return nil, invalid
	}
	raw := bytes.TrimSpace(c.Data)
	if len(raw) < 2 || raw[0] != '{' || raw[len(raw)-1] != '}' {
		return nil, invalid
	}
	var p any
	switch c.Type {
	case "remove_participant":
		p = &RemovalPayload{}
	case "begin_round":
		p = &BeginRoundPayload{}
	case "begin_question":
		p = &BeginQuestionPayload{}
	case "set_rules":
		p = &RulesPayload{}
	case "set_appearance":
		p = &AppearancePayload{}
	case "set_room_settings":
		p = &SettingsPayload{}
	case "pause", "resume", "finish":
		p = &EmptyPayload{}
	case "extend":
		p = &ExtendPayload{}
	case "restart_stage":
		p = &RestartPayload{}
	case "advance", "open_buzzer", "buzz", "continue_media", "wait_media":
		p = &StagePayload{}
	case "media_ready":
		p = &MediaReadyPayload{}
	case "skip_question":
		p = &SkipQuestionPayload{}
	case "answer":
		p = &AnswerPayload{}
	case "wager", "select_price":
		p = &AmountPayload{}
	case "bid":
		p = &BidPayload{}
	case "select_recipient":
		p = &RecipientPayload{}
	case "eliminate_theme":
		p = &EliminatePayload{}
	case "reveal":
		p = &RevealPayload{}
	case "judge":
		p = &JudgePayload{}
	case "correct_judgement":
		p = &CorrectionPayload{}
	case "adjust_score":
		p = &AdjustPayload{}
	case "appeal":
		p = &AppealPayload{}
	case "resolve_appeal":
		p = &ResolveAppealPayload{}
	case "transfer_host":
		p = &TransferPayload{}
	default:
		return nil, invalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(p) != nil || d.Decode(new(any)) != io.EOF {
		return nil, invalid
	}
	valid := true
	switch x := p.(type) {
	case *RemovalPayload:
		valid = c.ExpectedSeq != nil && ValidID(x.TargetID) && (x.Mode == "kick" || x.Mode == "ban_guest_session")
	case *SettingsPayload:
		valid = c.ExpectedSeq != nil && x.Settings.Validate() == nil
	case *AppearancePayload:
		valid = c.ExpectedSeq != nil && x.Appearance.Validate() == nil
	case *BeginRoundPayload:
		valid = ValidID(x.Round.ID)
	case *BeginQuestionPayload:
		valid = ValidID(x.Question.ID)
	case *ExtendPayload:
		valid = ValidID(x.StageID) && x.Seconds >= 5 && x.Seconds <= 120
	case *RestartPayload:
		valid = ValidID(x.StageID) && validReason(x.Reason)
	case *StagePayload:
		valid = validStage(x.StageID, x.InstanceID)
	case *MediaReadyPayload:
		valid = validStage(x.StageID, x.InstanceID) && (x.Status == "loading" || x.Status == "ready" || x.Status == "error" || x.Status == "hidden")
	case *SkipQuestionPayload:
		valid = validStage(x.StageID, x.InstanceID) && validReason(x.Reason)
	case *AnswerPayload:
		valid = validStage(x.StageID, x.InstanceID) && strings.TrimSpace(x.Text) != "" && serializedTextFits(x.Text, 1024)
	case *AmountPayload:
		valid = validStage(x.StageID, x.InstanceID) && x.Amount != nil && *x.Amount >= 0 && *x.Amount <= 1000000000
	case *BidPayload:
		valid = validStage(x.StageID, x.InstanceID) && (x.Mode == "bid" && x.Amount != nil && *x.Amount >= 0 && *x.Amount <= 1000000000 || (x.Mode == "pass" || x.Mode == "all_in") && x.Amount == nil)
	case *RecipientPayload:
		valid = validStage(x.StageID, x.InstanceID) && ValidID(x.TargetID)
	case *EliminatePayload:
		valid = ValidID(x.StageID) && ValidID(x.ThemeID)
	case *RevealPayload:
		valid = validStage(x.StageID, x.InstanceID) && (x.TargetID == "" || ValidID(x.TargetID))
	case *JudgePayload:
		valid = validStage(x.StageID, x.InstanceID) && ValidID(x.TargetID) && x.Correct != nil
	case *CorrectionPayload:
		valid = ValidID(x.JudgementID) && x.Correct != nil && validReason(x.Reason)
	case *AdjustPayload:
		valid = ValidID(x.TargetID) && x.Delta != nil && *x.Delta >= -1000000000 && *x.Delta <= 1000000000 && validReason(x.Reason)
	case *AppealPayload:
		valid = ValidID(x.JudgementID) && validReason(x.Reason)
	case *ResolveAppealPayload:
		valid = ValidID(x.AppealID) && validReason(x.Reason)
	case *TransferPayload:
		valid = ValidID(x.TargetID)
	}
	if !valid {
		return nil, invalid
	}
	return p, nil
}
func validStage(stage, instance string) bool { return ValidID(stage) && ValidID(instance) }
func validReason(s string) bool              { return strings.TrimSpace(s) != "" && len(s) <= 1024 }
