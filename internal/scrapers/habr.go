package scrapers

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"jobparser/internal/browser"
	"jobparser/internal/models"

	"github.com/PuerkitoBio/goquery"
	"github.com/go-rod/rod"
)

type HabrScraper struct {
	chrome *browser.ChromeManager
	client *http.Client
}

func NewHabrScraper(chrome *browser.ChromeManager) *HabrScraper {
	return &HabrScraper{
		chrome: chrome,
		client: &http.Client{Timeout: 15 * time.Second},
	}
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

	searchURL := fmt.Sprintf("https://career.habr.com/vacancies?q=%s&sort=date", url.QueryEscape(query))

	// 1. Fast HTTP fetch with Goquery (0.5s, avoids browser overload)
	vacancies, err := s.fetchHTTP(ctx, searchURL)
	if err == nil && len(vacancies) > 0 {
		log.Printf("[Habr Career] Successfully fetched %d vacancies via direct HTTP", len(vacancies))
		return vacancies, nil
	}

	if err != nil {
		log.Printf("[Habr Career] HTTP fetch returned %v, attempting headless Chrome fallback...", err)
	}

	// 2. Headless Chrome fallback
	return s.fetchBrowser(ctx, searchURL)
}

func (s *HabrScraper) fetchHTTP(ctx context.Context, targetURL string) ([]*models.Vacancy, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", targetURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, err
	}

	var vacancies []*models.Vacancy
	doc.Find(".vacancy-card").Each(func(i int, card *goquery.Selection) {
		titleEl := card.Find(".vacancy-card__title a")
		if titleEl.Length() == 0 {
			return
		}

		title := strings.TrimSpace(titleEl.Text())
		href, _ := titleEl.Attr("href")
		if href == "" {
			return
		}

		fullURL := href
		if !strings.HasPrefix(fullURL, "http") {
			fullURL = "https://career.habr.com" + fullURL
		}

		company := strings.TrimSpace(card.Find(".vacancy-card__company-title").Text())
		salary := strings.TrimSpace(card.Find(".vacancy-card__salary").Text())
		meta := strings.TrimSpace(card.Find(".vacancy-card__meta").Text())

		var skills []string
		card.Find(".vacancy-card__skills a").Each(func(_ int, sk *goquery.Selection) {
			t := strings.TrimSpace(sk.Text())
			if t != "" {
				skills = append(skills, t)
			}
		})

		hash := sha256.Sum256([]byte(fullURL))
		vID := fmt.Sprintf("habr_%x", hash[:6])

		desc := meta
		if len(skills) > 0 {
			if desc != "" {
				desc += "\nНавыки: " + strings.Join(skills, ", ")
			} else {
				desc = "Навыки: " + strings.Join(skills, ", ")
			}
		}

		vacancies = append(vacancies, &models.Vacancy{
			ID:           vID,
			Source:       "career.habr.com",
			ExternalID:   fullURL,
			Title:        title,
			Company:      company,
			Salary:       salary,
			Location:     meta,
			URL:          fullURL,
			Requirements: strings.Join(skills, ", "),
			Description:  desc,
			PublishedAt:  time.Now(),
			CreatedAt:    time.Now(),
		})
	})

	return vacancies, nil
}

func (s *HabrScraper) fetchBrowser(ctx context.Context, searchURL string) ([]*models.Vacancy, error) {
	if s.chrome == nil {
		return nil, fmt.Errorf("chrome manager not available")
	}

	var vacancies []*models.Vacancy
	scraperCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()

	err := s.chrome.WithPage(scraperCtx, func(page *rod.Page) error {
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

			desc := meta
			if len(skills) > 0 {
				if desc != "" {
					desc += "\nНавыки: " + strings.Join(skills, ", ")
				} else {
					desc = "Навыки: " + strings.Join(skills, ", ")
				}
			}

			vacancies = append(vacancies, &models.Vacancy{
				ID:           vID,
				Source:       "career.habr.com",
				ExternalID:   fullURL,
				Title:        title,
				Company:      company,
				Salary:       salary,
				Location:     meta,
				URL:          fullURL,
				Requirements: strings.Join(skills, ", "),
				Description:  desc,
				PublishedAt:  time.Now(),
				CreatedAt:    time.Now(),
			})
		}
		return nil
	})

	return vacancies, err
}
