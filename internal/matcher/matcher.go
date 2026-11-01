package matcher

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"jobparser/internal/ai"
	"jobparser/internal/browser"
	"jobparser/internal/models"
	"jobparser/internal/resume"
	"jobparser/internal/storage"
)

type Matcher struct {
	storage *storage.Storage
	ai      *ai.GeminiClient
	chrome  *browser.ChromeManager
}

func NewMatcher(st *storage.Storage, gemini *ai.GeminiClient, chrome *browser.ChromeManager) *Matcher {
	return &Matcher{
		storage: st,
		ai:      gemini,
		chrome:  chrome,
	}
}

type ProcessResult struct {
	Vacancy     *models.Vacancy
	MatchResult *models.MatchResult
	AutoApplied bool
	ApplyMsg    string
	Error       error
}

// DetectVacancyGrade analyzes title, requirements and description to determine expected grade
func DetectVacancyGrade(title, requirements, description string) string {
	return models.DetectGrade(title, requirements, description)
}

// CalculateRuleBasedMatch computes an intelligent heuristic match score and skills breakdown
func CalculateRuleBasedMatch(res *models.Resume, v *models.Vacancy) *models.MatchResult {
	vacText := strings.ToLower(v.Title + " " + v.Requirements + " " + v.Description)
	titleLower := strings.ToLower(v.Title)

	// Determine vacancy expected grade
	vacGrade := DetectVacancyGrade(v.Title, v.Requirements, v.Description)
	v.ExpectedGrade = vacGrade

	score := 40 // Base score for Go vacancies

	// 1. Role / Title Relevance
	if strings.Contains(titleLower, "go") || strings.Contains(titleLower, "golang") {
		score += 20
	} else if strings.Contains(vacText, "golang") || strings.Contains(vacText, " go ") || strings.Contains(vacText, " go,") {
		score += 10
	}

	// 2. Skill Intersections
	candidateSkills := res.Skills
	if len(candidateSkills) == 0 {
		candidateSkills = resume.ExtractSkills(res.RawText)
	}

	var matched []string
	var missing []string

	// Check core Go ecosystem skills in vacancy
	coreSkills := []string{"PostgreSQL", "Docker", "Kubernetes", "gRPC", "Redis", "Kafka", "RabbitMQ", "CI/CD", "Linux", "Microservices", "ClickHouse"}
	for _, cs := range coreSkills {
		csLower := strings.ToLower(cs)
		if strings.Contains(vacText, csLower) {
			hasInResume := false
			for _, userSk := range candidateSkills {
				if strings.EqualFold(userSk, cs) || strings.Contains(strings.ToLower(userSk), csLower) {
					hasInResume = true
					break
				}
			}
			if hasInResume {
				matched = append(matched, cs)
				score += 5
			} else {
				missing = append(missing, cs)
			}
		}
	}

	// 3. Seniority Matching with Strict Grade Enforcement
	userLevel := strings.ToLower(res.Seniority)
	isUserJunior := strings.Contains(userLevel, "junior") || strings.Contains(userLevel, "джун") || strings.Contains(userLevel, "стажер") || strings.Contains(userLevel, "intern")
	isUserSenior := strings.Contains(userLevel, "senior") || strings.Contains(userLevel, "сеньор") || strings.Contains(userLevel, "синиор")
	isUserLead := strings.Contains(userLevel, "lead") || strings.Contains(userLevel, "лид")

	var gradeComment string

	if isUserJunior {
		switch vacGrade {
		case "Architect", "Lead":
			score = 15 // Strict penalty for Junior viewing Architect/Lead
			gradeComment = fmt.Sprintf("⚠️ Требуется уровень %s (не подходит для Junior).", vacGrade)
		case "Senior":
			score = 25 // Strict penalty for Junior viewing Senior
			gradeComment = "⚠️ Вакансия для Senior (требуется глубокий опыт от 4-5 лет, не для Junior)."
		case "Middle / Senior":
			score = 35
			gradeComment = "⚠️ Уровень Middle/Senior (может быть завышена планка для Junior)."
		case "Middle":
			score = 55
			gradeComment = "🟡 Уровень Middle (для Junior возможен отклик на вырост)."
		case "Junior / Middle":
			score += 25
			gradeComment = "🟢 Отлично: позиция открыта для начинающих (Junior/Middle)!"
		case "Junior":
			score += 35
			gradeComment = "🟢 Отличный матч: прямое попадание в уровень Junior!"
		default:
			score += 10
			gradeComment = "Уровень не ограничен строго."
		}
	} else if isUserLead {
		switch vacGrade {
		case "Architect", "Lead":
			score += 25
			gradeComment = "🟣 Прямое попадание в уровень Lead/Architect!"
		case "Senior":
			score += 15
			gradeComment = "Уровень Senior подходит для Lead."
		default:
			score += 5
		}
	} else if isUserSenior {
		switch vacGrade {
		case "Senior":
			score += 25
			gradeComment = "🔴 Отличное попадание в уровень Senior!"
		case "Lead", "Architect":
			score += 10
			gradeComment = "Вакансия Lead/Architect — релевантна для Senior."
		case "Junior":
			score -= 10
			gradeComment = "Вакансия уровня Junior (оверквалифицирован)."
		default:
			score += 10
		}
	} else {
		// User is Middle
		switch vacGrade {
		case "Middle":
			score += 25
			gradeComment = "🟡 Прямое попадание в уровень Middle!"
		case "Junior / Middle", "Middle / Senior":
			score += 20
			gradeComment = "🟡 Подходящий уровень Middle."
		case "Architect", "Lead":
			score -= 15
			gradeComment = "⚠️ Вакансия уровня Lead/Architect."
		case "Senior":
			score += 5
			gradeComment = "Позиция Senior на вырост."
		}
	}

	// Cap score
	if score > 98 {
		score = 98
	}
	if score < 15 {
		score = 15
	}

	// Build summary
	var summaryParts []string
	if gradeComment != "" {
		summaryParts = append(summaryParts, gradeComment)
	}
	if len(matched) > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("Стек: %s.", strings.Join(matched, ", ")))
	}
	if len(missing) > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("Дополнительно: %s.", strings.Join(missing, ", ")))
	}

	summary := strings.Join(summaryParts, " ")
	if summary == "" {
		summary = "Базовое совпадение по разработке на Go."
	}

	return &models.MatchResult{
		Percentage:     score,
		MatchingSkills: matched,
		MissingSkills:  missing,
		Summary:        summary,
	}
}

