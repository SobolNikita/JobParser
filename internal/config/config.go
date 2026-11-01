package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	TelegramBotToken string   `json:"telegram_bot_token"`
	TelegramChatID   int64    `json:"telegram_chat_id"`
	GeminiAPIKey     string   `json:"gemini_api_key"`
	GeminiModel      string   `json:"gemini_model"`
	ChromeWsURL      string   `json:"chrome_ws_url"`
	HeadlessBrowser  bool     `json:"headless_browser"`
	ChromeProfileDir string   `json:"chrome_profile_dir"`
	PollIntervalMin  int      `json:"poll_interval_min"`
	DBPath           string   `json:"db_path"`
	TargetKeywords   []string `json:"target_keywords"`
	TelegramChannels []string `json:"telegram_channels"`
	APIPort          int      `json:"api_port"`
	BotAuthPassword  string   `json:"bot_auth_password"`
	WebAppURL        string   `json:"web_app_url"`
}

func DefaultConfig() *Config {
	return &Config{
		GeminiModel:      "gemini-2.0-flash",
		HeadlessBrowser:  true,
		ChromeProfileDir: "./data/chrome_profile",
		PollIntervalMin:  15,
		DBPath:           "jobparser.db",
		APIPort:          8080,
		WebAppURL:        "https://sobolnikita.github.io/JobParser",
		TargetKeywords:   []string{"golang", "go developer", "go разработчик"},
		TelegramChannels: []string{
			"jobgolang",
			"golang_job",
			"godevjob",
		},
	}
}

// AutoDetectChromeWS reads DevToolsActivePort from Chrome user data directory (Windows or Linux)
func AutoDetectChromeWS() (string, error) {
	var candidates []string

	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		candidates = append(candidates, filepath.Join(localAppData, "Google", "Chrome", "User Data", "DevToolsActivePort"))
	}

	if home := os.Getenv("HOME"); home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".config", "google-chrome", "DevToolsActivePort"),
			filepath.Join(home, ".config", "chromium", "DevToolsActivePort"),
		)
	}

	candidates = append(candidates, filepath.Join(".", "data", "chrome_profile", "DevToolsActivePort"))

	for _, portFile := range candidates {
		data, err := os.ReadFile(portFile)
		if err == nil {
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) >= 2 {
				port := strings.TrimSpace(lines[0])
				path := strings.TrimSpace(lines[1])
				return fmt.Sprintf("ws://127.0.0.1:%s%s", port, path), nil
			}
		}
	}

	return "", fmt.Errorf("DevToolsActivePort not found")
}

func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()

	// Try reading file if exists
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("failed to parse config JSON: %w", err)
		}
	}

	// Environment variable overrides
	if val := os.Getenv("TELEGRAM_BOT_TOKEN"); val != "" {
		cfg.TelegramBotToken = val
	}
	if val := os.Getenv("GEMINI_API_KEY"); val != "" {
		cfg.GeminiAPIKey = val
	}
	if val := os.Getenv("GEMINI_MODEL"); val != "" {
		cfg.GeminiModel = val
	}
	if val := os.Getenv("CHROME_WS_URL"); val != "" {
		cfg.ChromeWsURL = val
	}
	if val := os.Getenv("WEB_APP_URL"); val != "" {
		cfg.WebAppURL = val
	}
	if val := os.Getenv("BOT_AUTH_PASSWORD"); val != "" {
		cfg.BotAuthPassword = val
	}
	if cfg.APIPort == 0 {
		cfg.APIPort = 8080
	}

	// Try auto-detecting Chrome WS if not set
	if cfg.ChromeWsURL == "" {
		if detected, err := AutoDetectChromeWS(); err == nil {
			cfg.ChromeWsURL = detected
		}
	}

	return cfg, nil
}

func (c *Config) SaveConfig(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
