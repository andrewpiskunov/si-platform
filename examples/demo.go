// Package examples contains original demonstration content for local development.
package examples

import (
	"fmt"
	"siplatform/internal/packs"
)

func Demo() packs.Document {
	d := packs.Document{ID: "si-platform-demo", Name: "Первая игра", Version: "5.0", Language: "ru", Difficulty: 2, Tags: []string{"демо", "для друзей"}, Info: packs.Info{Authors: []string{"SI Platform — демонстрационный пакет"}, Comments: "Вопросы написаны для этого проекта. Можно изменять и распространять на условиях CC0."}}
	r := packs.Round{ID: "r1", Name: "Разминка"}
	themes := []struct {
		name      string
		questions [][2]string
	}{
		{"Числа", [][2]string{{"Сколько будет 7 × 8?", "56"}, {"Как называется число, равное отношению длины окружности к её диаметру?", "Пи"}, {"Сколько граней у куба?", "6"}}},
		{"Слова", [][2]string{{"Как называется слово, одинаково читающееся слева направо и справа налево?", "Палиндром"}, {"Как называется часть речи, обозначающая действие?", "Глагол"}, {"Какой знак препинания завершает обычный вопрос?", "Вопросительный знак"}}},
		{"Мир вокруг", [][2]string{{"Как называется естественный спутник Земли?", "Луна"}, {"Как называется переход воды из жидкого состояния в газообразное?", "Испарение"}, {"Какой химический элемент обозначают символом O?", "Кислород"}}},
	}
	for ti, src := range themes {
		theme := packs.Theme{ID: fmt.Sprintf("r1-t%d", ti+1), Name: src.name}
		for qi, item := range src.questions {
			theme.Questions = append(theme.Questions, packs.Question{ID: fmt.Sprintf("%s-q%d", theme.ID, qi+1), Price: (qi + 1) * 100, Type: "simple", Params: []packs.Parameter{{Name: "question", Type: "content", Items: []packs.Content{{Type: "text", Value: item[0]}}}}, Right: []string{item[1]}})
		}
		r.Themes = append(r.Themes, theme)
	}
	d.Rounds = []packs.Round{r}
	return d
}
