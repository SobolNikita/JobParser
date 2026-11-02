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

	searchURLs := []string{
		fmt.Sprintf("https://hirehi.ru/vacancies?search=%s", url.QueryEscape(query)),
		fmt.Sprintf("https://hirehi.ru/vacancies/development?search=%s", url.QueryEscape(query)),
	}

	for _, searchURL := range searchURLs {
		vacancies, err := s.fetchHTTP(ctx, searchURL, query)
		if err == nil && len(vacancies) > 0 {
			log.Printf("[HireHi.ru] Successfully fetched %d vacancies via direct HTTP (%s)", len(vacancies), searchURL)
			return vacancies, nil
		}
	}

	// Headless Chrome fallback
	return s.fetchBrowser(ctx, searchURLs[0], query)
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

	// A. Parse job card elements (strictly cards, no category links!)
	doc.Find(".job-card, [class*='job-card'], [class*='job_card'], [class*='VacancyCard']").Each(func(i int, card *goquery.Selection) {
		href, _ := card.Attr("href")
		if href == "" {
			href, _ = card.Find("a").Attr("href")
		}
		if href == "" {
			return
		}
		fullURL := href
		if !strings.HasPrefix(fullURL, "http") {
			fullURL = "https://hirehi.ru" + fullURL
		}
		if seenURLs[fullURL] || isHireHiCategoryURL(fullURL) {
			return
		}

		title := strings.TrimSpace(card.Find(".job-title, [class*='title']").Text())
		if title == "" {
			title = strings.TrimSpace(card.Text())
			lines := strings.Split(title, "\n")
			if len(lines) > 0 {
				title = strings.TrimSpace(lines[0])
			}
		}
		if title == "" || len(title) < 3 {
			return
		}

		if !models.IsRelevantToStack(query, title, "", "") {
			return
		}

		company := strings.TrimSpace(card.Find(".job-company-name-text, .job-company-name, [class*='company']").Text())
		if company == "" {
			company = "HireHi Partner"
		}

		salary := strings.TrimSpace(card.Find(".job-salary, [class*='salary']").Text())
		if strings.Contains(strings.ToLower(salary), "не указана") {
			salary = ""
		}

		seenURLs[fullURL] = true
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
					if !models.IsRelevantToStack(query, item.Name, "", "") {
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

	// C. If still empty, parse Next.js __NEXT_DATA__
	if len(vacancies) == 0 {
		nextDataScript := doc.Find("script#__NEXT_DATA__")
		if nextDataScript.Length() > 0 {
			var nextData map[string]interface{}
			if err := json.Unmarshal([]byte(nextDataScript.Text()), &nextData); err == nil {
				extractHireHiJSON(nextData, query, seenURLs, &vacancies)
			}
		}
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

		cards, err := page.Elements(".job-card, [class*='job-card'], [class*='VacancyCard']")
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

			if seenURLs[fullURL] || isHireHiCategoryURL(fullURL) {
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
			if title == "" || !models.IsRelevantToStack(query, title, "", "") {
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

func extractHireHiJSON(data interface{}, query string, seenURLs map[string]bool, vacancies *[]*models.Vacancy) {
	switch val := data.(type) {
	case map[string]interface{}:
		var title, comp, sal, link string
		if t, ok := val["title"].(string); ok {
			title = t
		} else if n, ok := val["name"].(string); ok {
			title = n
		}

		if u, ok := val["url"].(string); ok {
			link = u
		} else if s, ok := val["slug"].(string); ok {
			link = "/vacancies/" + s
		} else if id, ok := val["id"].(string); ok {
			link = "/vacancies/" + id
		}

		if c, ok := val["company"].(string); ok {
			comp = c
		} else if cm, ok := val["company"].(map[string]interface{}); ok {
			if cn, ok := cm["name"].(string); ok {
				comp = cn
			}
		}

		if s, ok := val["salary"].(string); ok {
			sal = s
		}

		if title != "" && link != "" {
			if isHireHiCategoryURL(link) || !models.IsRelevantToStack(query, title, "", "") {
				return
			}
			if !strings.HasPrefix(link, "http") {
				link = "https://hirehi.ru" + link
			}
			if !seenURLs[link] {
				seenURLs[link] = true
				hash := sha256.Sum256([]byte(link))
				vID := fmt.Sprintf("hirehi_%x", hash[:6])
				if comp == "" {
					comp = "HireHi Partner"
				}
				*vacancies = append(*vacancies, &models.Vacancy{
					ID:           vID,
					Source:       "HireHi.ru",
					ExternalID:   link,
					Title:        title,
					Company:      comp,
					Salary:       sal,
					Location:     "Удаленно / РФ",
					URL:          link,
					Requirements: query,
					Description:  title,
					PublishedAt:  time.Now(),
					CreatedAt:    time.Now(),
				})
			}
		}

		for _, item := range val {
			extractHireHiJSON(item, query, seenURLs, vacancies)
		}
	case []interface{}:
		for _, item := range val {
			extractHireHiJSON(item, query, seenURLs, vacancies)
		}
	}
}

func isHireHiCategoryURL(rawURL string) bool {
	rawURL = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(rawURL), "/"))
	if strings.HasSuffix(rawURL, "/vacancies") || strings.HasSuffix(rawURL, "/vacancies/development") {
		return true
	}
	parts := strings.Split(rawURL, "/")
	for i, p := range parts {
		if p == "vacancies" && i == len(parts)-2 {
			slug := parts[i+1]
			categories := map[string]bool{
				"web-design": true, "design": true, "development": true, "marketing": true,
				"finance": true, "accounting": true, "legal": true, "hr": true,
				"analytics": true, "qa": true, "devops": true, "sales": true,
				"management": true, "product": true, "support": true, "mobile": true,
			}
			if categories[slug] {
				return true
			}
		}
	}
	return false
}
