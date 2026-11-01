package scrapers

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"strings"
	"time"

	"jobparser/internal/browser"
	"jobparser/internal/models"

	"github.com/go-rod/rod"
)

type HabrScraper struct {
	chrome *browser.ChromeManager
}

func NewHabrScraper(chrome *browser.ChromeManager) *HabrScraper {
	return &HabrScraper{chrome: chrome}
}

func (s *HabrScraper) Name() string {
	return "Habr Career"
}

func (s *HabrScraper) FetchJobs(ctx context.Context) ([]*models.Vacancy, error) {
	return s.FetchJobsForQuery(ctx, "golang")
}

func (s *HabrScraper) FetchJobsForQuery(ctx context.Context, query string) ([]*models.Vacancy, error) {
	if query == "" {
		query = "golang"
	}

	var vacancies []*models.Vacancy

	scraperCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	err := s.chrome.WithPage(scraperCtx, func(page *rod.Page) error {
		searchURL := fmt.Sprintf("https://career.habr.com/vacancies?q=%s&sort=date", url.QueryEscape(query))
		if err := page.Navigate(searchURL); err != nil {
			return err
		}
		_ = page.Timeout(10 * time.Second).WaitLoad()
		time.Sleep(2 * time.Second)

		cards, err := page.Elements(".vacancy-card")
		if err != nil {
			return err
		}

		for _, card := range cards {
			titleEl, err := card.Element(".vacancy-card__title a")
			if err != nil || titleEl == nil {
				continue
			}

			title, _ := titleEl.Text()
			href, _ := titleEl.Attribute("href")
			if href == nil || *href == "" {
				continue
			}

			fullURL := *href
			if !strings.HasPrefix(fullURL, "http") {
				fullURL = "https://career.habr.com" + fullURL
			}

			var company string
			if compEl, err := card.Element(".vacancy-card__company-title"); err == nil && compEl != nil {
				company, _ = compEl.Text()
			}

			var salary string
			if salEl, err := card.Element(".vacancy-card__salary"); err == nil && salEl != nil {
				salary, _ = salEl.Text()
			}

			var skills []string
			if skillEls, err := card.Elements(".vacancy-card__skills a"); err == nil {
				for _, sk := range skillEls {
					if t, err := sk.Text(); err == nil && t != "" {
						skills = append(skills, strings.TrimSpace(t))
					}
				}
			}

			var meta string
			if metaEl, err := card.Element(".vacancy-card__meta"); err == nil && metaEl != nil {
				meta, _ = metaEl.Text()
			}

			fullURL = strings.ToValidUTF8(fullURL, "")
			title = strings.ToValidUTF8(strings.TrimSpace(title), "")
			company = strings.ToValidUTF8(strings.TrimSpace(company), "")
			salary = strings.ToValidUTF8(strings.TrimSpace(salary), "")
			meta = strings.ToValidUTF8(strings.TrimSpace(meta), "")

			hash := sha256.Sum256([]byte(fullURL))
			vID := fmt.Sprintf("habr_%x", hash[:6])

			vacancies = append(vacancies, &models.Vacancy{
				ID:           vID,
				Source:       "Habr Career",
				ExternalID:   fullURL,
				Title:        title,
				Company:      company,
				Salary:       salary,
				Location:     meta,
				URL:          fullURL,
				Requirements: strings.Join(skills, ", "),
				Description:  fmt.Sprintf("%s\nСтек: %s", meta, strings.Join(skills, ", ")),
				PublishedAt:  time.Now(),
				CreatedAt:    time.Now(),
			})
		}
		return nil
	})

	if err != nil {
		return nil, err
	}
	return vacancies, nil
}
