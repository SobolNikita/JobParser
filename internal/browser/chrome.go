package browser

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"jobparser/internal/config"
	"jobparser/internal/models"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

type ChromeManager struct {
	wsURL      string
	profileDir string
	headless   bool
	browser    *rod.Browser
	launcher   *launcher.Launcher
	mu         sync.Mutex
	stopCh     chan struct{}
}

func NewChromeManager(wsURL, profileDir string, headless bool) *ChromeManager {
	if profileDir == "" {
		profileDir = filepath.Join(".", "data", "chrome_profile")
	}
	return &ChromeManager{
		wsURL:      wsURL,
		profileDir: profileDir,
		headless:   headless,
		stopCh:     make(chan struct{}),
	}
}

// Start establishes connection to existing Chrome or launches managed Chrome
func (m *ChromeManager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.browser != nil {
		return nil
	}

	// 1. First, check if an existing Chrome CDP is running
	url := m.wsURL
	if url == "" {
		if detected, err := config.AutoDetectChromeWS(); err == nil {
			url = detected
		}
	}

	if url != "" {
		log.Printf("[Chrome] Attempting to attach to existing Chrome at: %s", url)
		b := rod.New().ControlURL(url)
		if err := b.Connect(); err == nil {
			m.browser = b
			log.Println("[Chrome] Attached successfully to existing running Chrome session!")
			go m.keepAlive()
			return nil
		}
		log.Printf("[Chrome] Existing Chrome not responding at %s. Falling back to autonomous managed Chrome.", url)
	}

	// 2. Launch autonomous managed Chrome with persistent profile
	_ = os.MkdirAll(m.profileDir, 0755)

	chromeBin, found := launcher.LookPath()
	l := launcher.New().
		UserDataDir(m.profileDir).
		Headless(m.headless).
		NoSandbox(true).
		Set("disable-dev-shm-usage").
		Set("disable-gpu")

	if found {
		log.Printf("[Chrome] Found system Chrome at: %s", chromeBin)
		l = l.Bin(chromeBin)
	}

	controlURL, err := l.Launch()
	if err != nil {
		return fmt.Errorf("failed to launch autonomous Chrome: %w", err)
	}

	m.launcher = l
	b := rod.New().ControlURL(controlURL)
	if err := b.Connect(); err != nil {
		return fmt.Errorf("failed to connect to managed Chrome: %w", err)
	}

	m.browser = b
	log.Printf("[Chrome] Managed autonomous Chrome started successfully (headless=%v, profile=%s)", m.headless, m.profileDir)

	go m.keepAlive()
	return nil
}

func (m *ChromeManager) keepAlive() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.mu.Lock()
			if m.browser != nil {
				_, err := m.browser.Version()
				if err != nil {
					log.Printf("[Chrome] Browser connection lost (%v). Reconnecting...", err)
					_ = m.browser.Close()
					m.browser = nil
				}
			}
			m.mu.Unlock()
		}
	}
}

// Stop cleanly terminates browser and launcher
func (m *ChromeManager) Stop() {
	close(m.stopCh)
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.browser != nil {
		_ = m.browser.Close()
		m.browser = nil
	}
	if m.launcher != nil {
		m.launcher.Kill()
		m.launcher = nil
	}
	log.Println("[Chrome] Browser manager stopped cleanly.")
}

func (m *ChromeManager) ensureConnected() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.browser != nil {
		return nil
	}

	m.mu.Unlock()
	err := m.Start()
	m.mu.Lock()
	return err
}

// WithPage executes callback in a temporary tab and closes ONLY that tab
func (m *ChromeManager) WithPage(ctx context.Context, fn func(page *rod.Page) error) error {
	if err := m.ensureConnected(); err != nil {
		return err
	}

	m.mu.Lock()
	b := m.browser
	m.mu.Unlock()

	if b == nil {
		return fmt.Errorf("chrome browser instance not available")
	}

	page, err := b.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return fmt.Errorf("failed to create browser tab: %w", err)
	}
	defer func() {
		_ = page.Close()
	}()

	pageWithCtx := page.Context(ctx)
	return fn(pageWithCtx)
}

