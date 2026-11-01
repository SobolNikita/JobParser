package models

import (
	"strings"
)

// DetectGrade analyzes title, requirements and description to determine expected grade
func DetectGrade(title, requirements, description string) string {
	combined := strings.ToLower(title + " " + requirements + " " + description)
	titleLower := strings.ToLower(title)

	// 1. Check title first (highest precision)
	if strings.Contains(titleLower, "architect") || strings.Contains(titleLower, "архитектор") {
		return "Architect"
	}
	if strings.Contains(titleLower, "team lead") || strings.Contains(titleLower, "tech lead") || strings.Contains(titleLower, "тимлид") || strings.Contains(titleLower, "техлид") || strings.Contains(titleLower, "руководитель") || strings.Contains(titleLower, "lead ") || strings.HasSuffix(titleLower, "lead") {
		return "Lead"
	}
	if strings.Contains(titleLower, "senior") || strings.Contains(titleLower, "сеньор") || strings.Contains(titleLower, "синиор") || strings.Contains(titleLower, "старший") {
		return "Senior"
	}
	if strings.Contains(titleLower, "junior") || strings.Contains(titleLower, "джун") || strings.Contains(titleLower, "стажер") || strings.Contains(titleLower, "стажёр") || strings.Contains(titleLower, "intern") || strings.Contains(titleLower, "trainee") || strings.Contains(titleLower, "младший") {
		return "Junior"
	}
	if strings.Contains(titleLower, "middle") || strings.Contains(titleLower, "мидл") {
		return "Middle"
	}

	// 2. Check full text indicators
	if strings.Contains(combined, "architect") || strings.Contains(combined, "архитектор") {
		return "Architect"
	}
	if strings.Contains(combined, "team lead") || strings.Contains(combined, "tech lead") || strings.Contains(combined, "тимлид") {
		return "Lead"
	}

	hasSenior := strings.Contains(combined, "senior") || strings.Contains(combined, "сеньор") || strings.Contains(combined, "синиор") || strings.Contains(combined, "опыт от 5") || strings.Contains(combined, "опыт от 6")
	hasJunior := strings.Contains(combined, "junior") || strings.Contains(combined, "джун") || strings.Contains(combined, "стажер") || strings.Contains(combined, "стажёр") || strings.Contains(combined, "intern") || strings.Contains(combined, "trainee") || strings.Contains(combined, "без опыта") || strings.Contains(combined, "начинающий") || strings.Contains(combined, "опыт от 0")
	hasMiddle := strings.Contains(combined, "middle") || strings.Contains(combined, "мидл") || strings.Contains(combined, "опыт от 2") || strings.Contains(combined, "опыт от 3")

	if hasJunior && !hasSenior {
		return "Junior"
	}
	if hasSenior && !hasJunior {
		return "Senior"
	}
	if hasMiddle && !hasSenior && !hasJunior {
		return "Middle"
	}
	if hasMiddle && hasSenior {
		return "Middle / Senior"
	}
	if hasJunior && hasMiddle {
		return "Junior / Middle"
	}

	return "Middle"
}

// GradeRank returns numerical rank for sorting (Junior=1, Lead=5)
func GradeRank(grade string) int {
	grade = strings.TrimSpace(grade)
	switch {
	case strings.Contains(grade, "Junior") && !strings.Contains(grade, "Middle"):
		return 1
	case strings.Contains(grade, "Junior") && strings.Contains(grade, "Middle"):
		return 2
	case strings.EqualFold(grade, "Middle"):
		return 3
	case strings.Contains(grade, "Middle") && strings.Contains(grade, "Senior"):
		return 4
	case strings.Contains(grade, "Senior"):
		return 5
	case strings.Contains(grade, "Lead") || strings.Contains(grade, "Architect"):
		return 6
	default:
		return 3
	}
}
