package library

import (
	"strconv"
	"strings"

	"siplatform/internal/packs"
)

// Keep the migration backfill aligned with this projection. Only public card
// metadata and theme names are searchable; question content never enters it.
func catalogueMetadata(card Card, document packs.Document) (string, *int) {
	parts := []string{card.Name, card.Author}
	for _, round := range document.Rounds {
		for _, theme := range round.Themes {
			parts = append(parts, theme.Name)
		}
	}
	var age *int
	switch label := strings.Trim(card.Restriction, " \t\r\n"); label {
	case "0+", "6+", "12+", "16+", "18+":
		n, _ := strconv.Atoi(strings.TrimSuffix(label, "+"))
		age = &n
	}
	return strings.Join(parts, "\n"), age
}

func literalSearchPattern(query string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.TrimSpace(query)) + "%"
}
