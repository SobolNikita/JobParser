package storage

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"jobparser/internal/models"

	_ "github.com/glebarez/go-sqlite"
)

type Storage struct {
	db *sql.DB
	mu sync.RWMutex
}

func NewStorage(dbPath string) (*Storage, error) {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	s := &Storage{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return s, nil
}

func (s *Storage) Close() error {
	return s.db.Close()
}

func (s *Storage) migrate() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS users (
			user_id INTEGER PRIMARY KEY,
			resume_text TEXT DEFAULT '',
			resume_filename TEXT DEFAULT '',
			summary TEXT DEFAULT '',
			skills_json TEXT DEFAULT '[]',
			seniority TEXT DEFAULT '',
			desired_role TEXT DEFAULT '',
			target_stack TEXT DEFAULT 'Golang',
			auth_token TEXT DEFAULT '',
			is_authorized INTEGER DEFAULT 1,
			auto_apply_json TEXT DEFAULT '',
			updated_at DATETIME
		);`,
		`CREATE TABLE IF NOT EXISTS vacancies (
			id TEXT PRIMARY KEY,
			source TEXT,
			external_id TEXT,
			title TEXT,
			company TEXT,
			salary TEXT,
			location TEXT,
			url TEXT,
			description TEXT,
			requirements TEXT,
			published_at DATETIME,
			created_at DATETIME
		);`,
		`CREATE TABLE IF NOT EXISTS user_vacancies (
			user_id INTEGER,
			vacancy_id TEXT,
			match_pct INTEGER DEFAULT 0,
			match_analysis TEXT DEFAULT '',
			status TEXT DEFAULT 'new',
			cover_letter TEXT DEFAULT '',
			applied_at DATETIME,
			updated_at DATETIME,
			PRIMARY KEY (user_id, vacancy_id),
			FOREIGN KEY (vacancy_id) REFERENCES vacancies(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS channels (
			name TEXT PRIMARY KEY,
			added_at DATETIME
		);`,
	}

	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}

	// Safe alter table for existing databases
	_, _ = s.db.Exec("ALTER TABLE users ADD COLUMN target_stack TEXT DEFAULT 'Golang';")
	_, _ = s.db.Exec("ALTER TABLE users ADD COLUMN auth_token TEXT DEFAULT '';")
	_, _ = s.db.Exec("ALTER TABLE users ADD COLUMN is_authorized INTEGER DEFAULT 1;")
	_, _ = s.db.Exec("ALTER TABLE users ADD COLUMN is_polling_enabled INTEGER DEFAULT 0;")

	return nil
}

// SaveResume updates or inserts user's resume
func (s *Storage) SaveResume(resume *models.Resume) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if resume.TargetStack == "" {
		resume.TargetStack = "Golang"
	}
	authInt := 1
	if !resume.IsAuthorized {
		authInt = 0
	}

	skillsJSON, _ := json.Marshal(resume.Skills)
	query := `INSERT INTO users (user_id, resume_text, resume_filename, summary, skills_json, seniority, desired_role, target_stack, auth_token, is_authorized, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			resume_text = excluded.resume_text,
			resume_filename = excluded.resume_filename,
			summary = excluded.summary,
			skills_json = excluded.skills_json,
			seniority = excluded.seniority,
			desired_role = excluded.desired_role,
			target_stack = CASE WHEN excluded.target_stack != '' THEN excluded.target_stack ELSE users.target_stack END,
			auth_token = CASE WHEN excluded.auth_token != '' THEN excluded.auth_token ELSE users.auth_token END,
			is_authorized = excluded.is_authorized,
			updated_at = excluded.updated_at;`

	_, err := s.db.Exec(query,
		resume.UserID,
		resume.RawText,
		resume.FileName,
		resume.Summary,
		string(skillsJSON),
		resume.Seniority,
		resume.DesiredRole,
		resume.TargetStack,
		resume.AuthToken,
		authInt,
		time.Now(),
	)
	return err
}

// GetResume retrieves the user's resume
func (s *Storage) GetResume(userID int64) (*models.Resume, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT user_id, COALESCE(resume_text, ''), COALESCE(resume_filename, ''), COALESCE(summary, ''), COALESCE(skills_json, '[]'), COALESCE(seniority, 'Junior'), COALESCE(desired_role, ''), COALESCE(target_stack, 'Golang'), COALESCE(auth_token, ''), COALESCE(is_authorized, 1), COALESCE(is_polling_enabled, 0), updated_at
		FROM users WHERE user_id = ?;`

	var r models.Resume
	var skillsJSON string
	var authInt int
	var pollInt int
	var updatedAt sql.NullTime

	err := s.db.QueryRow(query, userID).Scan(
		&r.UserID,
		&r.RawText,
		&r.FileName,
		&r.Summary,
		&skillsJSON,
		&r.Seniority,
		&r.DesiredRole,
		&r.TargetStack,
		&r.AuthToken,
		&authInt,
		&pollInt,
		&updatedAt,
	)
	if err != nil {
		return nil, err
	}

	r.IsAuthorized = (authInt == 1)
	r.IsPollingEnabled = (pollInt == 1)
	if updatedAt.Valid {
		r.UpdatedAt = updatedAt.Time
	}
	_ = json.Unmarshal([]byte(skillsJSON), &r.Skills)
	return &r, nil
}

// GenerateUserToken creates and saves a unique session token for web auth
func (s *Storage) GenerateUserToken(userID int64) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)

	query := `INSERT INTO users (user_id, auth_token, is_authorized, updated_at)
		VALUES (?, ?, 1, ?)
		ON CONFLICT(user_id) DO UPDATE SET auth_token = excluded.auth_token, updated_at = excluded.updated_at;`

	_, err := s.db.Exec(query, userID, token, time.Now())
	if err != nil {
		return "", err
	}
	return token, nil
}

// GetUserByToken validates session token and returns user resume profile
func (s *Storage) GetUserByToken(token string) (*models.Resume, error) {
	if token == "" {
		return nil, fmt.Errorf("empty token")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT user_id, COALESCE(resume_text, ''), COALESCE(resume_filename, ''), COALESCE(summary, ''), COALESCE(skills_json, '[]'), COALESCE(seniority, 'Junior'), COALESCE(desired_role, ''), COALESCE(target_stack, 'Golang'), COALESCE(auth_token, ''), COALESCE(is_authorized, 1), COALESCE(is_polling_enabled, 0), updated_at
		FROM users WHERE auth_token = ?;`

	var r models.Resume
	var skillsJSON string
	var authInt int
	var pollInt int
	var updatedAt sql.NullTime

	err := s.db.QueryRow(query, token).Scan(
		&r.UserID,
		&r.RawText,
		&r.FileName,
		&r.Summary,
		&skillsJSON,
		&r.Seniority,
		&r.DesiredRole,
		&r.TargetStack,
		&r.AuthToken,
		&authInt,
		&pollInt,
		&updatedAt,
	)
	if err != nil {
		return nil, err
	}
	r.IsAuthorized = (authInt == 1)
	r.IsPollingEnabled = (pollInt == 1)
	if updatedAt.Valid {
		r.UpdatedAt = updatedAt.Time
	}
	_ = json.Unmarshal([]byte(skillsJSON), &r.Skills)
	return &r, nil
}

// SetUserStack sets the preferred tech stack / search query
func (s *Storage) SetUserStack(userID int64, stack string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `INSERT INTO users (user_id, target_stack, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET target_stack = excluded.target_stack, updated_at = excluded.updated_at;`

	_, err := s.db.Exec(query, userID, stack, time.Now())
	return err
}

// GetUserStack gets the user's preferred search stack
func (s *Storage) GetUserStack(userID int64) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var stack string
	err := s.db.QueryRow("SELECT COALESCE(target_stack, 'Golang') FROM users WHERE user_id = ?", userID).Scan(&stack)
	if err != nil || stack == "" {
		return "Golang"
	}
	return stack
}

// SetUserAuthorized marks user as authenticated or revoked
func (s *Storage) SetUserAuthorized(userID int64, auth bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	val := 0
	if auth {
		val = 1
	}
	query := `INSERT INTO users (user_id, is_authorized, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET is_authorized = excluded.is_authorized, updated_at = excluded.updated_at;`
	_, err := s.db.Exec(query, userID, val, time.Now())
	return err
}

// IsUserAuthorized checks if user has permission to use bot/API
func (s *Storage) IsUserAuthorized(userID int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var authInt int
	err := s.db.QueryRow("SELECT COALESCE(is_authorized, 0) FROM users WHERE user_id = ?", userID).Scan(&authInt)
	if err != nil {
		return false
	}
	return authInt == 1
}

// SetUserPolling enables or disables continuous background vacancy polling
func (s *Storage) SetUserPolling(userID int64, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	val := 0
	if enabled {
		val = 1
	}
	query := `INSERT INTO users (user_id, is_polling_enabled, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET is_polling_enabled = excluded.is_polling_enabled, updated_at = excluded.updated_at;`

	_, err := s.db.Exec(query, userID, val, time.Now())
	return err
}

// IsUserPolling returns true if the user has background polling enabled
func (s *Storage) IsUserPolling(userID int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var val int
	err := s.db.QueryRow("SELECT COALESCE(is_polling_enabled, 0) FROM users WHERE user_id = ?", userID).Scan(&val)
	if err != nil {
		return false
	}
	return val == 1
}

// GetAllPollingUsers returns all user IDs who have polling enabled and a non-empty resume
func (s *Storage) GetAllPollingUsers() ([]int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query("SELECT user_id FROM users WHERE LENGTH(resume_text) > 0 AND is_polling_enabled = 1")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// VacancyExists checks if vacancy already exists
func (s *Storage) VacancyExists(id string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM vacancies WHERE id = ?", id).Scan(&count)
	return count > 0, err
}

// SaveVacancy stores a discovered vacancy
func (s *Storage) SaveVacancy(v *models.Vacancy) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `INSERT OR IGNORE INTO vacancies (id, source, external_id, title, company, salary, location, url, description, requirements, published_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`

	_, err := s.db.Exec(query,
		v.ID,
		v.Source,
		v.ExternalID,
		v.Title,
		v.Company,
		v.Salary,
		v.Location,
		v.URL,
		v.Description,
		v.Requirements,
		v.PublishedAt,
		time.Now(),
	)
	return err
}

// SaveUserVacancyState saves user interaction with vacancy
func (s *Storage) SaveUserVacancyState(userID int64, vacID string, matchPct int, analysis string, status models.VacancyStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `INSERT INTO user_vacancies (user_id, vacancy_id, match_pct, match_analysis, status, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, vacancy_id) DO UPDATE SET
			match_pct = excluded.match_pct,
			match_analysis = excluded.match_analysis,
			status = excluded.status,
			updated_at = excluded.updated_at;`

	_, err := s.db.Exec(query, userID, vacID, matchPct, analysis, string(status), time.Now())
	return err
}

// UpdateUserVacancyStatus updates only the status (favorite, rejected, applied, etc.)
func (s *Storage) UpdateUserVacancyStatus(userID int64, vacID string, status models.VacancyStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var appliedAt *time.Time
	if status == models.StatusApplied {
		now := time.Now()
		appliedAt = &now
	}

	query := `UPDATE user_vacancies SET status = ?, updated_at = ?, applied_at = COALESCE(?, applied_at)
		WHERE user_id = ? AND vacancy_id = ?;`

	_, err := s.db.Exec(query, string(status), time.Now(), appliedAt, userID, vacID)
	return err
}

// ToggleFavorite toggles favorite state
func (s *Storage) ToggleFavorite(userID int64, vacID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var currentStatus string
	err := s.db.QueryRow("SELECT status FROM user_vacancies WHERE user_id = ? AND vacancy_id = ?", userID, vacID).Scan(&currentStatus)
	isFav := false
	if err == sql.ErrNoRows || currentStatus != string(models.StatusFavorite) {
		query := `INSERT INTO user_vacancies (user_id, vacancy_id, status, updated_at)
			VALUES (?, ?, 'favorite', ?)
			ON CONFLICT(user_id, vacancy_id) DO UPDATE SET status = 'favorite', updated_at = excluded.updated_at;`
		_, err = s.db.Exec(query, userID, vacID, time.Now())
		isFav = true
	} else {
		query := `UPDATE user_vacancies SET status = 'new', updated_at = ? WHERE user_id = ? AND vacancy_id = ?;`
		_, err = s.db.Exec(query, time.Now(), userID, vacID)
		isFav = false
	}
	return isFav, err
}

// UpdateCoverLetter stores cover letter text for vacancy
func (s *Storage) UpdateCoverLetter(userID int64, vacID string, letter string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `UPDATE user_vacancies SET cover_letter = ?, updated_at = ?
		WHERE user_id = ? AND vacancy_id = ?;`

	_, err := s.db.Exec(query, letter, time.Now(), userID, vacID)
	return err
}

// GetUserVacancy gets full vacancy with user match details
func (s *Storage) GetUserVacancy(userID int64, vacID string) (*models.Vacancy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT v.id, v.source, v.external_id, v.title, v.company, v.salary, v.location, v.url, v.description, v.requirements, v.published_at, v.created_at,
		COALESCE(uv.match_pct, 0), COALESCE(uv.match_analysis, ''), COALESCE(uv.status, 'new'), COALESCE(uv.cover_letter, '')
		FROM vacancies v
		LEFT JOIN user_vacancies uv ON v.id = uv.vacancy_id AND uv.user_id = ?
		WHERE v.id = ?;`

	var v models.Vacancy
	var statusStr string
	err := s.db.QueryRow(query, userID, vacID).Scan(
		&v.ID, &v.Source, &v.ExternalID, &v.Title, &v.Company, &v.Salary, &v.Location, &v.URL,
		&v.Description, &v.Requirements, &v.PublishedAt, &v.CreatedAt,
		&v.MatchPct, &v.MatchAnalysis, &statusStr, &v.CoverLetter,
	)
	if err != nil {
		return nil, err
	}
	v.Status = models.VacancyStatus(statusStr)
	return &v, nil
}

// GetFavorites returns list of favorite vacancies for user
func (s *Storage) GetFavorites(userID int64) ([]*models.Vacancy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT v.id, v.source, v.external_id, v.title, v.company, v.salary, v.location, v.url, v.description, v.requirements, v.published_at, v.created_at,
		uv.match_pct, uv.match_analysis, uv.status, uv.cover_letter
		FROM vacancies v
		JOIN user_vacancies uv ON v.id = uv.vacancy_id
		WHERE uv.user_id = ? AND uv.status = 'favorite'
		ORDER BY uv.updated_at DESC;`

	rows, err := s.db.Query(query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*models.Vacancy
	for rows.Next() {
		var v models.Vacancy
		var statusStr string
		err := rows.Scan(
			&v.ID, &v.Source, &v.ExternalID, &v.Title, &v.Company, &v.Salary, &v.Location, &v.URL,
			&v.Description, &v.Requirements, &v.PublishedAt, &v.CreatedAt,
			&v.MatchPct, &v.MatchAnalysis, &statusStr, &v.CoverLetter,
		)
		if err != nil {
			return nil, err
		}
		v.Status = models.VacancyStatus(statusStr)
		list = append(list, &v)
	}
	return list, nil
}

// GetVacanciesList returns paginated and filtered vacancies for the web API
func (s *Storage) GetVacanciesList(userID int64, search, grade, source, status string, minMatch, limit, offset int) ([]*models.Vacancy, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 20
	}

	whereClauses := []string{"1=1"}
	args := []interface{}{userID}

	if search != "" {
		whereClauses = append(whereClauses, "(v.title LIKE ? OR v.company LIKE ? OR v.requirements LIKE ?)")
		pattern := "%" + search + "%"
		args = append(args, pattern, pattern, pattern)
	}

	if source != "" {
		whereClauses = append(whereClauses, "v.source = ?")
		args = append(args, source)
	}

	if status != "" {
		whereClauses = append(whereClauses, "uv.status = ?")
		args = append(args, status)
	}

	if minMatch > 0 {
		whereClauses = append(whereClauses, "COALESCE(uv.match_pct, 0) >= ?")
		args = append(args, minMatch)
	}

	whereSQL := strings.Join(whereClauses, " AND ")

	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM vacancies v
		LEFT JOIN user_vacancies uv ON v.id = uv.vacancy_id AND uv.user_id = ?
		WHERE %s;`, whereSQL)

	var total int
	_ = s.db.QueryRow(countQuery, args...).Scan(&total)

	query := fmt.Sprintf(`SELECT v.id, v.source, v.external_id, v.title, v.company, v.salary, v.location, v.url, v.description, v.requirements, v.published_at, v.created_at,
		COALESCE(uv.match_pct, 0), COALESCE(uv.match_analysis, ''), COALESCE(uv.status, 'new'), COALESCE(uv.cover_letter, '')
		FROM vacancies v
		LEFT JOIN user_vacancies uv ON v.id = uv.vacancy_id AND uv.user_id = ?
		WHERE %s
		ORDER BY COALESCE(uv.match_pct, 0) DESC, v.created_at DESC
		LIMIT ? OFFSET ?;`, whereSQL)

	args = append(args, limit, offset)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var list []*models.Vacancy
	for rows.Next() {
		var v models.Vacancy
		var statusStr string
		err := rows.Scan(
			&v.ID, &v.Source, &v.ExternalID, &v.Title, &v.Company, &v.Salary, &v.Location, &v.URL,
			&v.Description, &v.Requirements, &v.PublishedAt, &v.CreatedAt,
			&v.MatchPct, &v.MatchAnalysis, &statusStr, &v.CoverLetter,
		)
		if err != nil {
			return nil, 0, err
		}
		v.Status = models.VacancyStatus(statusStr)
		list = append(list, &v)
	}

	return list, total, nil
}

// GetUnviewedVacancies returns vacancies that the user has not rejected or applied to yet
func (s *Storage) GetUnviewedVacancies(userID int64, limit int) ([]*models.Vacancy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 15
	}

	query := `SELECT v.id, v.source, v.external_id, v.title, v.company, v.salary, v.location, v.url, v.description, v.requirements, v.published_at, v.created_at,
		COALESCE(uv.match_pct, 0), COALESCE(uv.match_analysis, ''), COALESCE(uv.status, 'new'), COALESCE(uv.cover_letter, '')
		FROM vacancies v
		LEFT JOIN user_vacancies uv ON v.id = uv.vacancy_id AND uv.user_id = ?
		WHERE uv.status IS NULL OR uv.status = 'new'
		ORDER BY v.created_at DESC
		LIMIT ?;`

	rows, err := s.db.Query(query, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*models.Vacancy
	for rows.Next() {
		var v models.Vacancy
		var statusStr string
		err := rows.Scan(
			&v.ID, &v.Source, &v.ExternalID, &v.Title, &v.Company, &v.Salary, &v.Location, &v.URL,
			&v.Description, &v.Requirements, &v.PublishedAt, &v.CreatedAt,
			&v.MatchPct, &v.MatchAnalysis, &statusStr, &v.CoverLetter,
		)
		if err != nil {
			return nil, err
		}
		v.Status = models.VacancyStatus(statusStr)
		list = append(list, &v)
	}
	return list, nil
}

// GetStats returns counts per status for user
func (s *Storage) GetStats(userID int64) (map[string]int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT status, COUNT(*) FROM user_vacancies WHERE user_id = ? GROUP BY status;`
	rows, err := s.db.Query(query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stats := make(map[string]int)
	for rows.Next() {
		var st string
		var cnt int
		if err := rows.Scan(&st, &cnt); err == nil {
			stats[st] = cnt
		}
	}
	return stats, nil
}

// SaveAutoApplyFilter stores auto apply rules for user
func (s *Storage) SaveAutoApplyFilter(userID int64, f models.AutoApplyFilter) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(f)
	if err != nil {
		return err
	}

	query := `INSERT INTO users (user_id, auto_apply_json, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			auto_apply_json = excluded.auto_apply_json,
			updated_at = excluded.updated_at;`

	_, err = s.db.Exec(query, userID, string(data), time.Now())
	return err
}

// GetAutoApplyFilter retrieves auto-apply rules
func (s *Storage) GetAutoApplyFilter(userID int64) (*models.AutoApplyFilter, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var jsonStr string
	err := s.db.QueryRow("SELECT auto_apply_json FROM users WHERE user_id = ?", userID).Scan(&jsonStr)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(jsonStr) == "" {
		return nil, nil
	}

	var f models.AutoApplyFilter
	if err := json.Unmarshal([]byte(jsonStr), &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// GetAllActiveUsers returns all user IDs who have saved a resume
func (s *Storage) GetAllActiveUsers() ([]int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query("SELECT user_id FROM users WHERE LENGTH(resume_text) > 0")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// Channels management
func (s *Storage) AddChannel(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	name = strings.TrimPrefix(name, "@")
	name = strings.TrimSpace(name)
	_, err := s.db.Exec("INSERT OR IGNORE INTO channels (name, added_at) VALUES (?, ?)", name, time.Now())
	return err
}

func (s *Storage) RemoveChannel(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	name = strings.TrimPrefix(name, "@")
	name = strings.TrimSpace(name)
	_, err := s.db.Exec("DELETE FROM channels WHERE name = ?", name)
	return err
}

func (s *Storage) GetChannels() ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query("SELECT name FROM channels")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chs []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			chs = append(chs, name)
		}
	}
	return chs, nil
}