// ApplyResult holds the outcome of applying to a job
type ApplyResult struct {
	Success     bool
	Message     string
	AlreadyDone bool
}

// ApplyToVacancy executes browser automation to apply with cover letter
func (m *ChromeManager) ApplyToVacancy(ctx context.Context, v *models.Vacancy, coverLetter string, resumePDFPath ...string) (*ApplyResult, error) {
	var result ApplyResult

	pdfPath := ""
	if len(resumePDFPath) > 0 {
		pdfPath = resumePDFPath[0]
	}

	err := m.WithPage(ctx, func(page *rod.Page) error {
		log.Printf("[Apply] Navigating to vacancy URL: %s", v.URL)
		if err := page.Navigate(v.URL); err != nil {
			return fmt.Errorf("failed to navigate: %w", err)
		}

		_ = page.WaitLoad()
		time.Sleep(2 * time.Second)

		// Upload PDF resume if file input is available on the application page
		if pdfPath != "" {
			if _, statErr := os.Stat(pdfPath); statErr == nil {
				if fileInputs, err := page.Elements("input[type='file']"); err == nil && len(fileInputs) > 0 {
					log.Printf("[Apply] Attaching PDF resume file: %s", pdfPath)
					_ = fileInputs[0].SetFiles([]string{pdfPath})
					time.Sleep(1 * time.Second)
				}
			}
		}

		switch {
		case strings.Contains(v.URL, "hh.ru") || strings.Contains(v.URL, "rabota.by"):
			return m.applyHH(page, coverLetter, &result)
		case strings.Contains(v.URL, "habr.com"):
			return m.applyHabr(page, coverLetter, &result)
		case strings.Contains(v.URL, "hirehi.ru"):
			return m.applyHireHi(page, coverLetter, &result)
		default:
			return m.applyGeneric(page, coverLetter, &result)
		}
	})

	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (m *ChromeManager) applyHH(page *rod.Page, coverLetter string, res *ApplyResult) error {
	html, _ := page.HTML()
	if strings.Contains(html, "Вы откликнулись") || strings.Contains(html, "Вы уже откликнулись") {
		res.Success = true
		res.AlreadyDone = true
		res.Message = "Вы уже откликались на эту вакансию ранее."
		return nil
	}

	selectors := []string{
		"[data-qa='vacancy-response-link-top']",
		"[data-qa='vacancy-response-link-bottom']",
		"a.vacancy-response-button",
		"button.vacancy-response-button",
	}

	var btn *rod.Element
	for _, sel := range selectors {
		el, e := page.Element(sel)
		if e == nil && el != nil {
			btn = el
			break
		}
	}

	if btn == nil {
		els, _ := page.Elements("a, button")
		for _, el := range els {
			txt, _ := el.Text()
			if strings.Contains(txt, "Откликнуться") {
				btn = el
				break
			}
		}
	}

	if btn == nil {
		res.Success = false
		res.Message = "Кнопка 'Откликнуться' не найдена (возможно, требуется авторизация в браузере или вакансия закрыта)."
		return nil
	}

	log.Println("[Apply HH] Clicking apply button...")
	if err := btn.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return fmt.Errorf("failed to click apply button: %w", err)
	}

	time.Sleep(2 * time.Second)

	letterBtn, err := page.Element("[data-qa='vacancy-response-letter-toggle'], [data-qa='vacancy-response-letter-show']")
	if err == nil && letterBtn != nil {
		_ = letterBtn.Click(proto.InputMouseButtonLeft, 1)
		time.Sleep(1 * time.Second)
	}

	textarea, err := page.Element("textarea[data-qa='vacancy-response-popup-form-letter-input'], textarea[data-qa='vacancy-response-letter-text'], textarea")
	if err == nil && textarea != nil && coverLetter != "" {
		log.Println("[Apply HH] Typing cover letter...")
		_ = textarea.Input(coverLetter)
		time.Sleep(1 * time.Second)
	}

	submitBtn, err := page.Element("[data-qa='vacancy-response-submit-popup'], button[type='submit']")
	if err == nil && submitBtn != nil {
		log.Println("[Apply HH] Submitting application modal...")
		_ = submitBtn.Click(proto.InputMouseButtonLeft, 1)
		time.Sleep(2 * time.Second)
	}

	res.Success = true
	res.Message = "Отклик на hh.ru успешно отправлен!"
	return nil
}

