package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"jobparser/internal/ai"
	"jobparser/internal/browser"
	"jobparser/internal/config"
	"jobparser/internal/matcher"
	"jobparser/internal/models"
	"jobparser/internal/scrapers"
	"jobparser/internal/storage"
)

type Server struct {
	cfg          *config.Config
	storage      *storage.Storage
	ai           *ai.GeminiClient
	orchestrator *scrapers.Orchestrator
	matcher      *matcher.Matcher
	chrome       *browser.ChromeManager
	httpServer   *http.Server
}

func NewServer(
	cfg *config.Config,
	st *storage.Storage,
	aiClient *ai.GeminiClient,
	orch *scrapers.Orchestrator,
	m *matcher.Matcher,
	chrome *browser.ChromeManager,
) *Server {
	s := &Server{
		cfg:          cfg,
		storage:      st,
		ai:           aiClient,
		orchestrator: orch,
		matcher:      m,
		chrome:       chrome,
	}

	mux := http.NewServeMux()

	// Public endpoints
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/api/test-scrapers", s.handleTestScrapers)
	mux.HandleFunc("/api/auth/verify", s.handleAuthVerify)

	// Protected endpoints (Token Auth)
	mux.HandleFunc("/api/me", s.authMiddleware(s.handleMe))
	mux.HandleFunc("/api/me/resume/pdf", s.authMiddleware(s.handleDownloadPDF))
	mux.HandleFunc("/api/me/stack", s.authMiddleware(s.handleUpdateStack))
	mux.HandleFunc("/api/me/grade", s.authMiddleware(s.handleUpdateGrade))
	mux.HandleFunc("/api/me/polling", s.authMiddleware(s.handleUpdatePolling))
	mux.HandleFunc("/api/vacancies", s.authMiddleware(s.handleVacancies))
	mux.HandleFunc("/api/vacancies/refresh", s.authMiddleware(s.handleRefreshVacancies))
	mux.HandleFunc("/api/favorites", s.authMiddleware(s.handleFavorites))
	mux.HandleFunc("/api/favorites/toggle", s.authMiddleware(s.handleToggleFavorite))
	mux.HandleFunc("/api/cover-letter", s.authMiddleware(s.handleCoverLetter))
	mux.HandleFunc("/api/apply", s.authMiddleware(s.handleApply))
	mux.HandleFunc("/api/stats", s.authMiddleware(s.handleStats))

	// Static web frontend files
	mux.Handle("/", http.FileServer(http.Dir("./web")))

	// CORS wrapper
	handler := s.corsMiddleware(mux)

	port := cfg.APIPort
	if port <= 0 {
		port = 8080
	}

	s.httpServer = &http.Server{
		Addr:         fmt.Sprintf("0.0.0.0:%d", port),
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	return s
}

func (s *Server) Start() error {
	log.Printf("[API] REST API server listening on %s (CORS enabled)", s.httpServer.Addr)
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) Stop(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With, X-Session-Token")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

type contextKey string

const userContextKey contextKey = "user_resume"

func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := ""
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			token = strings.TrimPrefix(authHeader, "Bearer ")
		} else if t := r.URL.Query().Get("token"); t != "" {
			token = t
		} else if t := r.Header.Get("X-Session-Token"); t != "" {
			token = t
		}

		token = strings.TrimSpace(token)
		if token == "" {
			s.jsonError(w, http.StatusUnauthorized, "Authorization token is missing. Please login via Telegram bot.")
			return
		}

		user, err := s.storage.GetUserByToken(token)
		if err != nil || user == nil {
			s.jsonError(w, http.StatusUnauthorized, "Invalid or expired authorization token.")
			return
		}

		ctx := context.WithValue(r.Context(), userContextKey, user)
		next(w, r.WithContext(ctx))
	}
}

func getUserFromContext(ctx context.Context) *models.Resume {
	val := ctx.Value(userContextKey)
	if user, ok := val.(*models.Resume); ok {
		return user
	}
	return nil
}