// EvaluateVacancy matches a vacancy for a user and checks auto-apply
func (m *Matcher) EvaluateVacancy(ctx context.Context, userID int64, v *models.Vacancy) (*ProcessResult, error) {
	r, err := m.storage.GetResume(userID)
	if err != nil || r == nil || len(r.RawText) == 0 {
		return nil, fmt.Errorf("user %d has no resume loaded", userID)
	}

	// If resume skills weren't populated previously, extract them now
	if len(r.Skills) == 0 {
		r.Skills = resume.ExtractSkills(r.RawText)
		r.Seniority = resume.ExtractSeniority(r.RawText)
		_ = m.storage.SaveResume(r)
	}

	// 1. Calculate intelligent rule-based match first
	ruleMatch := CalculateRuleBasedMatch(r, v)
	matchRes := ruleMatch

	// 2. Try Gemini AI evaluation with dedicated timeout and strict grade awareness
	aiCtx, aiCancel := context.WithTimeout(ctx, 20*time.Second)
	aiMatch, err := m.ai.MatchJob(aiCtx, r.RawText, v)
	aiCancel()

	if err == nil && aiMatch != nil && aiMatch.Percentage > 0 {
		// Strict check: if user is Junior and vacancy is Senior/Lead, don't let AI give a high score
		userLevelLower := strings.ToLower(r.Seniority)
		isUserJunior := strings.Contains(userLevelLower, "junior") || strings.Contains(userLevelLower, "джун")
		if isUserJunior && (v.ExpectedGrade == "Senior" || v.ExpectedGrade == "Lead" || v.ExpectedGrade == "Architect") {
			if aiMatch.Percentage > 30 {
				aiMatch.Percentage = 25
			}
		}

		// Blend AI evaluation with rule match (70% AI, 30% rule-based for stability)
		blendedPct := int(float64(aiMatch.Percentage)*0.7 + float64(ruleMatch.Percentage)*0.3)
		if isUserJunior && (v.ExpectedGrade == "Senior" || v.ExpectedGrade == "Lead" || v.ExpectedGrade == "Architect") && blendedPct > 30 {
			blendedPct = 25
		}
		if blendedPct > 98 {
			blendedPct = 98
		}
		if blendedPct < 15 {
			blendedPct = 15
		}
		aiMatch.Percentage = blendedPct
		if aiMatch.Summary != "" {
			matchRes = aiMatch
		} else {
			aiMatch.Summary = ruleMatch.Summary
			matchRes = aiMatch
		}
	} else {
		log.Printf("[Matcher] Using rule-based match for %s: %d%% (%v)", v.ID, ruleMatch.Percentage, err)
	}

	v.MatchPct = matchRes.Percentage
	v.MatchAnalysis = matchRes.Summary

	// Save match state in DB
	_ = m.storage.SaveUserVacancyState(userID, v.ID, v.MatchPct, v.MatchAnalysis, models.StatusNew)

	res := &ProcessResult{
		Vacancy:     v,
		MatchResult: matchRes,
	}

	// 3. Check Auto-Apply filter
	filter, err := m.storage.GetAutoApplyFilter(userID)
	if err == nil && filter != nil && filter.Enabled {
		if m.checkAutoApplyCriteria(v, matchRes, filter) {
			log.Printf("[AutoApply] Vacancy %s satisfies criteria! Generating letter and applying...", v.ID)

			letterCtx, letterCancel := context.WithTimeout(ctx, 25*time.Second)
			letter, err := m.ai.GenerateCoverLetter(letterCtx, r.RawText, v)
			letterCancel()

			if err != nil || len(letter) == 0 {
				letter = ai.FallbackCoverLetter(r.RawText, v)
			}
			v.CoverLetter = letter
			_ = m.storage.UpdateCoverLetter(userID, v.ID, letter)

			// Apply via Chrome
			applyCtx, applyCancel := context.WithTimeout(ctx, 45*time.Second)
			applyRes, err := m.chrome.ApplyToVacancy(applyCtx, v, letter)
			applyCancel()

			if err != nil {
				log.Printf("[AutoApply ERROR] Apply failed: %v", err)
				res.ApplyMsg = fmt.Sprintf("Ошибка автоотклика: %v", err)
			} else {
				res.AutoApplied = applyRes.Success
				res.ApplyMsg = applyRes.Message
				if applyRes.Success {
					_ = m.storage.UpdateUserVacancyStatus(userID, v.ID, models.StatusApplied)
				}
			}
		}
	}

	return res, nil
}

