package models

import (
	"strings"
)

// NonITStopWords contains titles and professions that must NEVER be treated as IT engineering vacancies
var NonITStopWords = []string{
	"бухгалтер", "бухгалтери", "экономист", "аудитор", "финансист",
	"юрист", "юрисконсульт", "адвокат", "водитель", "курьер",
	"повар", "официант", "бармен", "уборщик", "клинер", "кассир",
	"продавец", "мерчендайзер", "маркетолог", "копирайтер", "smm",
	"таргетолог", "рекрутер", "подбор персонала", "дизайнер", "дизайн",
	"менеджер по продажам", "оператор", "секретарь", "ассистент",
	"делопроизводитель", "агроном", "врач", "медсестра", "учитель",
	"строитель", "монтажник", "сварщик", "кладовщик", "комплектовщик",
	"риелтор", "закупщик", "логист", "товаровед", "эксперт по недвижимости",
	"менеджер по работе с клиентами", "менеджер проектов не it", "контент-менеджер",
}

// IsRelevantToStack checks whether a vacancy is actually relevant to the target tech stack
func IsRelevantToStack(stack, title, description, requirements string) bool {
	titleLower := strings.ToLower(title)
	combinedLower := strings.ToLower(title + " " + description + " " + requirements)

	// 1. Immediately drop any non-IT stop-word matches in title
	for _, stop := range NonITStopWords {
		if strings.Contains(titleLower, stop) {
			return false
		}
	}

	stk := strings.ToLower(strings.TrimSpace(stack))
	if stk == "" || strings.Contains(stk, "golang") || strings.Contains(stk, "go") {
		// Specific check for Golang / Go
		hasGoTitle := strings.Contains(titleLower, "golang") ||
			strings.Contains(titleLower, "go developer") ||
			strings.Contains(titleLower, "go разработчик") ||
			strings.Contains(titleLower, "go-разработчик") ||
			strings.Contains(titleLower, "go backend") ||
			strings.Contains(titleLower, "go бэкенд") ||
			strings.Contains(titleLower, "разработчик go") ||
			strings.Contains(titleLower, "программист go") ||
			strings.Contains(titleLower, "golang-разработчик") ||
			strings.Contains(titleLower, "(go)") ||
			strings.Contains(titleLower, "/go") ||
			strings.Contains(titleLower, "go/")

		if hasGoTitle {
			return true
		}

		// Generic Backend/Developer titles: must mention golang or go in description or requirements
		isDevTitle := strings.Contains(titleLower, "разработчик") ||
			strings.Contains(titleLower, "developer") ||
			strings.Contains(titleLower, "engineer") ||
			strings.Contains(titleLower, "инженер") ||
			strings.Contains(titleLower, "backend") ||
			strings.Contains(titleLower, "бэкенд") ||
			strings.Contains(titleLower, "программист")

		if isDevTitle {
			hasGoBody := strings.Contains(combinedLower, "golang") ||
				strings.Contains(combinedLower, " go ") ||
				strings.Contains(combinedLower, "go,") ||
				strings.Contains(combinedLower, "go.") ||
				strings.Contains(combinedLower, "(go)") ||
				strings.Contains(combinedLower, "go/") ||
				strings.Contains(combinedLower, "/go")
			return hasGoBody
		}

		return false
	}

	// For other stacks (Python, Frontend, Java, etc.)
	return strings.Contains(titleLower, stk) || strings.Contains(combinedLower, stk)
}