func (s *Server) jsonResponse(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (s *Server) jsonError(w http.ResponseWriter, status int, message string) {
	s.jsonResponse(w, status, map[string]interface{}{
		"error":   true,
		"message": message,
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.jsonResponse(w, http.StatusOK, map[string]interface{}{
		"status":  "ok",
		"service": "JobParser API",
		"time":    time.Now().Format(time.RFC3339),
	})
}

func (s *Server) handleAuthVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.jsonError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req struct {
		Token      string `json:"token"`
		Password   string `json:"password"`
		TelegramID int64  `json:"telegram_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	if req.Token != "" {
		user, err := s.storage.GetUserByToken(strings.TrimSpace(req.Token))
		if err != nil || user == nil {
			s.jsonError(w, http.StatusUnauthorized, "Неверный или устаревший токен авторизации")
			return
		}
		s.jsonResponse(w, http.StatusOK, map[string]interface{}{
			"valid":        true,
			"user_id":      user.UserID,
			"seniority":    user.Seniority,
			"target_stack": user.TargetStack,
			"token":        req.Token,
		})
		return
	}

	// Login with Telegram ID and Password
	if req.TelegramID != 0 {
		if s.cfg.BotAuthPassword != "" && req.Password != s.cfg.BotAuthPassword {
			s.jsonError(w, http.StatusUnauthorized, "Неверный пароль")
			return
		}
		token, err := s.storage.GenerateUserToken(req.TelegramID)
		if err != nil {
			s.jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		user, _ := s.storage.GetResume(req.TelegramID)
		seniority := "Junior"
		stack := "Golang"
		if user != nil {
			if user.Seniority != "" {
				seniority = user.Seniority
			}
			if user.TargetStack != "" {
				stack = user.TargetStack
			}
		}

		s.jsonResponse(w, http.StatusOK, map[string]interface{}{
			"valid":        true,
			"user_id":      req.TelegramID,
			"seniority":    seniority,
			"target_stack": stack,
			"token":        token,
		})
		return
	}

	s.jsonError(w, http.StatusBadRequest, "Необходимо указать token или telegram_id")
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user := getUserFromContext(r.Context())
	if user == nil {
		s.jsonError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	freshUser, err := s.storage.GetResume(user.UserID)
	if err == nil && freshUser != nil {
		user = freshUser
	}

	hasPDF := user.PDFFilePath != "" || user.PDFFileID != ""
	if !hasPDF && user.FileName != "" && strings.HasSuffix(strings.ToLower(user.FileName), ".pdf") {
		hasPDF = true
	}

	s.jsonResponse(w, http.StatusOK, map[string]interface{}{
		"user_id":            user.UserID,
		"seniority":          user.Seniority,
		"target_stack":       user.TargetStack,
		"desired_role":       user.DesiredRole,
		"skills":             user.Skills,
		"summary":            user.Summary,
		"has_resume":         len(user.RawText) > 0,
		"has_pdf":            hasPDF,
		"file_name":          user.FileName,
		"pdf_file_path":      user.PDFFilePath,
		"is_polling_enabled": s.storage.IsUserPolling(user.UserID),
		"updated_at":         user.UpdatedAt,
	})
}

func (s *Server) handleDownloadPDF(w http.ResponseWriter, r *http.Request) {
	user := getUserFromContext(r.Context())
	if user == nil {
		s.jsonError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	freshUser, err := s.storage.GetResume(user.UserID)
	if err == nil && freshUser != nil {
		user = freshUser
	}

	pdfPath := user.PDFFilePath
	if pdfPath == "" {
		pdfPath = fmt.Sprintf("./data/resumes/user_%d.pdf", user.UserID)
	}

	if _, err := os.Stat(pdfPath); os.IsNotExist(err) {
		s.jsonError(w, http.StatusNotFound, "Файл резюме в формате PDF еще не загружен на сервер.")
		return
	}

	fileName := user.FileName
	if fileName == "" || !strings.HasSuffix(strings.ToLower(fileName), ".pdf") {
		fileName = "Resume.pdf"
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, fileName))
	http.ServeFile(w, r, pdfPath)
}

func (s *Server) handleUpdatePolling(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.jsonError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	user := getUserFromContext(r.Context())
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.jsonError(w, http.StatusBadRequest, "Неверный формат запроса")
		return
	}

	if err := s.storage.SetUserPolling(user.UserID, body.Enabled); err != nil {
		s.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success":            true,
		"is_polling_enabled": body.Enabled,
	})
}

func (s *Server) handleUpdateStack(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.jsonError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	user := getUserFromContext(r.Context())
	var body struct {
		Stack string `json:"stack"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Stack) == "" {
		s.jsonError(w, http.StatusBadRequest, "Поле 'stack' обязательно для заполнения")
		return
	}

	stack := strings.TrimSpace(body.Stack)
	if err := s.storage.SetUserStack(user.UserID, stack); err != nil {
		s.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success":      true,
		"target_stack": stack,
	})
}

func (s *Server) handleUpdateGrade(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.jsonError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	user := getUserFromContext(r.Context())
	var body struct {
		Grade string `json:"grade"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Grade) == "" {
		s.jsonError(w, http.StatusBadRequest, "Поле 'grade' обязательно для заполнения")
		return
	}

	grade := strings.TrimSpace(body.Grade)
	user.Seniority = grade
	if err := s.storage.SaveResume(user); err != nil {
		s.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success":   true,
		"seniority": grade,
	})
}

func (s *Server) handleVacancies(w http.ResponseWriter, r *http.Request) {
	user := getUserFromContext(r.Context())
	q := r.URL.Query()

	search := strings.TrimSpace(q.Get("search"))
	grade := strings.TrimSpace(q.Get("grade"))
	source := strings.TrimSpace(q.Get("source"))
	status := strings.TrimSpace(q.Get("status"))
	sortOrder := strings.TrimSpace(q.Get("sort"))

	minMatch := 0
	if m, err := strconv.Atoi(q.Get("min_match")); err == nil {
		minMatch = m
	}

	page := 1
	if p, err := strconv.Atoi(q.Get("page")); err == nil && p > 0 {
		page = p
	}

	limit := 20
	if l, err := strconv.Atoi(q.Get("limit")); err == nil && l > 0 {
		limit = l
		if limit > 100 {
			limit = 100
		}
	}

	offset := (page - 1) * limit

	vacancies, total, err := s.storage.GetVacanciesList(user.UserID, search, grade, source, status, sortOrder, minMatch, limit, offset)
	if err != nil {
		s.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	for _, v := range vacancies {
		if v.ExpectedGrade == "" {
			v.ExpectedGrade = matcher.DetectVacancyGrade(v.Title, v.Requirements, v.Description)
		}
	}

	s.jsonResponse(w, http.StatusOK, map[string]interface{}{
		"vacancies": vacancies,
		"total":     total,
		"page":      page,
		"limit":     limit,
	})
}

func (s *Server) handleRefreshVacancies(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.jsonError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	user := getUserFromContext(r.Context())
	stack := user.TargetStack
	if stack == "" {
		stack = "Golang"
	}

	ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
	defer cancel()

	vacancies, err := s.orchestrator.FetchAllForQuery(ctx, stack)
	if err != nil {
		s.jsonError(w, http.StatusInternalServerError, fmt.Sprintf("Ошибка парсинга: %v", err))
		return
	}

	go func(uid int64, vacs []*models.Vacancy) {
		for _, v := range vacs {
			evalCtx, evalCancel := context.WithTimeout(context.Background(), 15*time.Second)
			_, _ = s.matcher.EvaluateVacancy(evalCtx, uid, v)
			evalCancel()
		}
	}(user.UserID, vacancies)

	s.jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success":     true,
		"newly_found": len(vacancies),
		"stack":       stack,
	})
}

func (s *Server) handleFavorites(w http.ResponseWriter, r *http.Request) {
	user := getUserFromContext(r.Context())
	favs, err := s.storage.GetFavorites(user.UserID)
	if err != nil {
		s.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, v := range favs {
		if v.ExpectedGrade == "" {
			v.ExpectedGrade = matcher.DetectVacancyGrade(v.Title, v.Requirements, v.Description)
		}
	}
	s.jsonResponse(w, http.StatusOK, map[string]interface{}{
		"favorites": favs,
		"count":     len(favs),
	})
}

func (s *Server) handleToggleFavorite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.jsonError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	user := getUserFromContext(r.Context())
	var body struct {
		VacancyID string `json:"vacancy_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.VacancyID == "" {
		s.jsonError(w, http.StatusBadRequest, "Поле 'vacancy_id' обязательно")
		return
	}

	isFav, err := s.storage.ToggleFavorite(user.UserID, body.VacancyID)
	if err != nil {
		s.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success":     true,
		"vacancy_id":  body.VacancyID,
		"is_favorite": isFav,
	})
}

