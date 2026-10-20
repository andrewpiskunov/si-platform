// Package playpack translates the supported SIQ profile into game commands.
// Its diagnostics describe engine capabilities, separately from SIQ validity.
package playpack

import (
	"fmt"
	"strings"

	"siplatform/internal/game"
	"siplatform/internal/packs"
)

func ValidateLegacy(doc packs.Document) []packs.Issue {
	issues := []packs.Issue{}
	for ri, round := range doc.Rounds {
		rp := fmt.Sprintf("rounds[%d]", ri)
		if round.Type != "" && round.Type != "standard" {
			issues = append(issues, packs.Issue{Severity: "error", Code: "game_round", Path: rp + ".type", Message: "Этот тип раунда пока нельзя провести в браузере"})
		}
		for ti, theme := range round.Themes {
			for qi, q := range theme.Questions {
				issues = append(issues, questionIssues(q, fmt.Sprintf("%s.themes[%d].questions[%d]", rp, ti, qi))...)
			}
		}
	}
	return issues
}
func questionIssues(q packs.Question, path string) []packs.Issue {
	issues := []packs.Issue{}
	add := func(code, field, message string) {
		issues = append(issues, packs.Issue{Severity: "error", Code: code, Path: path + field, Message: message})
	}
	if !game.ValidID(q.ID) {
		add("game_id", ".id", "Для игры нужен ID из 1–64 латинских букв, цифр, дефисов или подчёркиваний")
	}
	if q.Type != "" && q.Type != "simple" && q.Type != "withButton" || q.HasScript {
		add("game_question_type", ".type", "Этот тип вопроса пока нельзя провести в браузере")
	}
	if q.Price < 1 || q.Price > 100000 {
		add("game_price", ".price", "Для текущих правил игры укажите цену от 1 до 100000")
	}
	for _, p := range q.Params {
		// Additional parameters affect presentation or timing; do not silently ignore them.
		if p.Name != "question" {
			add("game_parameter", ".params."+p.Name, "Дополнительный параметр пока не поддерживается игровой комнатой")
		}
	}
	var lines []string
	for _, item := range q.Content("question") {
		if item.Type != "text" || item.IsRef || item.Placement != "" && item.Placement != "screen" || item.DurationMS != 0 || item.WaitForFinish {
			add("game_content", ".params.question", "Пока поддерживается обычный текст на экране без медиа и тайминга")
			break
		}
		lines = append(lines, item.Value)
	}
	text := strings.Join(lines, "\n")
	if strings.TrimSpace(text) == "" || len(text) > 4096 {
		add("game_text", ".params.question", "Текст вопроса для игры должен занимать от 1 до 4096 байт UTF-8")
	}
	answer := strings.Join(q.Right, " / ")
	if strings.TrimSpace(answer) == "" || len(answer) > 4096 {
		add("game_answer", ".right", "Ответ для игры должен занимать от 1 до 4096 байт UTF-8")
	}
	return issues
}
func Command(round packs.Round, q packs.Question, id string, seq int64) (game.Command, error) {
	if round.Type != "" && round.Type != "standard" || !packs.Playable(questionIssues(q, "question")) {
		return game.Command{}, game.Fault("unsupported_question")
	}
	var lines []string
	for _, item := range q.Content("question") {
		lines = append(lines, item.Value)
	}
	cmd := game.Command{Version: 1, ID: id, Type: "open_question", QuestionID: q.ID, Text: strings.Join(lines, "\n"), Answer: strings.Join(q.Right, " / "), Value: int64(q.Price), ExpectedSeq: &seq}
	return cmd, cmd.Validate()
}
