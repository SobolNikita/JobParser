package scrapers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
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
	"github.com/go-rod/rod/lib/proto"
)

type HHScraper struct {
	chrome   *browser.ChromeManager
	siteName string
	startURL string
	client   *http.Client
}

func NewHHScraper(chrome *browser.ChromeManager, siteName, startURL string) *HHScraper {
	return &HHScraper{
		chrome:   chrome,
		siteName: siteName,
		startURL: startURL,
		client:   &http.Client{Timeout: 15 * time.Second},
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

	// 1. Try native RSS feeds first (instant 0.3s, bypasses DDoS-Guard and doesn't require OAuth)
	vacancies, err := s.fetchRSS(ctx, query)
	if err == nil && len(vacancies) > 0 {
		log.Printf("[%s] Successfully fetched %d vacancies via native RSS feed", s.siteName, len(vacancies))
		return vacancies, nil
	}

	if err != nil {
		log.Printf("[%s] RSS fetch attempt returned: %v. Trying REST API...", s.siteName, err)
	}

	// 2. Try REST API
	vacancies, err = s.fetchAPI(ctx, query)
	if err == nil && len(vacancies) > 0 {
		log.Printf("[%s] Successfully fetched %d vacancies via official API", s.siteName, len(vacancies))
		return vacancies, nil
	}

	if err != nil {
		log.Printf("[%s] API fetch attempt returned: %v. Running browser automation...", s.siteName, err)
	}

	// 3. Headless Chrome automation fallback
	return s.fetchBrowser(ctx, query)
}

func (s *HHScraper) fetchRSS(ctx context.Context, query string) ([]*models.Vacancy, error) {
	var rssURLs []string
	if strings.Contains(s.siteName, "rabota.by") {
		rssURLs = append(rssURLs,
			fmt.Sprintf("https://rabota.by/search/vacancy/rss?text=%s&enable_snippets=true", url.QueryEscape(query)),
			fmt.Sprintf("https://rabota.by/search/vacancy/rss?text=%s&area=16&enable_snippets=true", url.QueryEscape(query)),
		)
	} else {
		// Remote vacancies across all countries and cities
		rssURLs = append(rssURLs,
			fmt.Sprintf("https://hh.ru/search/vacancy/rss?text=%s&schedule=remote&enable_snippets=true", url.QueryEscape(query)),
			fmt.Sprintf("https://hh.ru/search/vacancy/rss?text=%s&enable_snippets=true", url.QueryEscape(query)),
		)
	}

	type hhRSSRoot struct {
		XMLName xml.Name `xml:"rss"`
		Channel struct {
			Title string `xml:"title"`
			Items []struct {
				Title       string `xml:"title"`
				Link        string `xml:"link"`
				Description string `xml:"description"`
				PubDate     string `xml:"pubDate"`
				Author      string `xml:"author"`
			} `xml:"item"`
		} `xml:"channel"`
	}

	var vacancies []*models.Vacancy
	seenURLs := make(map[string]bool)

	for _, rssURL := range rssURLs {
		req, err := http.NewRequestWithContext(ctx, "GET", rssURL, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
		req.Header.Set("Accept", "application/rss+xml,application/xml,text/xml;q=0.9,*/*;q=0.8")
		req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7")

		resp, err := s.client.Do(req)
		if err != nil {
			continue
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			continue
		}

		var root hhRSSRoot
		decodeErr := xml.NewDecoder(resp.Body).Decode(&root)
		resp.Body.Close()
		if decodeErr != nil {
			log.Printf("[%s] RSS decode error for %s: %v", s.siteName, rssURL, decodeErr)
			continue
		}

		for _, item := range root.Channel.Items {
			link := item.Link
			if idx := strings.Index(link, "?"); idx != -1 {
				link = link[:idx]
			}
			if link == "" || seenURLs[link] {
				continue
			}

			if strings.Contains(s.siteName, "rabota.by") && strings.Contains(link, "hh.ru") {
				link = strings.Replace(link, "hh.ru", "rabota.by", 1)
			}

			title := strings.TrimSpace(item.Title)
			salary := ""
			if openIdx := strings.LastIndex(title, "("); openIdx != -1 && strings.HasSuffix(title, ")") {
				bracketContent := title[openIdx+1 : len(title)-1]
				if strings.Contains(bracketContent, "руб") || strings.Contains(bracketContent, "$") || strings.Contains(bracketContent, "€") || strings.Contains(bracketContent, "USD") || strings.Contains(bracketContent, "EUR") || strings.Contains(bracketContent, "от") || strings.Contains(bracketContent, "до") {
					salary = bracketContent
					title = strings.TrimSpace(title[:openIdx])
				}
			}

			descDoc, _ := goquery.NewDocumentFromReader(strings.NewReader("<div>" + item.Description + "</div>"))
			descText := item.Description
			company := item.Author
			if descDoc != nil {
				descText = strings.TrimSpace(descDoc.Text())
				descDoc.Find("p, div, span").Each(func(i int, sel *goquery.Selection) {
					t := strings.TrimSpace(sel.Text())
					if strings.HasPrefix(strings.ToLower(t), "компания:") || strings.HasPrefix(strings.ToLower(t), "работодатель:") {
						parts := strings.SplitN(t, ":", 2)
						if len(parts) == 2 && company == "" {
							company = strings.TrimSpace(parts[1])
						}
					}
				})
			}

			if company == "" {
				company = "HeadHunter Partner"
			}

			location := "Удаленно / РФ и СНГ"
			if strings.Contains(s.siteName, "rabota.by") {
				location = "Беларусь / Минск"
			}

			isRemote := strings.Contains(rssURL, "schedule=remote") ||
				strings.Contains(strings.ToLower(title), "удален") ||
				strings.Contains(strings.ToLower(descText), "удален")

			if isRemote && !strings.Contains(strings.ToLower(location), "удален") {
				location += " (Удаленно)"
			}

			seenURLs[link] = true
			hash := sha256.Sum256([]byte(link))
			vID := fmt.Sprintf("%s_%x", strings.ReplaceAll(s.siteName, ".", "_"), hash[:6])

			vacancies = append(vacancies, &models.Vacancy{
				ID:           vID,
				Source:       s.siteName,
				ExternalID:   link,
				Title:        title,
				Company:      company,
				Salary:       salary,
				Location:     location,
				URL:          link,
				Requirements: query,
				Description:  descText,
				PublishedAt:  time.Now(),
				CreatedAt:    time.Now(),
			})
		}
	}

	return vacancies, nil
}

func (s *HHScraper) fetchAPI(ctx context.Context, query string) ([]*models.Vacancy, error) {
	apiHost := "api.hh.ru"
	var urlsToFetch []string

	if strings.Contains(s.siteName, "rabota.by") {
		// All Belarus vacancies (Area 16)
		urlsToFetch = append(urlsToFetch, fmt.Sprintf("https://%s/vacancies?text=%s&area=16&per_page=50", apiHost, url.QueryEscape(query)))
	} else {
		// 1. Remote vacancies across all countries and cities
		urlsToFetch = append(urlsToFetch, fmt.Sprintf("https://%s/vacancies?text=%s&schedule=remote&order_by=publication_time&per_page=50", apiHost, url.QueryEscape(query)))
		// 2. Fresh general vacancies matching query
		urlsToFetch = append(urlsToFetch, fmt.Sprintf("https://%s/vacancies?text=%s&order_by=publication_time&per_page=30", apiHost, url.QueryEscape(query)))
	}

	type hhResponseItem struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		AltURL   string `json:"alternate_url"`
		Employer struct {
			Name string `json:"name"`
		} `json:"employer"`
		Salary *struct {
			From     *int   `json:"from"`
			To       *int   `json:"to"`
			Currency string `json:"currency"`
		} `json:"salary"`
		Snippet struct {
			Requirement    string `json:"requirement"`
			Responsibility string `json:"responsibility"`
		} `json:"snippet"`
		Schedule *struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"schedule"`
		Area struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"area"`
	}

	var allItems []hhResponseItem
	seenItemIDs := make(map[string]bool)

	for _, apiURL := range urlsToFetch {
		req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "JobParserBot/2.0 (sobolnikita@github.com)")
		req.Header.Set("HH-User-Agent", "JobParserBot/2.0 (sobolnikita@github.com)")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7")

		resp, err := s.client.Do(req)
		if err != nil {
			continue
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			continue
		}

		var res struct {
			Items []hhResponseItem `json:"items"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&res); err == nil {
			for _, item := range res.Items {
				if !seenItemIDs[item.ID] {
					seenItemIDs[item.ID] = true
					allItems = append(allItems, item)
				}
			}
		}
		resp.Body.Close()
	}

	if len(allItems) == 0 {
		return nil, fmt.Errorf("no vacancies returned from HH API")
	}

	var vacancies []*models.Vacancy
	for _, item := range allItems {
		cleanTitle := strings.ReplaceAll(strings.ReplaceAll(item.Name, "<highlighttext>", ""), "</highlighttext>", "")
		cleanReq := strings.ReplaceAll(strings.ReplaceAll(item.Snippet.Requirement, "<highlighttext>", ""), "</highlighttext>", "")
		cleanResp := strings.ReplaceAll(strings.ReplaceAll(item.Snippet.Responsibility, "<highlighttext>", ""), "</highlighttext>", "")

		isRemote := (item.Schedule != nil && item.Schedule.ID == "remote") ||
			strings.Contains(strings.ToLower(cleanTitle), "удален") ||
			strings.Contains(strings.ToLower(cleanReq), "удален") ||
			strings.Contains(strings.ToLower(cleanResp), "удален")

		isBelarus := strings.Contains(s.siteName, "rabota.by") ||
			item.Area.ID == "16" ||
			strings.Contains(strings.ToLower(item.Area.Name), "беларусь") ||
			strings.Contains(strings.ToLower(item.Area.Name), "минск") ||
			strings.Contains(strings.ToLower(item.Area.Name), "брест") ||
			strings.Contains(strings.ToLower(item.Area.Name), "гомель") ||
			strings.Contains(strings.ToLower(item.Area.Name), "гродно") ||
			strings.Contains(strings.ToLower(item.Area.Name), "витебск") ||
			strings.Contains(strings.ToLower(item.Area.Name), "могилев")

		// Filtering rule: keep all Belarus vacancies OR remote vacancies from any region
		if !isBelarus && !isRemote {
			continue
		}

		salaryStr := ""
		if item.Salary != nil {
			if item.Salary.From != nil && item.Salary.To != nil {
				salaryStr = fmt.Sprintf("%d - %d %s", *item.Salary.From, *item.Salary.To, item.Salary.Currency)
			} else if item.Salary.From != nil {
				salaryStr = fmt.Sprintf("от %d %s", *item.Salary.From, item.Salary.Currency)
			} else if item.Salary.To != nil {
				salaryStr = fmt.Sprintf("до %d %s", *item.Salary.To, item.Salary.Currency)
			}
		}

		desc := cleanReq
		if cleanResp != "" {
			if desc != "" {
				desc += "\n" + cleanResp
			} else {
				desc = cleanResp
			}
		}

		link := item.AltURL
		if strings.Contains(s.siteName, "rabota.by") && strings.Contains(link, "hh.ru") {
			link = strings.Replace(link, "hh.ru", "rabota.by", 1)
		}

		locationStr := item.Area.Name
		if isRemote && !strings.Contains(strings.ToLower(locationStr), "удален") {
			if locationStr != "" {
				locationStr += " (Удаленно)"
			} else {
				locationStr = "Удаленно"
			}
		}

		hash := sha256.Sum256([]byte(link))
		vID := fmt.Sprintf("%s_%x", strings.ReplaceAll(s.siteName, ".", "_"), hash[:6])

		vacancies = append(vacancies, &models.Vacancy{
			ID:           vID,
			Source:       s.siteName,
			ExternalID:   link,
			Title:        cleanTitle,
			Company:      item.Employer.Name,
			Salary:       salaryStr,
			Location:     locationStr,
			URL:          link,
			Requirements: cleanReq,
			Description:  desc,
			PublishedAt:  time.Now(),
			CreatedAt:    time.Now(),
		})
	}

	return vacancies, nil
}

func (s *HHScraper) fetchBrowser(ctx context.Context, query string) ([]*models.Vacancy, error) {
	if s.chrome == nil {
		return nil, fmt.Errorf("chrome manager not available")
	}

	targetURL := s.startURL
	if strings.Contains(s.startURL, "text=") {
		if strings.Contains(s.siteName, "rabota.by") {
			targetURL = fmt.Sprintf("https://rabota.by/search/vacancy?text=%s&order_by=publication_time&items_on_page=50", url.QueryEscape(query))
		} else {
			targetURL = fmt.Sprintf("https://hh.ru/search/vacancy?text=%s&schedule=remote&order_by=publication_time&items_on_page=50", url.QueryEscape(query))
		}
	}

	var vacancies []*models.Vacancy
	scraperCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	err := s.chrome.WithPage(scraperCtx, func(page *rod.Page) error {
		if err := page.Navigate(targetURL); err != nil {
			return err
		}

		// Wait for initial page load
		_ = page.Timeout(12 * time.Second).WaitLoad()

		// Dismiss region popup / cookie banner if present
		if confirmBtn, err := page.Element("[data-qa='region-clarification-confirm'], button[data-qa*='confirm']"); err == nil && confirmBtn != nil {
			_ = confirmBtn.Click(proto.InputMouseButtonLeft, 1)
			time.Sleep(500 * time.Millisecond)
		}

		// Wait up to 20 seconds for vacancy elements to appear (allows DDoS-Guard redirect / React hydration to complete)
		_, _ = page.Timeout(20 * time.Second).Element("a[data-qa*='vacancy-title'], [data-qa*='serp-item__title'], a[href*='/vacancy/'], [data-qa*='vacancy-serp__vacancy']")

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
				log.Printf("[%s] No vacancy card elements found on page. Title: %s", s.siteName, page.MustInfo().Title)
				return nil
			}
		}

		seenURLs := make(map[string]bool)

		for _, card := range cards {
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

	return vacancies, err
}