func (s *Server) handleCoverLetter(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.jsonError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	user := getUserFromContext(r.Context())
	var body struct {
		VacancyID  string `json:"vacancy_id"`
		Regenerate bool   `json:"regenerate"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.VacancyID == "" {
		s.jsonError(w, http.StatusBadRequest, "Поле 'vacancy_id' обязательно")
		return
	}

	vac, err := s.storage.GetUserVacancy(user.UserID, body.VacancyID)
	if err != nil || vac == nil {
		s.jsonError(w, http.StatusNotFound, "Вакансия не найдена")
		return
	}

	if vac.CoverLetter != "" && !body.Regenerate {
		s.jsonResponse(w, http.StatusOK, map[string]interface{}{
			"cover_letter": vac.CoverLetter,
			"vacancy_id":   vac.ID,
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
	defer cancel()

	letter, err := s.ai.GenerateCoverLetter(ctx, user.RawText, vac)
	if err != nil || strings.TrimSpace(letter) == "" {
		letter = ai.FallbackCoverLetter(user.RawText, vac)
	}

	_ = s.storage.UpdateCoverLetter(user.UserID, vac.ID, letter)

	s.jsonResponse(w, http.StatusOK, map[string]interface{}{
		"cover_letter": letter,
		"vacancy_id":   vac.ID,
	})
}

func (s *Server) handleApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.jsonError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	user := getUserFromContext(r.Context())
	var body struct {
		VacancyID string `json:"vacancy_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.VacancyID == "" {
		s.jsonError(w, http.StatusBadRequest, "Поле 'vacancy_id' обязательно")
		return
	}

	vac, err := s.storage.GetUserVacancy(user.UserID, body.VacancyID)
	if err != nil || vac == nil {
		s.jsonError(w, http.StatusNotFound, "Вакансия не найдена")
		return
	}

	letter := vac.CoverLetter
	if letter == "" {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		letter, _ = s.ai.GenerateCoverLetter(ctx, user.RawText, vac)
		cancel()
		if letter != "" {
			_ = s.storage.UpdateCoverLetter(user.UserID, vac.ID, letter)
		}
	}

	if vac.Source == "hh.ru" || vac.Source == "rabota.by" {
		applyCtx, applyCancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer applyCancel()
		applyRes, err := s.chrome.ApplyToVacancy(applyCtx, vac, letter, user.PDFFilePath)
		if err != nil {
			log.Printf("[API Apply] Chrome apply warning: %v", err)
			_ = s.storage.UpdateUserVacancyStatus(user.UserID, vac.ID, models.StatusApplied)
			s.jsonResponse(w, http.StatusOK, map[string]interface{}{
				"success": true,
				"message": fmt.Sprintf("Отклик зафиксирован (предупреждение браузера: %v)", err),
			})
			return
		}
		if applyRes != nil && !applyRes.Success {
			_ = s.storage.UpdateUserVacancyStatus(user.UserID, vac.ID, models.StatusApplied)
			s.jsonResponse(w, http.StatusOK, map[string]interface{}{
				"success": true,
				"message": applyRes.Message,
			})
			return
		}
	}

	_ = s.storage.UpdateUserVacancyStatus(user.UserID, vac.ID, models.StatusApplied)
	s.jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Отклик успешно зарегистрирован!",
	})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	user := getUserFromContext(r.Context())
	stats, err := s.storage.GetStats(user.UserID)
	if err != nil {
		s.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.jsonResponse(w, http.StatusOK, map[string]interface{}{
		"total":    stats[string(models.StatusNew)] + stats[string(models.StatusFavorite)] + stats[string(models.StatusApplied)] + stats[string(models.StatusRejected)],
		"new":      stats[string(models.StatusNew)],
		"favorite": stats[string(models.StatusFavorite)],
		"applied":  stats[string(models.StatusApplied)],
		"rejected": stats[string(models.StatusRejected)],
	})
}