func (m *ChromeManager) applyHabr(page *rod.Page, coverLetter string, res *ApplyResult) error {
	html, _ := page.HTML()
	if strings.Contains(html, "Вы откликнулись") {
		res.Success = true
		res.AlreadyDone = true
		res.Message = "Вы уже откликались на эту вакансию на Хабр Карьере."
		return nil
	}

	var btn *rod.Element
	els, _ := page.Elements("button, a")
	for _, el := range els {
		txt, _ := el.Text()
		if strings.TrimSpace(txt) == "Откликнуться" || strings.Contains(txt, "Откликнуться на вакансию") {
			btn = el
			break
		}
	}

	if btn == nil {
		res.Success = false
		res.Message = "Кнопка отклика не найдена на Хабр Карьере."
		return nil
	}

	log.Println("[Apply Habr] Clicking apply button...")
	_ = btn.Click(proto.InputMouseButtonLeft, 1)
	time.Sleep(2 * time.Second)

	textarea, err := page.Element("textarea")
	if err == nil && textarea != nil && coverLetter != "" {
		_ = textarea.Input(coverLetter)
		time.Sleep(1 * time.Second)
	}

	submits, _ := page.Elements("button[type='submit'], .button-comp--appearance-primary")
	for _, s := range submits {
		t, _ := s.Text()
		if strings.Contains(t, "Отправить") || strings.Contains(t, "Откликнуться") {
			_ = s.Click(proto.InputMouseButtonLeft, 1)
			break
		}
	}

	time.Sleep(2 * time.Second)
	res.Success = true
	res.Message = "Отклик на Хабр Карьере отправлен!"
	return nil
}

func (m *ChromeManager) applyHireHi(page *rod.Page, coverLetter string, res *ApplyResult) error {
	var btn *rod.Element
	els, _ := page.Elements("button, a")
	for _, el := range els {
		txt, _ := el.Text()
		if strings.Contains(txt, "Откликнуться") {
			btn = el
			break
		}
	}

	if btn == nil {
		res.Success = false
		res.Message = "Кнопка отклика на HireHi не найдена."
		return nil
	}

	_ = btn.Click(proto.InputMouseButtonLeft, 1)
	time.Sleep(2 * time.Second)

	textarea, err := page.Element("textarea")
	if err == nil && textarea != nil && coverLetter != "" {
		_ = textarea.Input(coverLetter)
		time.Sleep(1 * time.Second)
	}

	res.Success = true
	res.Message = "Отклик на HireHi обработан."
	return nil
}

func (m *ChromeManager) applyGeneric(page *rod.Page, coverLetter string, res *ApplyResult) error {
	var btn *rod.Element
	els, _ := page.Elements("button, a")
	for _, el := range els {
		txt, _ := el.Text()
		tLower := strings.ToLower(txt)
		if strings.Contains(tLower, "откликнуться") || strings.Contains(tLower, "apply") {
			btn = el
			break
		}
	}

	if btn == nil {
		res.Success = false
		res.Message = "Кнопка 'Откликнуться' / 'Apply' не найдена автоматически. Пожалуйста, откройте ссылку вручную."
		return nil
	}

	_ = btn.Click(proto.InputMouseButtonLeft, 1)
	time.Sleep(2 * time.Second)

	res.Success = true
	res.Message = "Кнопка отклика нажата в браузере."
	return nil
}
