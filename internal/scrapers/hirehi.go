package scrapers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
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

type HireHiScraper struct {
	chrome *browser.ChromeManager
	client *http.Client
}

func NewHireHiScraper(chrome *browser.ChromeManager) *HireHiScraper {
	return &HireHiScraper{
		chrome: chrome,
		client: &http.Client{Timeout: 15 * time.Second},
	}
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

	searchURL := fmt.Sprintf("https://hirehi.ru/vacancies/development?search=%s", url.QueryEscape(query))

	// 1. Direct HTTP request with Goquery & JSON-LD parser (0.5s, no Chrome overhead)
	vacancies, err := s.fetchHTTP(ctx, searchURL, query)
	if err == nil && len(vacancies) > 0 {
		log.Printf("[HireHi.ru] Successfully fetched %d vacancies via direct HTTP", len(vacancies))
		return vacancies, nil
	}

	if err != nil {
		log.Printf("[HireHi.ru] HTTP fetch returned %v, attempting headless Chrome fallback...", err)
	}

	// 2. Headless Chrome fallback
	return s.fetchBrowser(ctx, searchURL, query)
}

func (s *HireHiScraper) fetchHTTP(ctx context.Context, targetURL, query string) ([]*models.Vacancy, error) {
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
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, err
	}

	var vacancies []*models.Vacancy
	seenURLs := make(map[string]bool)

	// A. Parse .job-card elements
	doc.Find(".job-card").Each(func(i int, card *goquery.Selection) {
		href, _ := card.Attr("href")
		if href == "" {
			return
		}
		fullURL := href
		if !strings.HasPrefix(fullURL, "http") {
			fullURL = "https://hirehi.ru" + fullURL
		}
		if seenURLs[fullURL] {
			return
		}
		seenURLs[fullURL] = true

		title := strings.TrimSpace(card.Find(".job-title").Text())
		if title == "" {
			return
		}

		company := strings.TrimSpace(card.Find(".job-company-name-text").Text())
		if company == "" {
			company = strings.TrimSpace(card.Find(".job-company-name").Text())
		}

		salary := strings.TrimSpace(card.Find(".job-salary").Text())
		if strings.Contains(strings.ToLower(salary), "не указана") {
			salary = ""
		}

		hash := sha256.Sum256([]byte(fullURL))
		vID := fmt.Sprintf("hirehi_%x", hash[:6])

		vacancies = append(vacancies, &models.Vacancy{
			ID:           vID,
			Source:       "HireHi.ru",
			ExternalID:   fullURL,
			Title:        title,
			Company:      company,
			Salary:       salary,
			Location:     "Удаленно / РФ",
			URL:          fullURL,
			Requirements: query,
			Description:  title,
			PublishedAt:  time.Now(),
			CreatedAt:    time.Now(),
		})
	})

	// B. If HTML cards were empty, parse embedded application/ld+json ItemList
	if len(vacancies) == 0 {
		doc.Find("script[type='application/ld+json']").Each(func(i int, s *goquery.Selection) {
			text := s.Text()
			if !strings.Contains(text, "ItemList") {
				return
			}
			var ldData struct {
				Type     string `json:"@type"`
				ItemList []struct {
					Name string `json:"name"`
					URL  string `json:"url"`
				} `json:"itemListElement"`
			}
			if err := json.Unmarshal([]byte(text), &ldData); err == nil && len(ldData.ItemList) > 0 {
				for _, item := range ldData.ItemList {
					if item.URL == "" || item.Name == "" || seenURLs[item.URL] {
						continue
					}
					seenURLs[item.URL] = true

					hash := sha256.Sum256([]byte(item.URL))
					vID := fmt.Sprintf("hirehi_%x", hash[:6])

					vacancies = append(vacancies, &models.Vacancy{
						ID:           vID,
						Source:       "HireHi.ru",
						ExternalID:   item.URL,
						Title:        item.Name,
						Company:      "HireHi Partner",
						Salary:       "",
						Location:     "Удаленно / РФ",
						URL:          item.URL,
						Requirements: query,
						Description:  item.Name,
						PublishedAt:  time.Now(),
						CreatedAt:    time.Now(),
					})
				}
			}
		})
	}

	return vacancies, nil
}

func (s *HireHiScraper) fetchBrowser(ctx context.Context, targetURL, query string) ([]*models.Vacancy, error) {
	if s.chrome == nil {
		return nil, fmt.Errorf("chrome manager not available")
	}

	var vacancies []*models.Vacancy
	scraperCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()

	err := s.chrome.WithPage(scraperCtx, func(page *rod.Page) error {
		if err := page.Navigate(targetURL); err != nil {
			return err
		}

		_ = page.Timeout(10 * time.Second).WaitLoad()
		time.Sleep(2 * time.Second)

		cards, err := page.Elements(".job-card, [class*='job-card'], a[href*='/development/']")
		if err != nil || len(cards) == 0 {
			log.Printf("[HireHi.ru] No job cards found on page")
			return nil
		}

		seenURLs := make(map[string]bool)
		for _, card := range cards {
			href, _ := card.Attribute("href")
			if href == nil || *href == "" {
				continue
			}

			fullURL := *href
			if !strings.HasPrefix(fullURL, "http") {
				fullURL = "https://hirehi.ru" + fullURL
			}

			if seenURLs[fullURL] {
				continue
			}
			seenURLs[fullURL] = true

			var title string
			if titleEl, err := card.Element(".job-title"); err == nil && titleEl != nil {
				title, _ = titleEl.Text()
			}
			if title == "" {
				title, _ = card.Text()
				lines := strings.Split(strings.TrimSpace(title), "\n")
				if len(lines) > 0 {
					title = lines[0]
				}
			}
			title = strings.TrimSpace(title)
			if title == "" {
				continue
			}

			var company string
			if compEl, err := card.Element(".job-company-name-text, .job-company-name"); err == nil && compEl != nil {
				company, _ = compEl.Text()
				company = strings.TrimSpace(company)
			}

			var salary string
			if salEl, err := card.Element(".job-salary"); err == nil && salEl != nil {
				salary, _ = salEl.Text()
				salary = strings.TrimSpace(salary)
			}

			hash := sha256.Sum256([]byte(fullURL))
			vID := fmt.Sprintf("hirehi_%x", hash[:6])

			vacancies = append(vacancies, &models.Vacancy{
				ID:           vID,
				Source:       "HireHi.ru",
				ExternalID:   fullURL,
				Title:        title,
				Company:      company,
				Salary:       salary,
				Location:     "Удаленно / РФ",
				URL:          fullURL,
				Requirements: query,
				Description:  title,
				PublishedAt:  time.Now(),
				CreatedAt:    time.Now(),
			})
		}
		return nil
	})

	return vacancies, err
}
