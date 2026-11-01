package scrapers

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"sync"
	"time"

	"jobparser/internal/browser"
	"jobparser/internal/models"
	"jobparser/internal/storage"
)

type JobSource interface {
	Name() string
	FetchJobs(ctx context.Context) ([]*models.Vacancy, error)
}

type QueryJobSource interface {
	JobSource
	FetchJobsForQuery(ctx context.Context, query string) ([]*models.Vacancy, error)
}

type Orchestrator struct {
	sources []JobSource
	storage *storage.Storage
}

func NewOrchestrator(chrome *browser.ChromeManager, st *storage.Storage, customChannels []string) *Orchestrator {
	var sources []JobSource

	// 1. Habr Career (HTTP / CDP)
	sources = append(sources, NewHabrScraper(chrome))

	// 2. Telegram Channels
	sources = append(sources, NewTelegramScraper(st, customChannels))

	// 3. HireHi
	sources = append(sources, NewHireHiScraper(chrome))

	// 4. HH.ru
	sources = append(sources, NewHHScraper(chrome, "hh.ru", "https://hh.ru/search/vacancy?text=golang&order_by=publication_time"))

	// 5. Rabota.by
	sources = append(sources, NewHHScraper(chrome, "rabota.by", "https://rabota.by/search/vacancy?text=golang&order_by=publication_time"))

	return &Orchestrator{
		sources: sources,
		storage: st,
	}
}

type ScraperProgressUpdate struct {
	SourceName string
	IsDone     bool
	Count      int
	Err        error
}

// GetSourceNames returns names of registered scraper sources
func (o *Orchestrator) GetSourceNames() []string {
	var names []string
	for _, s := range o.sources {
		names = append(names, s.Name())
	}
	return names
}

// FetchAll runs all scrapers concurrently for default query and saves newly found vacancies
func (o *Orchestrator) FetchAll(ctx context.Context) ([]*models.Vacancy, error) {
	return o.FetchAllForQuery(ctx, "golang")
}

// FetchAllForQuery runs scrapers concurrently for a specific query stack and saves newly found vacancies
func (o *Orchestrator) FetchAllForQuery(ctx context.Context, query string) ([]*models.Vacancy, error) {
	return o.FetchAllForQueryWithProgress(ctx, query, nil)
}

// FetchAllForQueryWithProgress runs scrapers concurrently and reports progress per source
func (o *Orchestrator) FetchAllForQueryWithProgress(ctx context.Context, query string, onProgress func(u ScraperProgressUpdate)) ([]*models.Vacancy, error) {
	if query == "" {
		query = "golang"
	}
	log.Printf("[Orchestrator] Starting job sweep for query '%s' across %d sources...", query, len(o.sources))

	var wg sync.WaitGroup
	resultsCh := make(chan []*models.Vacancy, len(o.sources))

	for _, src := range o.sources {
		wg.Add(1)
		go func(s JobSource) {
			defer wg.Done()
			log.Printf("[Scraper] Running %s for '%s'...", s.Name(), query)
			var vacancies []*models.Vacancy
			var err error
			if qs, ok := s.(QueryJobSource); ok {
				vacancies, err = qs.FetchJobsForQuery(ctx, query)
			} else {
				vacancies, err = s.FetchJobs(ctx)
			}

			if onProgress != nil {
				onProgress(ScraperProgressUpdate{
					SourceName: s.Name(),
					IsDone:     true,
					Count:      len(vacancies),
					Err:        err,
				})
			}

			if err != nil {
				log.Printf("[Scraper ERROR] %s failed: %v", s.Name(), err)
				return
			}
			log.Printf("[Scraper] %s fetched %d vacancies for '%s'", s.Name(), len(vacancies), query)
			resultsCh <- vacancies
		}(src)
	}

	wg.Wait()
	close(resultsCh)

	var newlyFound []*models.Vacancy
	for list := range resultsCh {
		for _, v := range list {
			// Generate deterministic ID if missing
			if v.ID == "" {
				hash := sha256.Sum256([]byte(v.Source + ":" + v.URL))
				v.ID = fmt.Sprintf("%x", hash[:8])
			}
			if v.CreatedAt.IsZero() {
				v.CreatedAt = time.Now()
			}

			exists, err := o.storage.VacancyExists(v.ID)
			if err != nil {
				log.Printf("[Storage ERROR] Check exists %s: %v", v.ID, err)
				continue
			}
			if !exists {
				if err := o.storage.SaveVacancy(v); err != nil {
					log.Printf("[Storage ERROR] Save vacancy %s: %v", v.ID, err)
					continue
				}
				newlyFound = append(newlyFound, v)
			}
		}
	}

	log.Printf("[Orchestrator] Job sweep finished for '%s'! %d new vacancies found.", query, len(newlyFound))
	return newlyFound, nil
}
