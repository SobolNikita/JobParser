package models

import (
	"time"
)

type VacancyStatus string

const (
	StatusNew      VacancyStatus = "new"
	StatusSent     VacancyStatus = "sent"
	StatusFavorite VacancyStatus = "favorite"
	StatusRejected VacancyStatus = "rejected"
	StatusApplied  VacancyStatus = "applied"
)

// Vacancy represents a parsed job opening
type Vacancy struct {
	ID           string        `json:"id"`
	Source       string        `json:"source"` // "hh", "habr", "hirehi", "rabota_by", "telegram"
	ExternalID   string        `json:"external_id"`
	Title        string        `json:"title"`
	Company      string        `json:"company"`
	Salary       string        `json:"salary"`
	Location     string        `json:"location"`
	URL          string        `json:"url"`
	Description  string        `json:"description"`
	Requirements string        `json:"requirements"`
	PublishedAt  time.Time     `json:"published_at"`
	CreatedAt    time.Time     `json:"created_at"`

	// Match analysis for current user
	MatchPct      int           `json:"match_pct"`
	MatchAnalysis string        `json:"match_analysis"`
	ExpectedGrade string        `json:"expected_grade"`
	Status        VacancyStatus `json:"status"`
	CoverLetter   string        `json:"cover_letter,omitempty"`
}

// Resume holds the candidate's resume and parsed profile
type Resume struct {
	UserID       int64     `json:"user_id"`
	RawText      string    `json:"raw_text"`
	FileName     string    `json:"file_name"`
	Summary      string    `json:"summary"`
	Skills       []string  `json:"skills"`
	Seniority    string    `json:"seniority"` // Junior, Middle, Senior, Lead
	DesiredRole  string    `json:"desired_role"`
	TargetStack  string    `json:"target_stack"` // e.g. "Golang", "Python", "Frontend", etc.
	AuthToken        string    `json:"auth_token,omitempty"`
	IsAuthorized     bool      `json:"is_authorized"`
	IsPollingEnabled bool      `json:"is_polling_enabled"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// MatchResult is the output of AI matching between resume and vacancy
type MatchResult struct {
	Percentage    int      `json:"percentage"`
	MatchingSkills []string `json:"matching_skills"`
	MissingSkills  []string `json:"missing_skills"`
	Summary       string   `json:"summary"`
}

// AutoApplyFilter defines auto-application rules parsed from natural language
type AutoApplyFilter struct {
	Enabled           bool     `json:"enabled"`
	NaturalPrompt     string   `json:"natural_prompt"`
	MinMatchPct       int      `json:"min_match_pct"`
	MinSalaryRub      int      `json:"min_salary_rub"`
	MinSalaryUsd      int      `json:"min_salary_usd"`
	RemoteOnly        bool     `json:"remote_only"`
	Seniority         []string `json:"seniority"`
	BlacklistKeywords []string `json:"blacklist_keywords"`
	WhitelistKeywords []string `json:"whitelist_keywords"`
}

// UserSettings stores individual user configuration
type UserSettings struct {
	UserID          int64           `json:"user_id"`
	AutoApplyFilter AutoApplyFilter `json:"auto_apply_filter"`
	MinAlertMatch   int             `json:"min_alert_match"` // default e.g. 50%
}