func (s *Server) handleTestScrapers(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		query = "golang"
	}

	result := make(map[string]interface{})
	c := &http.Client{Timeout: 5 * time.Second}

	// 1. Test HH RSS
	rssURL := "https://hh.ru/search/vacancy/rss?text=" + url.QueryEscape(query) + "&schedule=remote&enable_snippets=true"
	req, _ := http.NewRequest("GET", rssURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	if resp, err := c.Do(req); err == nil {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		snippet := string(body)
		if len(snippet) > 250 {
			snippet = snippet[:250]
		}
		result["hh_rss_status"] = resp.StatusCode
		result["hh_rss_snippet"] = snippet
	} else {
		result["hh_rss_error"] = err.Error()
	}

	// 2. Test Rabota.by RSS
	byURL := "https://rabota.by/search/vacancy/rss?text=" + url.QueryEscape(query) + "&enable_snippets=true"
	byReq, _ := http.NewRequest("GET", byURL, nil)
	byReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	if resp, err := c.Do(byReq); err == nil {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		snippet := string(body)
		if len(snippet) > 250 {
			snippet = snippet[:250]
		}
		result["rabota_by_rss_status"] = resp.StatusCode
		result["rabota_by_rss_snippet"] = snippet
	} else {
		result["rabota_by_rss_error"] = err.Error()
	}

	// 3. Test HH API
	apiURL := "https://api.hh.ru/vacancies?text=" + url.QueryEscape(query) + "&per_page=5"
	apiReq, _ := http.NewRequest("GET", apiURL, nil)
	apiReq.Header.Set("User-Agent", "JobParserBot/2.0 (sobolnikita@github.com)")
	if resp, err := c.Do(apiReq); err == nil {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		snippet := string(body)
		if len(snippet) > 250 {
			snippet = snippet[:250]
		}
		result["hh_api_status"] = resp.StatusCode
		result["hh_api_snippet"] = snippet
	} else {
		result["hh_api_error"] = err.Error()
	}

	s.jsonResponse(w, http.StatusOK, result)
}
