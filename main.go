package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"jobparser/internal/ai"
	"jobparser/internal/api"
	"jobparser/internal/bot"
	"jobparser/internal/browser"
	"jobparser/internal/config"
	"jobparser/internal/matcher"
	"jobparser/internal/scrapers"
	"jobparser/internal/storage"
)

func main() {
	configPath := flag.String("config", "config.json", "path to configuration file")
	flag.Parse()

	log.Println("==================================================")
	log.Println("     Golang Job Hunter Bot & Auto-Apply AI        ")
	log.Println("==================================================")

	// 1. Load Configuration
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("[Config ERROR] %v", err)
	}

	// If config file doesn't exist yet, save the default template
	if _, err := os.Stat(*configPath); os.IsNotExist(err) {
		_ = cfg.SaveConfig(*configPath)
		log.Printf("[Config] Created template config file: %s", *configPath)
	}

	if cfg.TelegramBotToken == "" {
		fmt.Println("\n⚠️  ВНИМАНИЕ: TELEGRAM_BOT_TOKEN не указан!")
		fmt.Printf("Пожалуйста, укажите токен вашего бота в файле '%s' или в переменной окружения TELEGRAM_BOT_TOKEN.\n", *configPath)
		fmt.Println("Пример в config.json:")
		fmt.Println(`  "telegram_bot_token": "123456789:ABCdefGHIjklmnoPQRstuvwxYZ"`)
		fmt.Println("\nВы можете получить токен бесплатно у @BotFather в Telegram.")
	}

	if cfg.GeminiAPIKey == "" {
		fmt.Println("\n⚠️  ВНИМАНИЕ: GEMINI_API_KEY не указан!")
		fmt.Printf("Пожалуйста, укажите API-ключ Gemini в файле '%s' или в переменной окружения GEMINI_API_KEY.\n", *configPath)
		fmt.Println("Бесплатный ключ можно получить за 1 минуту в Google AI Studio: https://aistudio.google.com/app/apikey\n")
	}

	// 2. Initialize Database Storage
	st, err := storage.NewStorage(cfg.DBPath)
	if err != nil {
		log.Fatalf("[Storage ERROR] %v", err)
	}
	defer st.Close()
	log.Printf("[Storage] SQLite database ready at %s", cfg.DBPath)

	// 3. Initialize Chrome Manager (Autonomous or Attached)
	chrome := browser.NewChromeManager(cfg.ChromeWsURL, cfg.ChromeProfileDir, cfg.HeadlessBrowser)
	if err := chrome.Start(); err != nil {
		log.Printf("[Chrome WARNING] Could not start browser manager immediately: %v", err)
	} else {
		log.Println("[Chrome] Браузерный движок успешно инициализирован и готов к работе!")
	}
	defer chrome.Stop()

	// 4. Initialize Gemini AI Client
	aiClient := ai.NewGeminiClient(cfg.GeminiAPIKey, cfg.GeminiModel)
	log.Printf("[AI] Gemini client initialized (configured: %s, resolved: %s)", cfg.GeminiModel, ai.NormalizeModel(cfg.GeminiModel))


	// 5. Initialize Scrapers Orchestrator
	orch := scrapers.NewOrchestrator(chrome, st, cfg.TelegramChannels)

	// 6. Initialize Matcher
	matchEngine := matcher.NewMatcher(st, aiClient, chrome)

	// 7. Initialize REST API Server (Port 8080)
	apiServer := api.NewServer(cfg, st, aiClient, orch, matchEngine, chrome)
	go func() {
		if err := apiServer.Start(); err != nil {
			log.Printf("[API ERROR] Server exited: %v", err)
		}
	}()

	// 8. Initialize Telegram Bot
	if cfg.TelegramBotToken == "" {
		log.Println("[Bot] Ожидание настройки токена перед запуском Telegram бота...")
		select {}
	}

	tgBot, err := bot.NewBot(cfg, st, aiClient, chrome, orch, matchEngine)
	if err != nil {
		log.Fatalf("[Bot ERROR] %v", err)
	}

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Println("\n[Shutdown] Shutting down gracefully...")
		tgBot.Stop()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = apiServer.Stop(ctx)
		cancel()
		chrome.Stop()
		st.Close()
		os.Exit(0)
	}()

	tgBot.Start()
}