func (m *Matcher) checkAutoApplyCriteria(v *models.Vacancy, mr *models.MatchResult, f *models.AutoApplyFilter) bool {
	// 1. Percentage check
	if mr.Percentage < f.MinMatchPct {
		return false
	}

	fullTextLower := strings.ToLower(v.Title + " " + v.Description + " " + v.Requirements)

	// 2. Blacklist check
	for _, bl := range f.BlacklistKeywords {
		bl = strings.TrimSpace(strings.ToLower(bl))
		if bl != "" && strings.Contains(fullTextLower, bl) {
			log.Printf("[AutoApply] Skipped %s due to blacklisted keyword: %s", v.ID, bl)
			return false
		}
	}

	// 3. Remote only check
	if f.RemoteOnly {
		isRemote := strings.Contains(fullTextLower, "удален") ||
			strings.Contains(fullTextLower, "remote") ||
			strings.Contains(strings.ToLower(v.Location), "удален")
		if !isRemote {
			return false
		}
	}

	// 4. Whitelist check (if any required)
	if len(f.WhitelistKeywords) > 0 {
		hasAny := false
		for _, wl := range f.WhitelistKeywords {
			wl = strings.TrimSpace(strings.ToLower(wl))
			if wl != "" && strings.Contains(fullTextLower, wl) {
				hasAny = true
				break
			}
		}
		if !hasAny {
			return false
		}
	}

	return true
}
