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
		targetURL = fmt.Sprintf("https://%s/search/vacancy?text=%s&order_by=publication_time", s.siteName, url.QueryEscape(query))
	}

	var vacancies []*models.Vacancy

	scraperCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	err := s.chrome.WithPage(scraperCtx, func(page *rod.Page) error {
		_ = page.SetUserAgent(&proto.NetworkSetUserAgentOverride{
			UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
		})

		if err := page.Navigate(targetURL); err != nil {
			return err
		}

		_ = page.Timeout(10 * time.Second).WaitLoad()
		time.Sleep(2 * time.Second)

		cards, err := page.Elements("[data-qa='vacancy-serp__vacancy'], [data-qa='vacancy-serp__vacancy_paginated'], .serp-item, [class*='vacancy-card']")
		if err != nil || len(cards) == 0 {
			log.Printf("[%s] No vacancy card elements found on page", s.siteName)
			return nil
		}

		for _, card := range cards {
			titleEl, err := card.Element("[data-qa='serp-item__title'], a[href*='/vacancy/'], [data-qa='vacancy-serp__vacancy-title']")
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
				if strings.Contains(s.siteName, "rabota.by") {
					fullURL = "https://rabota.by" + fullURL
				} else {
					fullURL = "https://hh.ru" + fullURL
				}
			}

			if idx := strings.Index(fullURL, "?"); idx != -1 {
				fullURL = fullURL[:idx]
			}

			var company string
			if compEl, err := card.Element("[data-qa='vacancy-serp__vacancy-employer'], [data-qa='vacancy-serp__vacancy-company']"); err == nil && compEl != nil {
				company, _ = compEl.Text()
			}

			var salary string
			if salEl, err := card.Element("[data-qa='vacancy-serp__vacancy-compensation'], [data-qa='vacancy-compensation']"); err == nil && salEl != nil {
				salary, _ = salEl.Text()
			}

			var reqs string
			if reqEl, err := card.Element("[data-qa='vacancy-serp__vacancy_snippet_requirement']"); err == nil && reqEl != nil {
				reqs, _ = reqEl.Text()
			}

			var resp string
			if respEl, err := card.Element("[data-qa='vacancy-serp__vacancy_snippet_responsibility']"); err == nil && respEl != nil {
				resp, _ = respEl.Text()
			}

			var location string
			if locEl, err := card.Element("[data-qa='vacancy-serp__vacancy-address']"); err == nil && locEl != nil {
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
