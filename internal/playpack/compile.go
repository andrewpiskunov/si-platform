package playpack

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"siplatform/internal/game"
	"siplatform/internal/packs"
)

type CompileError struct{ Issue packs.Issue }

func (e CompileError) Error() string { return e.Issue.Path + ": " + e.Issue.Message }
func problem(code, path, message string) error {
	return CompileError{Issue: packs.Issue{Severity: "error", Code: code, Path: path, Message: message}}
}
func Validate(doc packs.Document) []packs.Issue {
	issues := []packs.Issue{}
	for ri, r := range doc.Rounds {
		rp := fmt.Sprintf("rounds[%d]", ri)
		if _, err := roundSpec(r, rp); err != nil {
			issues = append(issues, err.(CompileError).Issue)
		}
		for ti, t := range r.Themes {
			for qi, q := range t.Questions {
				_, err := compileQuestion(r, t, q, fmt.Sprintf("%s.themes[%d].questions[%d]", rp, ti, qi))
				if err != nil {
					issues = append(issues, err.(CompileError).Issue)
				}
			}
		}
	}
	return issues
}
func Compile(doc packs.Document, questionID string) (game.QuestionSpec, error) {
	for ri, r := range doc.Rounds {
		for ti, t := range r.Themes {
			for qi, q := range t.Questions {
				if q.ID == questionID {
					rp := fmt.Sprintf("rounds[%d]", ri)
					if _, err := roundSpec(r, rp); err != nil {
						return game.QuestionSpec{}, err
					}
					return compileQuestion(r, t, q, fmt.Sprintf("%s.themes[%d].questions[%d]", rp, ti, qi))
				}
			}
		}
	}
	return game.QuestionSpec{}, game.Fault("question_not_found")
}
func Round(doc packs.Document, roundID string) (game.RoundSpec, error) {
	for i, r := range doc.Rounds {
		if r.ID == roundID {
			return roundSpec(r, fmt.Sprintf("rounds[%d]", i))
		}
	}
	return game.RoundSpec{}, game.Fault("round_not_found")
}
func roundSpec(r packs.Round, path string) (game.RoundSpec, error) {
	out := game.RoundSpec{ID: r.ID, Name: r.Name, Type: r.Type, Themes: []game.ThemeSpec{}}
	if out.Type == "" {
		out.Type = "standard"
	}
	if !textFits(r.Name, 256) {
		return out, problem("game_limit", path+".name", "Название раунда превышает 256 байт представления")
	}
	if !game.ValidID(r.ID) {
		return out, problem("game_id", path+".id", "Для игры нужен корректный ID раунда")
	}
	if out.Type != "standard" && out.Type != "final" {
		return out, problem("game_round", path+".type", "Этот тип раунда не поддерживается")
	}
	if len(r.Themes) == 0 {
		return out, problem("game_themes", path+".themes", "В раунде нет тем")
	}
	seen := map[string]bool{}
	for ti, t := range r.Themes {
		tp := fmt.Sprintf("%s.themes[%d]", path, ti)
		if !textFits(t.Name, 256) {
			return out, problem("game_limit", tp+".name", "Название темы превышает 256 байт представления")
		}
		if !game.ValidID(t.ID) || seen[t.ID] {
			return out, problem("game_id", tp+".id", "Некорректный или повторяющийся ID темы")
		}
		seen[t.ID] = true
		if len(t.Questions) == 0 {
			return out, problem("game_questions", tp+".questions", "В теме нет вопросов")
		}
		out.Themes = append(out.Themes, game.ThemeSpec{ID: t.ID, Name: t.Name, QuestionID: t.Questions[0].ID})
		for _, q := range t.Questions {
			price := int64(q.Price)
			if price > 0 && (out.MinimumPrice == 0 || price < out.MinimumPrice) {
				out.MinimumPrice = price
			}
			if price > out.MaximumPrice {
				out.MaximumPrice = price
			}
		}
	}
	out.MinimumPrice = max(int64(1), out.MinimumPrice)
	out.MaximumPrice = max(int64(1), out.MaximumPrice)
	if out.Type == "final" {
		themes := []game.ThemeView{}
		for _, t := range out.Themes {
			themes = append(themes, game.ThemeView{ID: t.ID, Name: t.Name})
		}
		b, _ := json.Marshal(themes)
		if len(b) > 8192 {
			return out, problem("game_limit", path+".themes", "Список финальных тем превышает лимит 8192 байт представления")
		}
	}
	return out, nil
}
func compileQuestion(r packs.Round, t packs.Theme, q packs.Question, path string) (game.QuestionSpec, error) {
	out := game.QuestionSpec{ID: q.ID, RoundID: r.ID, ThemeID: t.ID, Theme: t.Name, Type: q.Type, Price: int64(q.Price), Question: []game.TextStep{}, Answer: []game.TextStep{}, Right: append([]string{}, q.Right...)}
	bad := func(code, field, message string) (game.QuestionSpec, error) {
		return out, problem(code, path+field, message)
	}
	if out.Type == "" {
		if r.Type == "final" {
			out.Type = "stakeAll"
		} else {
			out.Type = "withButton"
		}
	}
	if out.Type == "simple" {
		out.Type = "withButton"
	}
	if out.Type == "secret" {
		out.SelectionMode = "exceptCurrent"
	}
	if !game.ValidID(q.ID) {
		return bad("game_id", ".id", "Для игры нужен ID из 1–64 латинских букв, цифр, дефисов или подчёркиваний")
	}
	if q.HasScript {
		return bad("game_question_type", ".type", "Пользовательские сценарии не поддерживаются")
	}
	switch out.Type {
	case "withButton", "stake", "stakeAll", "secret", "forAll", "noRisk":
	default:
		return bad("game_question_type", ".type", "Этот тип вопроса пока нельзя провести в браузере")
	}
	if r.Type == "final" && out.Type != "stakeAll" {
		return bad("game_final_type", ".type", "В финале поддерживается общая скрытая ставка; оставьте тип по умолчанию или stakeAll")
	}
	if r.Type == "final" && q.Price == -1 {
		out.Price = 0
	}
	if out.Price < 0 || out.Price > 100000 {
		return bad("game_price", ".price", "Цена должна быть от 0 до 100000")
	}
	seen := map[string]bool{}
	for _, p := range q.Params {
		pp := ".params." + p.Name
		if seen[p.Name] {
			return bad("game_parameter", pp, "Параметр повторяется")
		}
		seen[p.Name] = true
		switch p.Name {
		case "question", "answer":
			if p.Type != "content" || p.Value != "" || p.NumberSet != nil || len(p.Params) > 0 {
				return bad("game_parameter", pp, "Текст должен быть параметром content")
			}
			steps := []game.TextStep{}
			length := 0
			for i, c := range p.Items {
				cp := fmt.Sprintf("%s.items[%d]", pp, i)
				if c.Type == "image" || c.Type == "audio" || c.Type == "video" {
					return bad("game_media_pending", cp, "Медиа требует подготовки перед игрой; текстовые правила уже доступны")
				}
				if c.Type != "text" || c.IsRef || c.Placement != "" && c.Placement != "screen" && c.Placement != "replic" || c.DurationMS < 0 || c.DurationMS > 600000 {
					return bad("game_content", cp, "Неподдерживаемый текстовый фрагмент")
				}
				length += len(c.Value)
				steps = append(steps, game.TextStep{Text: c.Value, DurationMS: c.DurationMS, WaitForFinish: c.WaitForFinish, Placement: c.Placement})
			}
			if length > 4096 {
				return bad("game_text", pp, "Текст для игры не должен превышать 4096 байт UTF-8")
			}
			if p.Name == "question" {
				out.Question = steps
			} else {
				out.Answer = steps
			}
		case "price":
			if out.Type != "secret" || p.Type != "numberSet" || p.NumberSet == nil || p.Value != "" || len(p.Params) > 0 || len(p.Items) > 0 {
				return bad("game_parameter", pp, "Цена секрета должна быть диапазоном numberSet")
			}
			n := p.NumberSet
			rangeValue := game.PriceRange{}
			if n.Minimum != nil {
				rangeValue.Minimum = int64(*n.Minimum)
			}
			if n.Maximum != nil {
				rangeValue.Maximum = int64(*n.Maximum)
			}
			if n.Step != nil {
				rangeValue.Step = int64(*n.Step)
			}
			if n.Value != "" || rangeValue.Minimum < 0 || rangeValue.Maximum < rangeValue.Minimum || rangeValue.Maximum > 100000 || rangeValue.Step < 0 {
				return bad("game_price_range", pp, "Недопустимый диапазон цены")
			}
			out.PriceRange = &rangeValue
		case "theme", "selectionMode", "answerDuration", "answerType":
			if p.Type != "" && p.Type != "simple" || p.NumberSet != nil || len(p.Items) > 0 || len(p.Params) > 0 {
				return bad("game_parameter", pp, "Ожидается простой параметр")
			}
			switch p.Name {
			case "theme":
				if out.Type != "secret" {
					return bad("game_parameter", pp, "Параметр темы применяется только к секрету")
				}
				if p.Value != "" {
					out.Theme = p.Value
				}
			case "selectionMode":
				if out.Type != "secret" || p.Value != "any" && p.Value != "exceptCurrent" {
					return bad("game_parameter", pp, "Выбор получателя: any или exceptCurrent")
				}
				out.SelectionMode = p.Value
			case "answerDuration":
				seconds, err := strconv.Atoi(p.Value)
				if err != nil || seconds < 5 || seconds > 120 {
					return bad("game_answer_duration", pp, "Время ответа должно быть целым числом секунд от 5 до 120")
				}
				out.AnswerSeconds = seconds
			case "answerType":
				if p.Value != "" && p.Value != "text" {
					return bad("game_parameter", pp, "Поддерживается текстовый ответ")
				}
			}
		default:
			return bad("game_parameter", pp, "Дополнительный параметр не поддерживается игровыми правилами")
		}
	}
	textLength := 0
	for _, s := range out.Question {
		textLength += len(strings.TrimSpace(s.Text))
	}
	if textLength == 0 {
		return bad("game_text", ".params.question", "Нужен текст вопроса")
	}
	answer := strings.Join(q.Right, " / ")
	if !textFits(out.Theme, 256) {
		return bad("game_limit", ".params.theme", "Название темы превышает 256 байт представления")
	}
	questionTexts, answerTexts := []string{}, []string{}
	for _, step := range out.Question {
		questionTexts = append(questionTexts, step.Text)
	}
	for _, step := range out.Answer {
		answerTexts = append(answerTexts, step.Text)
	}
	if !textFits(strings.Join(questionTexts, "\n"), 4096) || !textFits(strings.Join(answerTexts, "\n"), 4096) || !textFits(answer, 4096) {
		return bad("game_text", ".params", "Текст вопроса, ответа или пояснения превышает 4096 байт JSON-представления")
	}
	if len(answer) > 4096 || strings.TrimSpace(answer) == "" && len(out.Answer) == 0 {
		return bad("game_answer", ".right", "Нужен ответ размером до 4096 байт UTF-8")
	}
	return out, nil
}

func textFits(s string, limit int) bool {
	if len(s) > limit {
		return false
	}
	b, _ := json.Marshal(s)
	return len(b) <= limit+2
}
