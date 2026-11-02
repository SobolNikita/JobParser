package scrapers

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"jobparser/internal/browser"
	"jobparser/internal/models"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

type HHScraper struct {
	chrome   *browser.ChromeManager
	siteName string
	startURL string
}

func NewHHScraper(chrome *browser.ChromeManager, siteName, startURL string) *HHScraper {
	return &HHScraper{
		chrome:   chrome,
		siteName: siteName,
		startURL: startURL,
	}
}

func (s *HHScraper) Name() string {
	return s.siteName
}

func (s *HHScraper) FetchJobs(ctx context.Context) ([]*models.Vacancy, error) {
	return s.FetchJobsForQuery(ctx, "golang")
}

func (s *HHScraper) FetchJobsForQuery(ctx context.Context, query string) ([]*models.Vacancy, error) {
	if query == "" {
		query = "golang"
	}

	targetURL := s.startURL
	if strings.Contains(s.startURL, "text=") {
		targetURL = fmt.Sprintf("https://%s/search/vacancy?text=%s&order_by=publication_time&items_on_page=50", s.siteName, url.QueryEscape(query))
	}

	var vacancies []*models.Vacancy

	scraperCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	err := s.chrome.WithPage(scraperCtx, func(page *rod.Page) error {
		if err := page.Navigate(targetURL); err != nil {
			return err
		}

		_ = page.Timeout(12 * time.Second).WaitLoad()
		time.Sleep(2 * time.Second)

		// Dismiss region popup / cookie banner if present
		if confirmBtn, err := page.Element("[data-qa='region-clarification-confirm'], button[data-qa*='confirm']"); err == nil && confirmBtn != nil {
			_ = confirmBtn.Click(proto.InputMouseButtonLeft, 1)
			time.Sleep(500 * time.Millisecond)
		}

		// Scroll to trigger lazy-loaded cards
		_ = page.Mouse.Scroll(0, 600, 3)
		time.Sleep(1 * time.Second)

		// Broad selector matching both legacy and 2024-2026 HeadHunter/Rabota.by redesigned cards
		cards, err := page.Elements(`
			[data-qa*='vacancy-serp__vacancy'],
			.serp-item,
			[class*='serp-item'],
			[class*='vacancy-card'],
			div[data-qa*='vacancy'],
			[data-qa='vacancy-serp__vacancy_standard'],
			[data-qa='vacancy-serp__vacancy_premium']
		`)
		if err != nil || len(cards) == 0 {
			// Fallback: search for direct vacancy links on page
			cards, err = page.Elements("a[data-qa*='vacancy-title'], a[data-qa*='serp-item__title'], a[href*='/vacancy/']")
			if err != nil || len(cards) == 0 {
				log.Printf("[%s] No vacancy card elements found on page", s.siteName)
				return nil
			}
		}

		seenURLs := make(map[string]bool)

		for _, card := range cards {
			// Check if the element itself is the vacancy link
			var titleEl *rod.Element
			tagName, _ := card.Property("tagName")
			if strings.EqualFold(tagName.String(), "a") {
				titleEl = card
			} else {
				titleEl, err = card.Element("a[data-qa*='vacancy-title'], a[data-qa*='serp-item__title'], a[href*='/vacancy/']")
				if err != nil || titleEl == nil {
					continue
				}
			}

			title, _ := titleEl.Text()
			href, _ := titleEl.Attribute("href")
			if href == nil || *href == "" {
				continue
			}

			fullURL := *href
			if !strings.HasPrefix(fullURL, "http") {
				if strings.Contains(s.siteName, "rabota.by") {
					fullURL = "https://rabota.by" + fullURL
				} else {
					fullURL = "https://hh.ru" + fullURL
				}
			}

			if idx := strings.Index(fullURL, "?"); idx != -1 {
				fullURL = fullURL[:idx]
			}

			if seenURLs[fullURL] {
				continue
			}
			seenURLs[fullURL] = true

			var company string
			if compEl, err := card.Element("[data-qa*='employer'], [data-qa*='company'], [class*='employer']"); err == nil && compEl != nil {
				company, _ = compEl.Text()
			}

			var salary string
			if salEl, err := card.Element("[data-qa*='compensation'], [data-qa*='salary'], [class*='compensation']"); err == nil && salEl != nil {
				salary, _ = salEl.Text()
			}

			var reqs string
			if reqEl, err := card.Element("[data-qa*='snippet_requirement'], [data-qa*='requirement']"); err == nil && reqEl != nil {
				reqs, _ = reqEl.Text()
			}

			var resp string
			if respEl, err := card.Element("[data-qa*='snippet_responsibility'], [data-qa*='responsibility']"); err == nil && respEl != nil {
				resp, _ = respEl.Text()
			}

			var location string
			if locEl, err := card.Element("[data-qa*='address'], [class*='address']"); err == nil && locEl != nil {
				location, _ = locEl.Text()
			}

			fullURL = strings.ToValidUTF8(fullURL, "")
			title = strings.ToValidUTF8(strings.TrimSpace(title), "")
			company = strings.ToValidUTF8(strings.TrimSpace(company), "")
			salary = strings.ToValidUTF8(strings.TrimSpace(salary), "")
			location = strings.ToValidUTF8(strings.TrimSpace(location), "")
			reqs = strings.ToValidUTF8(strings.TrimSpace(reqs), "")
			resp = strings.ToValidUTF8(strings.TrimSpace(resp), "")

			if title == "" {
				continue
			}

			hash := sha256.Sum256([]byte(fullURL))
			vID := fmt.Sprintf("%s_%x", strings.ReplaceAll(s.siteName, ".", "_"), hash[:6])

			desc := reqs
			if resp != "" {
				if desc != "" {
					desc += "\n" + resp
				} else {
					desc = resp
				}
			}

			vacancies = append(vacancies, &models.Vacancy{
				ID:           vID,
				Source:       s.siteName,
				ExternalID:   fullURL,
				Title:        title,
				Company:      company,
				Salary:       salary,
				Location:     location,
				URL:          fullURL,
				Requirements: reqs,
				Description:  desc,
				PublishedAt:  time.Now(),
				CreatedAt:    time.Now(),
			})
		}
		return nil
	})

	if err != nil {
		log.Printf("[%s ERROR] Scraping failed: %v", s.siteName, err)
		return nil, nil
	}
	return vacancies, nil
}
