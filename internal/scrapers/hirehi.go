package scrapers

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"strings"
	"time"

	"jobparser/internal/browser"
	"jobparser/internal/models"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

type HireHiScraper struct {
	chrome *browser.ChromeManager
}

func NewHireHiScraper(chrome *browser.ChromeManager) *HireHiScraper {
	return &HireHiScraper{chrome: chrome}
}

func (s *HireHiScraper) Name() string {
	return "HireHi.ru"
}

func (s *HireHiScraper) FetchJobs(ctx context.Context) ([]*models.Vacancy, error) {
	return s.FetchJobsForQuery(ctx, "golang")
}

func (s *HireHiScraper) FetchJobsForQuery(ctx context.Context, query string) ([]*models.Vacancy, error) {
	if query == "" {
		query = "golang"
	}

	var vacancies []*models.Vacancy

	scraperCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	err := s.chrome.WithPage(scraperCtx, func(page *rod.Page) error {
		_ = page.SetUserAgent(&proto.NetworkSetUserAgentOverride{
			UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
		})

		url := "https://hirehi.ru/vacancies/development"
		if err := page.Navigate(url); err != nil {
			return err
		}

		_ = page.Timeout(10 * time.Second).WaitLoad()
		time.Sleep(2 * time.Second)

		// Search or filter cards
		cards, err := page.Elements(".vacancy-card, .vacancies-item, [class*='vacancy-item'], [class*='VacancyCard'], a[href*='/vacanc'], a[href*='/job']")
		if err != nil || len(cards) == 0 {
			log.Printf("[HireHi.ru] No vacancy cards found")
			return nil
		}

		seenURLs := make(map[string]bool)
		for _, card := range cards {
			href, _ := card.Attribute("href")
			if href == nil || *href == "" {
				childLink, err := card.Element("a[href*='/vacanc'], a[href*='/job']")
				if err == nil && childLink != nil {
					href, _ = childLink.Attribute("href")
				}
			}

			if href == nil || *href == "" || seenURLs[*href] {
				continue
			}

			fullURL := *href
			if !strings.HasPrefix(fullURL, "http") {
				fullURL = "https://hirehi.ru" + fullURL
			}

			if fullURL == "https://hirehi.ru/vacancies/development" || strings.HasSuffix(fullURL, "/vacancies") {
				continue
			}

			seenURLs[*href] = true

			text, _ := card.Text()
			text = strings.TrimSpace(text)
			if len(text) < 10 {
				continue
			}

			textLower := strings.ToLower(text)
			qLower := strings.ToLower(query)
			matches := strings.Contains(textLower, qLower)
			if qLower == "golang" || qLower == "go" {
				matches = matches || strings.Contains(textLower, "golang") || strings.Contains(textLower, "backend")
			}
			if !matches && !strings.Contains(textLower, "разработчик") && !strings.Contains(textLower, "developer") {
				continue
			}

			lines := strings.Split(text, "\n")
			var title string
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if len(line) > 5 && len(line) < 100 {
					title = line
					break
				}
			}
			if title == "" {
				title = fmt.Sprintf("%s Developer (HireHi)", strings.ToUpper(query[:1])+query[1:])
			}

			var salary string
			for _, line := range lines {
				lineTrim := strings.TrimSpace(line)
				if strings.Contains(lineTrim, "₽") || strings.Contains(lineTrim, "$") || strings.Contains(lineTrim, "руб") {
					salary = lineTrim
					break
				}
			}

			title = strings.ToValidUTF8(strings.TrimSpace(title), "")
			salary = strings.ToValidUTF8(strings.TrimSpace(salary), "")
			text = strings.ToValidUTF8(strings.TrimSpace(text), "")

			hash := sha256.Sum256([]byte(fullURL))
			vID := fmt.Sprintf("hirehi_%x", hash[:6])

			vacancies = append(vacancies, &models.Vacancy{
				ID:           vID,
				Source:       "HireHi.ru",
				ExternalID:   fullURL,
				Title:        title,
				Company:      "HireHi Employer",
				Salary:       salary,
				Location:     "Удаленно / Офис",
				URL:          fullURL,
				Requirements: text,
				Description:  text,
				PublishedAt:  time.Now(),
				CreatedAt:    time.Now(),
			})
		}
		return nil
	})

	if err != nil {
		log.Printf("[HireHi.ru ERROR] Scraping failed: %v", err)
		return nil, nil
	}
	return vacancies, nil
}
