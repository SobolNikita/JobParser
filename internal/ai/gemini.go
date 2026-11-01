package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"jobparser/internal/models"
	"jobparser/internal/resume"
)

type GeminiClient struct {
	apiKey     string
	model      string
	httpClient *http.Client
}

// NormalizeModel converts unofficial or alias model names to supported Gemini API models
func NormalizeModel(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	switch {
	case strings.Contains(name, "3.8") || strings.Contains(name, "3.5") || strings.Contains(name, "3-flash"):
		// Chat UI internal name mapped to latest high-speed Gemini Flash model
		return "gemini-2.0-flash"
	case strings.Contains(name, "2.0"):
		return "gemini-2.0-flash"
	case strings.Contains(name, "1.5-pro") || strings.Contains(name, "pro"):
		return "gemini-1.5-pro"
	case strings.Contains(name, "1.5") || strings.Contains(name, "flash"):
		return "gemini-1.5-flash"
	case name == "":
		return "gemini-2.0-flash"
	default:
		return name
	}
}

func NewGeminiClient(apiKey, model string) *GeminiClient {
	normalized := NormalizeModel(model)
	return &GeminiClient{
		apiKey: strings.TrimSpace(apiKey),
		model:  normalized,
		httpClient: &http.Client{
			Timeout: 45 * time.Second,
		},
	}
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiGenerationConfig struct {
	Temperature      float64 `json:"temperature,omitempty"`
	MaxOutputTokens  int     `json:"maxOutputTokens,omitempty"`
	ResponseMimeType string  `json:"responseMimeType,omitempty"`
}

type geminiRequest struct {
	Contents         []geminiContent         `json:"contents"`
	GenerationConfig *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiCandidate struct {
	Content struct {
		Parts []geminiPart `json:"parts"`
	} `json:"content"`
}

type geminiResponse struct {
	Candidates []geminiCandidate `json:"candidates"`
	Error      *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error,omitempty"`
}

// callWithModel performs a single request to the specified Gemini model
func (c *GeminiClient) callWithModel(ctx context.Context, modelName, prompt string, jsonOutput bool, temp float64) (string, error) {
	if c.apiKey == "" {
		return "", fmt.Errorf("GEMINI_API_KEY is not configured")
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", modelName, c.apiKey)

	reqBody := geminiRequest{
		Contents: []geminiContent{
			{
				Parts: []geminiPart{
					{Text: prompt},
				},
			},
		},
		GenerationConfig: &geminiGenerationConfig{
			Temperature:     temp,
			MaxOutputTokens: 2048,
		},
	}

	if jsonOutput {
		reqBody.GenerationConfig.ResponseMimeType = "application/json"
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("network request failed for %s: %w", modelName, err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	var res geminiResponse
	if err := json.Unmarshal(bodyBytes, &res); err != nil {
		return "", fmt.Errorf("failed to parse json (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	if res.Error != nil {
		return "", fmt.Errorf("gemini api error [%d %s]: %s", res.Error.Code, res.Error.Status, res.Error.Message)
	}

	if len(res.Candidates) == 0 || len(res.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("empty candidates returned from %s", modelName)
	}

	return strings.TrimSpace(res.Candidates[0].Content.Parts[0].Text), nil
}

// callAPI executes API call with automatic retries and fallback across model names
func (c *GeminiClient) callAPI(ctx context.Context, prompt string, jsonOutput bool, temp float64) (string, error) {
	// Candidate models to try in sequence
	modelsToTry := []string{c.model}
	fallbacks := []string{"gemini-2.0-flash", "gemini-1.5-flash", "gemini-1.5-pro"}
	for _, fb := range fallbacks {
		if fb != c.model {
			modelsToTry = append(modelsToTry, fb)
		}
	}

	var lastErr error
	for _, m := range modelsToTry {
		// Attempt up to 2 retries per model with backoff
		for attempt := 0; attempt < 2; attempt++ {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}

			res, err := c.callWithModel(ctx, m, prompt, jsonOutput, temp)
			if err == nil && len(res) > 0 {
				return res, nil
			}

			lastErr = err
			// Check if error is retryable (503 overloaded, 429 rate limit, or network error)
			errStr := strings.ToLower(err.Error())
			isOverloaded := strings.Contains(errStr, "503") || strings.Contains(errStr, "unavailable") || strings.Contains(errStr, "high demand")
			isRateLimit := strings.Contains(errStr, "429") || strings.Contains(errStr, "quota") || strings.Contains(errStr, "resource_exhausted")
			isNotFound := strings.Contains(errStr, "404") || strings.Contains(errStr, "not found")

			if isNotFound {
				// Model doesn't exist, immediately move to next model in list
				break
			}

			if isOverloaded || isRateLimit {
				log.Printf("[Gemini] Model %s busy (%v). Backing off attempt %d/2...", m, err, attempt+1)
				select {
				case <-time.After(time.Duration(1500*(attempt+1)) * time.Millisecond):
				case <-ctx.Done():
					return "", ctx.Err()
				}
			} else {
				// Other error, try next candidate
				break
			}
		}
	}

	return "", fmt.Errorf("all Gemini model candidates failed: %w", lastErr)
}

// CleanJSONResponse extracts clean JSON from markdown code block if present
func CleanJSONResponse(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "```json") {
		raw = strings.TrimPrefix(raw, "```json")
		raw = strings.TrimSuffix(raw, "```")
	} else if strings.HasPrefix(raw, "```") {
		raw = strings.TrimPrefix(raw, "```")
		raw = strings.TrimSuffix(raw, "```")
	}
	return strings.TrimSpace(raw)
}

// AnalyzeResume parses raw resume text into structured fields
func (c *GeminiClient) AnalyzeResume(ctx context.Context, resumeText string) (*models.Resume, error) {
	// Truncate to reasonable size for prompt if needed
	runes := []rune(resumeText)
	if len(runes) > 5000 {
		resumeText = string(runes[:5000])
	}

	prompt := fmt.Sprintf(`Ты эксперт по найму Go (Golang) разработчиков.
Внимательно прочитай резюме кандидата и извлеки ключевую информацию в формате JSON:
- "summary": емкое профессиональное резюме опыта разработчика (2-3 предложения на русском)
- "skills": массив всех технологий и инструментов кандидата (например: ["Golang", "PostgreSQL", "Docker", "Kubernetes", "gRPC", "Kafka", "Redis", "CI/CD", "Linux"])
- "seniority": реальный грейд кандидата ("Junior", "Middle", "Senior", "Lead")
- "desired_role": целевая должность (например: "Senior Golang Backend Developer")

РЕЗЮМЕ КАНДИДАТА:
%s`, resumeText)

	raw, err := c.callAPI(ctx, prompt, true, 0.1)
	if err != nil {
		log.Printf("[Gemini WARNING] AnalyzeResume AI call failed: %v. Using robust heuristic fallback.", err)
		return resume.FallbackAnalyze(resumeText), nil
	}

	var parsed struct {
		Summary     string   `json:"summary"`
		Skills      []string `json:"skills"`
		Seniority   string   `json:"seniority"`
		DesiredRole string   `json:"desired_role"`
	}

	cleanJSON := CleanJSONResponse(raw)
	if err := json.Unmarshal([]byte(cleanJSON), &parsed); err != nil || len(parsed.Skills) == 0 {
		log.Printf("[Gemini WARNING] AnalyzeResume JSON parse error (%v). Using heuristic fallback.", err)
		return resume.FallbackAnalyze(resumeText), nil
	}

	if parsed.Seniority == "" {
		parsed.Seniority = resume.ExtractSeniority(resumeText)
	}
	if parsed.DesiredRole == "" {
		parsed.DesiredRole = resume.ExtractDesiredRole(resumeText)
	}

	return &models.Resume{
		RawText:     resumeText,
		Summary:     parsed.Summary,
		Skills:      parsed.Skills,
		Seniority:   parsed.Seniority,
		DesiredRole: parsed.DesiredRole,
		UpdatedAt:   time.Now(),
	}, nil
}

// MatchJob calculates match percentage and reasons
func (c *GeminiClient) MatchJob(ctx context.Context, resumeText string, v *models.Vacancy) (*models.MatchResult, error) {
	// Truncate inputs to prevent timeout
	resSnippet := resumeText
	if len([]rune(resSnippet)) > 3000 {
		resSnippet = string([]rune(resSnippet)[:3000])
	}

	vacDesc := v.Description
	if len([]rune(vacDesc)) > 2000 {
		vacDesc = string([]rune(vacDesc)[:2000])
	}

	prompt := fmt.Sprintf(`Сравни резюме соискателя и вакансию для Go-разработчика.
Оцени реальное соответствие кандидата вакансии (от 0 до 100%%) с учетом стека технологий, опыта и уровня задач.
Верни строгий JSON с полями:
- "percentage": число от 0 до 100 (если стек совпадает на 80%%+ и грейд подходит — ставь 80-95%%; если нет Go или стек совсем другой — ставь меньше 40%%)
- "matching_skills": массив совпавших навыков кандидата и требований вакансии
- "missing_skills": массив технологий вакансии, которых нет в резюме (или пустой массив [])
- "summary": 1-2 емких предложения с обоснованием оценки на русском языке

РЕЗЮМЕ КАНДИДАТА:
%s

ВАКАНСИЯ:
Позиция: %s
Компания: %s
Требования: %s
Описание:
%s`, resSnippet, v.Title, v.Company, v.Requirements, vacDesc)

	raw, err := c.callAPI(ctx, prompt, true, 0.1)
	if err != nil {
		return nil, err
	}

	var res models.MatchResult
	cleanJSON := CleanJSONResponse(raw)
	if err := json.Unmarshal([]byte(cleanJSON), &res); err != nil {
		return nil, fmt.Errorf("failed to parse match json: %w", err)
	}

	if res.Percentage < 0 {
		res.Percentage = 0
	}
	if res.Percentage > 100 {
		res.Percentage = 100
	}

	return &res, nil
}

// GenerateCoverLetter creates a concise, human-written cover letter
func (c *GeminiClient) GenerateCoverLetter(ctx context.Context, resumeText string, v *models.Vacancy) (string, error) {
	prompt := fmt.Sprintf(`Ты сильный Golang-разработчик. Напиши живое, убедительное сопроводительное письмо для отклика на вакансию.

ПРАВИЛА НАПИСАНИЯ:
1. Письмо должно звучать ЕСТЕСТВЕННО, профессионально и по-человечески, как пишет живой программист в Telegram или на HeadHunter, а не нейросеть!
2. СТРОЖАЙШИЙ ЗАПРЕТ на шаблонные клише:
   - ЗАПРЕЩЕНО: "Здравствуйте! С большим интересом ознакомился с вашей вакансией..."
   - ЗАПРЕЩЕНО: "Я идеальный кандидат на эту роль..."
   - ЗАПРЕЩЕНО: "Уверен, что мой опыт станет ценным активом для вашей компании..."
   - ЗАПРЕЩЕНО: "Буду рад внести вклад в процветание вашей команды..."
   - ЗАПРЕЩЕНО: "С уважением и надеждой на плодотворное сотрудничество..."
3. ОБЪЕМ: короткое, емкое — 2-3 небольших абзаца (80-120 слов).
4. СТРУКТУРА:
   - Простое вежливое приветствие ("Здравствуйте!" или "Добрый день!").
   - Конкретика: упомяни 2-3 ключевые технологии из вакансии, с которыми у кандидата действительно есть опыт из резюме (например: "В проектах активно разрабатывал микросервисы на Go, работал с PostgreSQL, gRPC и настраивал очереди через Kafka").
   - Решение задач: 1 предложение о том, какие похожие задачи кандидат решал (отказоустойчивость, оптимизация запросов, проектирование API).
   - Призыв к действию: "Буду рад созвониться, познакомиться с командой и обсудить детали проекта".
5. ЯЗЫК: на русском (или на естественном английском, если вакансия полностью англоязычная).
6. Верни ТОЛЬКО текст письма, без кавычек и префиксов вроде "Вот письмо:".

РЕЗЮМЕ КАНДИДАТА:
%s

ВАКАНСИЯ:
Позиция: %s
Компания: %s
Требования: %s
Описание:
%s`, resumeText, v.Title, v.Company, v.Requirements, v.Description)

	letter, err := c.callAPI(ctx, prompt, false, 0.4)
	if err != nil {
		log.Printf("[Gemini WARNING] GenerateCoverLetter AI failed: %v. Using dynamic smart template.", err)
		return FallbackCoverLetter(resumeText, v), nil
	}

	cleanLetter := strings.TrimSpace(letter)
	// Remove outer quotes if AI wrapped it
	cleanLetter = strings.TrimPrefix(cleanLetter, "\"")
	cleanLetter = strings.TrimSuffix(cleanLetter, "\"")
	return cleanLetter, nil
}

// FallbackCoverLetter generates a natural human cover letter when AI is unavailable
func FallbackCoverLetter(resumeText string, v *models.Vacancy) string {
	skills := resume.ExtractSkills(resumeText)
	var matched []string
	vacLower := strings.ToLower(v.Title + " " + v.Requirements + " " + v.Description)
	for _, sk := range skills {
		if strings.Contains(vacLower, strings.ToLower(sk)) {
			matched = append(matched, sk)
		}
	}
	if len(matched) == 0 {
		matched = []string{"Golang", "PostgreSQL", "Docker"}
	}

	stackStr := strings.Join(matched, ", ")
	comp := v.Company
	if comp == "" {
		comp = "вашу команду"
	}

	return fmt.Sprintf("Здравствуйте!\n\nЗаинтересовала ваша вакансия %s в %s. Мой основной стек разработки как раз включает %s — проектировал бэкенд-сервисы, работал с базами данных и микросервисной архитектурой.\n\nБуду рад познакомиться, ответить на технические вопросы и подробнее обсудить задачи проекта!", v.Title, comp, stackStr)
}

// ParseAutoApplyCriteria parses natural language instructions into AutoApplyFilter
func (c *GeminiClient) ParseAutoApplyCriteria(ctx context.Context, userPrompt string) (*models.AutoApplyFilter, error) {
	prompt := fmt.Sprintf(`Пользователь задал правила для автоотклика на вакансии обычным языком.
Преобразуй эти требования в структурированный JSON с полями:
- "min_match_pct": минимальный процент совпадения с резюме (целое число от 40 до 95, по умолчанию 60, если не указано)
- "min_salary_rub": минимальная зарплата в рублях (целое число, 0 если не указана)
- "min_salary_usd": минимальная зарплата в долларах/евро (целое число, 0 если не указана)
- "remote_only": true если требуется только удаленная работа/удаленка, иначе false
- "seniority": массив подходящих уровней (например: ["Middle", "Senior", "Lead"])
- "blacklist_keywords": массив слов-исключений (например: ["crypto", "крипта", "казино", "gambling", "1win", "стартап без зарплаты"])
- "whitelist_keywords": массив обязательных слов/технологий, если упомянуты

Запрос пользователя:
"%s"`, userPrompt)

	raw, err := c.callAPI(ctx, prompt, true, 0.1)
	if err != nil {
		log.Printf("[Gemini WARNING] ParseAutoApplyCriteria AI failed: %v. Using regex heuristics.", err)
		return FallbackParseCriteria(userPrompt), nil
	}

	var f models.AutoApplyFilter
	cleanJSON := CleanJSONResponse(raw)
	if err := json.Unmarshal([]byte(cleanJSON), &f); err != nil {
		return FallbackParseCriteria(userPrompt), nil
	}

	f.NaturalPrompt = userPrompt
	f.Enabled = true
	if f.MinMatchPct == 0 {
		f.MinMatchPct = 60
	}

	return &f, nil
}

// FallbackParseCriteria parses user filter via regex heuristics when AI is unavailable
func FallbackParseCriteria(prompt string) *models.AutoApplyFilter {
	lower := strings.ToLower(prompt)
	filter := &models.AutoApplyFilter{
		Enabled:       true,
		NaturalPrompt: prompt,
		MinMatchPct:   60,
	}

	// Remote
	if strings.Contains(lower, "удален") || strings.Contains(lower, "remote") {
		filter.RemoteOnly = true
	}

	// Seniority
	if strings.Contains(lower, "junior") || strings.Contains(lower, "джун") {
		filter.Seniority = append(filter.Seniority, "Junior")
	}
	if strings.Contains(lower, "middle") || strings.Contains(lower, "мидл") {
		filter.Seniority = append(filter.Seniority, "Middle")
	}
	if strings.Contains(lower, "senior") || strings.Contains(lower, "сеньор") || strings.Contains(lower, "синиор") {
		filter.Seniority = append(filter.Seniority, "Senior")
	}
	if strings.Contains(lower, "lead") || strings.Contains(lower, "тимлид") {
		filter.Seniority = append(filter.Seniority, "Lead")
	}

	// Blacklist
	for _, bl := range []string{"крипт", "crypto", "казино", "gambling", "1win", "ставк"} {
		if strings.Contains(lower, bl) {
			filter.BlacklistKeywords = append(filter.BlacklistKeywords, bl)
		}
	}

	// Salary regex: "от 250к", "от 200 000"
	reSalary := regexp.MustCompile(`(?:от|зп|зарплат[а-я]*)\s*(\d+)\s*(?:к|k|000)?`)
	if match := reSalary.FindStringSubmatch(lower); len(match) > 1 {
		var val int
		fmt.Sscanf(match[1], "%d", &val)
		if strings.Contains(match[0], "к") || strings.Contains(match[0], "k") {
			val *= 1000
		} else if val < 1000 && val > 0 {
			val *= 1000
		}
		if val > 10000 {
			filter.MinSalaryRub = val
		}
	}

	return filter
}

// EditResume applies natural language corrections to the user's resume
func (c *GeminiClient) EditResume(ctx context.Context, current *models.Resume, instruction string) (*models.Resume, error) {
	currentSkillsJSON, _ := json.Marshal(current.Skills)

	prompt := fmt.Sprintf(`Ты помощник по настройке резюме разработчика.
Пользователь хочет скорректировать данные своего резюме текстовой инструкцией.

ТЕКУЩЕЕ РЕЗЮМЕ:
- Грейд (seniority): "%s"
- Желаемая роль (desired_role): "%s"
- Стек технологий (skills): %s
- Краткое резюме (summary): "%s"

ИНСТРУКЦИЯ ПОЛЬЗОВАТЕЛЯ ПО ИЗМЕНЕНИЮ:
"%s"

Примени изменения пользователя к текущему резюме и верни обновленный JSON со всеми 4 полями:
- "seniority": грейд ("Junior", "Middle", "Senior", "Lead")
- "desired_role": целевая должность
- "skills": массив всех актуальных технологий (сохрани существующие и добавь/удали запрошенные)
- "summary": обновленное или дополненное краткое описание

Верни ТОЛЬКО строгий JSON.`, current.Seniority, current.DesiredRole, string(currentSkillsJSON), current.Summary, instruction)

	raw, err := c.callAPI(ctx, prompt, true, 0.1)
	if err != nil {
		log.Printf("[Gemini WARNING] EditResume AI call failed: %v. Using fallback editor.", err)
		return FallbackEditResume(current, instruction), nil
	}

	var parsed struct {
		Seniority   string   `json:"seniority"`
		DesiredRole string   `json:"desired_role"`
		Skills      []string `json:"skills"`
		Summary     string   `json:"summary"`
	}

	cleanJSON := CleanJSONResponse(raw)
	if err := json.Unmarshal([]byte(cleanJSON), &parsed); err != nil {
		log.Printf("[Gemini WARNING] EditResume JSON parse failed: %v. Using fallback editor.", err)
		return FallbackEditResume(current, instruction), nil
	}

	updated := *current
	if parsed.Seniority != "" {
		updated.Seniority = parsed.Seniority
	}
	if parsed.DesiredRole != "" {
		updated.DesiredRole = parsed.DesiredRole
	}
	if len(parsed.Skills) > 0 {
		updated.Skills = parsed.Skills
	}
	if parsed.Summary != "" {
		updated.Summary = parsed.Summary
	}
	updated.UpdatedAt = time.Now()
	return &updated, nil
}

// FallbackEditResume applies simple rule-based modifications if Gemini is unreachable
func FallbackEditResume(current *models.Resume, instruction string) *models.Resume {
	updated := *current
	lower := strings.ToLower(instruction)

	// Seniority change
	switch {
	case strings.Contains(lower, "junior") || strings.Contains(lower, "джун") || strings.Contains(lower, "младш"):
		updated.Seniority = "Junior"
	case strings.Contains(lower, "middle") || strings.Contains(lower, "мидл"):
		updated.Seniority = "Middle"
	case strings.Contains(lower, "senior") || strings.Contains(lower, "сеньор") || strings.Contains(lower, "синиор"):
		updated.Seniority = "Senior"
	case strings.Contains(lower, "lead") || strings.Contains(lower, "тимлид") || strings.Contains(lower, "лид"):
		updated.Seniority = "Lead"
	}

	// Desired role change
	if strings.Contains(lower, "должност") || strings.Contains(lower, "роль") || strings.Contains(lower, "позици") {
		for _, r := range []string{"Golang Backend Developer", "Junior Golang Developer", "Middle Go Developer", "Senior Go Developer", "Go Developer"} {
			if strings.Contains(lower, strings.ToLower(r)) {
				updated.DesiredRole = r
				break
			}
		}
	}

	// Add skills if mentioned
	extracted := resume.ExtractSkills(instruction)
	if len(extracted) > 0 {
		seen := make(map[string]bool)
		for _, s := range updated.Skills {
			seen[strings.ToLower(s)] = true
		}
		for _, s := range extracted {
			if !seen[strings.ToLower(s)] {
				updated.Skills = append(updated.Skills, s)
				seen[strings.ToLower(s)] = true
			}
		}
	}

	updated.UpdatedAt = time.Now()
	return &updated
}

