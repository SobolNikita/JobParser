package scrapers

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"jobparser/internal/models"
	"jobparser/internal/storage"

	"github.com/PuerkitoBio/goquery"
)

var (
	salaryRegex = regexp.MustCompile(`(?i)(\d[\d\s]{1,10}\s*(?:руб|rub|₽|\$|usd|eur|€|k)\b|\b(?:от|до)\s*\d[\d\s]{1,10})`)
	remoteRegex = regexp.MustCompile(`(?i)(удаленк|remote|гибрид|офис|удаленно)`)
)

type TelegramScraper struct {
	storage         *storage.Storage
	defaultChannels []string
	httpClient      *http.Client
}

func NewTelegramScraper(st *storage.Storage, defaults []string) *TelegramScraper {
	if len(defaults) == 0 {
		defaults = []string{"jobgolang", "golang_job", "godevjob"}
	}
	return &TelegramScraper{
		storage:         st,
		defaultChannels: defaults,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

func (s *TelegramScraper) Name() string {
	return "Telegram Channels"
}

func (s *TelegramScraper) FetchJobs(ctx context.Context) ([]*models.Vacancy, error) {
	return s.FetchJobsForQuery(ctx, "golang")
}

func (s *TelegramScraper) FetchJobsForQuery(ctx context.Context, query string) ([]*models.Vacancy, error) {
	if query == "" {
		query = "golang"
	}

	channels := make(map[string]bool)
	for _, ch := range s.defaultChannels {
		channels[strings.TrimPrefix(ch, "@")] = true
	}

	if s.storage != nil {
		if dbChs, err := s.storage.GetChannels(); err == nil {
			for _, ch := range dbChs {
				channels[strings.TrimPrefix(ch, "@")] = true
			}
		}
	}

	var allVacancies []*models.Vacancy
	for ch := range channels {
		if ctx.Err() != nil {
			break
		}
		vacancies, err := s.fetchChannel(ctx, ch, query)
		if err != nil {
			log.Printf("[Telegram] Error fetching channel @%s: %v", ch, err)
			continue
		}
		allVacancies = append(allVacancies, vacancies...)
	}

	return allVacancies, nil
}

func (s *TelegramScraper) fetchChannel(ctx context.Context, channelName string, query string) ([]*models.Vacancy, error) {
	url := fmt.Sprintf("https://t.me/s/%s", channelName)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en;q=0.8")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http status %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, err
	}

	var vacancies []*models.Vacancy

	doc.Find(".tgme_widget_message").Each(func(i int, sMsg *goquery.Selection) {
		textEl := sMsg.Find(".tgme_widget_message_text")
		if textEl.Length() == 0 {
			return
		}

		fullText := strings.TrimSpace(textEl.Text())
		fullText = strings.ToValidUTF8(fullText, "")
		if len(fullText) < 50 {
			return
		}

		// Filter for job offerings
		textLower := strings.ToLower(fullText)
		qLower := strings.ToLower(query)
		hasTargetKeyword := strings.Contains(textLower, qLower)
		if qLower == "golang" || qLower == "go" {
			hasTargetKeyword = hasTargetKeyword || strings.Contains(textLower, "golang") || strings.Contains(textLower, "backend")
		}
		hasJobIndicator := strings.Contains(textLower, "вакансия") ||
			strings.Contains(textLower, "разработчик") ||
			strings.Contains(textLower, "developer") ||
			strings.Contains(textLower, "стек") ||
			strings.Contains(textLower, "требования") ||
			strings.Contains(textLower, "отклик") ||
			strings.Contains(textLower, "зп") ||
			strings.Contains(textLower, "зарплат") ||
			strings.Contains(textLower, "вилка")

		if !hasTargetKeyword || !hasJobIndicator {
			return
		}

		// Extract post URL
		postLinkEl := sMsg.Find("a.tgme_widget_message_date")
		postURL, exists := postLinkEl.Attr("href")
		if !exists || postURL == "" {
			postURL = fmt.Sprintf("https://t.me/%s", channelName)
		}
		postURL = strings.ToValidUTF8(postURL, "")

		// Extract title
		var title string
		boldEl := textEl.Find("b").First()
		if boldEl.Length() > 0 {
			title = strings.TrimSpace(boldEl.Text())
		}
		if title == "" || len(title) > 80 {
			lines := strings.Split(fullText, "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if len(line) > 5 && len(line) < 80 {
					title = line
					break
				}
			}
		}
		if title == "" {
			title = fmt.Sprintf("%s Вакансия (@%s)", strings.ToUpper(query[:1])+query[1:], channelName)
		}

		// Extract salary
		var salary string
		if sm := salaryRegex.FindString(fullText); sm != "" {
			salary = strings.TrimSpace(sm)
		}

		// Extract location/remote
		var location string
		if rm := remoteRegex.FindString(fullText); rm != "" {
			location = strings.TrimSpace(rm)
		}

		// Company name detection
		company := "@" + channelName
		for _, line := range strings.Split(fullText, "\n") {
			lineLower := strings.ToLower(line)
			if strings.Contains(lineLower, "компания:") || strings.Contains(lineLower, "company:") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					company = strings.TrimSpace(parts[1])
				}
				break
			}
		}

		title = strings.ToValidUTF8(strings.TrimSpace(title), "")
		company = strings.ToValidUTF8(strings.TrimSpace(company), "")
		salary = strings.ToValidUTF8(strings.TrimSpace(salary), "")
		location = strings.ToValidUTF8(strings.TrimSpace(location), "")

		hash := sha256.Sum256([]byte(postURL))
		vID := fmt.Sprintf("tg_%x", hash[:6])

		vacancies = append(vacancies, &models.Vacancy{
			ID:           vID,
			Source:       "Telegram @" + channelName,
			ExternalID:   postURL,
			Title:        title,
			Company:      company,
			Salary:       salary,
			Location:     location,
			URL:          postURL,
			Description:  fullText,
			Requirements: fullText,
			PublishedAt:  time.Now(),
			CreatedAt:    time.Now(),
		})
	})

	return vacancies, nil
}
