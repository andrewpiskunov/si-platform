package packs

import (
	"fmt"
	"strings"
)

func Validate(d Document) []Issue {
	issues := append([]Issue(nil), d.ImportIssues...)
	add := func(code, path, message string) { issues = append(issues, Issue{"error", code, path, message}) }
	required := func(value, path string) {
		if strings.TrimSpace(value) == "" {
			add("required", path, "Поле обязательно")
		}
	}
	required(d.Name, "name")
	if d.Version != "5.0" {
		add("unsupported_version", "version", "Модель должна использовать SIQ 5.0")
	}
	if len(d.Rounds) == 0 {
		add("required", "rounds", "Нужен хотя бы один раунд")
	}
	if d.Difficulty < 0 || d.Difficulty > 10 {
		add("invalid_difficulty", "difficulty", "Сложность должна быть от 0 до 10")
	}
	ids := map[string]bool{}
	count := 0
	for ri, r := range d.Rounds {
		rp := fmt.Sprintf("rounds[%d]", ri)
		required(r.Name, rp+".name")
		if r.Type != "" && r.Type != "standard" && r.Type != "final" {
			add("unsupported_round", rp+".type", "Неизвестный тип раунда")
		}
		if len(r.Themes) == 0 {
			add("required", rp+".themes", "В раунде нет тем")
		}
		for ti, t := range r.Themes {
			tp := fmt.Sprintf("%s.themes[%d]", rp, ti)
			required(t.Name, tp+".name")
			if len(t.Questions) == 0 {
				add("required", tp+".questions", "В теме нет вопросов")
			}
			for qi, q := range t.Questions {
				count++
				qp := fmt.Sprintf("%s.questions[%d]", tp, qi)
				required(q.ID, qp+".id")
				if ids[q.ID] {
					add("duplicate_id", qp+".id", "ID вопроса повторяется")
				}
				ids[q.ID] = true
				if q.Price < 0 && r.Type != "final" {
					add("invalid_price", qp+".price", "Цена обычного вопроса не может быть отрицательной")
				}
				switch q.Type {
				case "", "simple", "withButton", "stake", "stakeAll", "secret", "forAll", "noRisk":
				default:
					add("unsupported_type", qp+".type", "Тип вопроса вне поддержанного профиля: "+q.Type)
				}
				if q.HasScript {
					add("unsupported_script", qp, "Произвольные сценарии не исполняются")
				}
				if len(q.Content("question")) == 0 {
					add("required", qp+".params.question", "Нет содержимого вопроса")
				}
				hasAnswer := false
				for _, a := range q.Right {
					if strings.TrimSpace(a) != "" {
						hasAnswer = true
					}
				}
				if !hasAnswer {
					add("required", qp+".right", "Укажите правильный ответ для ведущего")
				}
				names := map[string]bool{}
				for _, p := range q.Params {
					pp := qp + ".params." + p.Name
					if names[p.Name] {
						add("duplicate_parameter", pp, "Параметр повторяется")
					}
					names[p.Name] = true
					switch p.Name {
					case "question", "answer", "theme", "price", "selectionMode", "answerDuration", "answerType":
					default:
						add("unsupported_parameter", pp, "Неизвестный параметр: "+p.Name)
					}
					if p.Name == "answerType" && p.Value != "" && p.Value != "text" {
						add("unsupported_answer_type", pp, "Поддерживается текстовый ответ")
					}
					validateParameter(p, pp, 0, add)
				}
			}
		}
	}
	if count > 2000 {
		add("question_limit", "rounds", "Допустимо не более 2000 вопросов")
	}
	return issues
}
func validateParameter(p Parameter, path string, depth int, add func(string, string, string)) {
	if depth >= 32 {
		add("parameter_depth", path, "Слишком глубокая вложенность параметров")
		return
	}
	switch p.Type {
	case "", "simple", "content", "group", "numberSet":
	default:
		add("unsupported_parameter_type", path, "Неизвестный тип параметра: "+p.Type)
	}
	if p.NumberSet != nil {
		n := p.NumberSet
		if (n.Minimum != nil && n.Maximum != nil && *n.Minimum > *n.Maximum) || (n.Step != nil && *n.Step <= 0) {
			add("invalid_number_set", path, "Недопустимый диапазон значений")
		}
	}
	for i, c := range p.Items {
		cp := fmt.Sprintf("%s.items[%d]", path, i)
		switch c.Type {
		case "text", "image", "audio", "video":
		default:
			add("unsupported_content", cp, "Неизвестный или исполняемый тип содержимого: "+c.Type)
		}
		if c.DurationMS < 0 || c.DurationMS > 600000 {
			add("invalid_duration", cp, "Длительность должна быть от 0 до 10 минут")
		}
		if c.Placement != "" && c.Placement != "screen" && c.Placement != "background" && c.Placement != "replic" {
			add("unsupported_placement", cp, "Неизвестное размещение содержимого")
		}
		if strings.TrimSpace(c.Value) == "" {
			add("required", cp, "Пустое содержимое")
		}
	}
	for _, child := range p.Params {
		validateParameter(child, path+"."+child.Name, depth+1, add)
	}
}
