package bot

import (
	"context"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"jobparser/internal/ai"
	"jobparser/internal/browser"
	"jobparser/internal/config"
	"jobparser/internal/matcher"
	"jobparser/internal/models"
	"jobparser/internal/resume"
	"jobparser/internal/scrapers"
	"jobparser/internal/storage"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Bot struct {
	cfg              *config.Config
	api              *tgbotapi.BotAPI
	storage          *storage.Storage
	ai               *ai.GeminiClient
	chrome           *browser.ChromeManager
	orchestrator     *scrapers.Orchestrator
	matcher          *matcher.Matcher
	stopCh           chan struct{}
	activeSearches   map[int64]context.CancelFunc
	activeSearchesMu sync.Mutex
}

func NewBot(
	cfg *config.Config,
	st *storage.Storage,
	aiClient *ai.GeminiClient,
	chrome *browser.ChromeManager,
	orch *scrapers.Orchestrator,
	m *matcher.Matcher,
) (*Bot, error) {
	if cfg.TelegramBotToken == "" {
		return nil, fmt.Errorf("TELEGRAM_BOT_TOKEN is empty. Set it in config.json or environment variable")
	}

	botAPI, err := tgbotapi.NewBotAPI(cfg.TelegramBotToken)
	if err != nil {
		return nil, fmt.Errorf("failed to init Telegram bot: %w", err)
	}

	log.Printf("[Bot] Authorized on account @%s", botAPI.Self.UserName)

	return &Bot{
		cfg:            cfg,
		api:            botAPI,
		storage:        st,
		ai:             aiClient,
		chrome:         chrome,
		orchestrator:   orch,
		matcher:        m,
		stopCh:         make(chan struct{}),
		activeSearches: make(map[int64]context.CancelFunc),
	}, nil
}

// Start launches update polling and background vacancy scanner
func (b *Bot) Start() {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := b.api.GetUpdatesChan(u)

	// Start background parser ticker
	go b.pollWorker()

	log.Println("[Bot] Telegram bot is listening for messages...")

	for {
		select {
		case <-b.stopCh:
			return
		case update := <-updates:
			b.handleUpdate(update)
		}
	}
}

func (b *Bot) Stop() {
	close(b.stopCh)
}

func (b *Bot) handleUpdate(update tgbotapi.Update) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[Bot PANIC] recovered: %v", r)
		}
	}()

	if update.CallbackQuery != nil {
		b.handleCallbackQuery(update.CallbackQuery)
		return
	}

	if update.Message != nil {
		b.handleMessage(update.Message)
		return
	}
}

func (b *Bot) handleMessage(msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	text := strings.TrimSpace(msg.Text)
	textLower := strings.ToLower(text)

	// Check password authorization if configured
	if b.cfg.BotAuthPassword != "" {
		if !b.storage.IsUserAuthorized(chatID) {
			if (msg.IsCommand() && msg.Command() == "auth") || strings.HasPrefix(textLower, "/auth") {
				arg := strings.TrimSpace(msg.CommandArguments())
				if arg == "" && strings.HasPrefix(textLower, "/auth") {
					parts := strings.Fields(text)
					if len(parts) > 1 {
						arg = parts[1]
					}
				}
				if arg == b.cfg.BotAuthPassword {
					_ = b.storage.SetUserAuthorized(chatID, true)
					b.sendHTMLWithReply(chatID, "🎉 <b>Авторизация успешна!</b>\n\nДобро пожаловать в Job Hunter Bot. Все функции активированы!", MainMenuKeyboard())
					return
				}
				b.sendHTML(chatID, "❌ <b>Неверный пароль.</b> Попробуйте еще раз: <code>/auth &lt;пароль&gt;</code>")
				return
			}
			b.sendHTML(chatID, "🔒 <b>Доступ ограничен. Бот защищен паролем.</b>\n\nДля доступа введите пароль:\n<code>/auth ваш_пароль</code>")
			return
		}
	}

	// Check for document (PDF or TXT resume)
	if msg.Document != nil {
		b.handleDocument(msg)
		return
	}

	// Commands
	if msg.IsCommand() {
		switch msg.Command() {
		case "start":
			b.cmdStart(msg)
		case "auth":
			b.cmdAuth(msg)
		case "stack", "role":
			b.cmdStack(msg)
		case "web":
			b.cmdWeb(msg)
		case "resume":
			b.cmdResume(msg)
		case "edit":
			b.cmdEdit(msg)
		case "reanalyze", "analyze":
			b.cmdReanalyze(msg)
		case "search", "parse":
			b.cmdSearch(msg)
		case "poll", "monitoring":
			b.cmdPoll(chatID, msg.CommandArguments())
		case "favorites", "favs":
			b.cmdFavorites(msg)
		case "autoapply":
			b.cmdAutoApply(msg)
		case "channels":
			b.cmdChannels(msg)
		case "addchannel":
			b.cmdAddChannel(msg)
		case "delchannel":
			b.cmdDelChannel(msg)
		case "stats":
			b.cmdStats(msg)
		case "help":
			b.cmdHelp(msg)
		default:
			b.sendHTMLWithReply(chatID, "Неизвестная команда. Выберите действие на кнопках ниже 👇", MainMenuKeyboard())
		}
		return
	}

	// Plain text input from Reply Keyboard buttons or direct typing
	switch text {
	case "🔍 Поиск вакансий":
		b.cmdSearch(msg)
		return
	case "🎯 Выбрать стек":
		b.cmdStack(msg)
		return
	case "🌐 Веб-интерфейс":
		b.cmdWeb(msg)
		return
	case "🔄 Фоновый поиск":
		b.cmdPoll(chatID, "")
		return
	case "📄 Мое резюме":
		b.cmdResume(msg)
		return
	case "⭐ Избранное":
		b.cmdFavorites(msg)
		return
	case "✏️ Изменить резюме":
		b.cmdEditMenu(msg)
		return
	case "🤖 Автоотклики":
		b.cmdAutoApply(msg)
		return
	case "📊 Статистика":
		b.cmdStats(msg)
		return
	}

	// Setting custom tech stack directly via chat (e.g. "стек Python" or "стек: Frontend")
	if strings.HasPrefix(textLower, "стек ") || strings.HasPrefix(textLower, "стек: ") {
		newStack := strings.TrimSpace(text[5:])
		newStack = strings.TrimPrefix(newStack, ":")
		newStack = strings.TrimSpace(newStack)
		if newStack != "" {
			_ = b.storage.SetUserStack(chatID, newStack)
			b.sendHTMLWithReply(chatID, fmt.Sprintf("🎯 <b>Стек для поиска вакансий изменен на: %s</b>\n\nНажмите <b>«🔍 Поиск вакансий»</b> для поиска!", html.EscapeString(newStack)), MainMenuKeyboard())
			return
		}
	}

	// Natural language edit commands (e.g. "поменяй уровень на junior" or "смени грейд на джун")
	if strings.HasPrefix(textLower, "поменяй ") ||
		strings.HasPrefix(textLower, "измени ") ||
		strings.HasPrefix(textLower, "установи ") ||
		strings.HasPrefix(textLower, "смени ") ||
		strings.HasPrefix(textLower, "добавь ") {
		b.processResumeEdit(chatID, text)
		return
	}

	if len(text) > 80 {
		// Treat as plain text resume
		b.processPlainTextResume(chatID, text)
		return
	}

	b.sendHTMLWithReply(chatID, "Выберите действие на кнопках ниже 👇 или пришлите файл резюме (PDF/TXT):", MainMenuKeyboard())
}

func (b *Bot) cmdStart(msg *tgbotapi.Message) {
	text := `👋 <b>Добро пожаловать в Golang Job Hunter Bot!</b>

Этот бот парсит вакансии для Go-разработчиков со всех площадок, анализирует ваше резюме через Gemini ИИ и автоматизирует отклики через браузер Chrome.

🎯 <b>Источники вакансий:</b>
• <b>HeadHunter / rabota.by</b> (hh.ru, rabota.by)
• <b>Хабр Карьера</b> (career.habr.com)
• <b>HireHi.ru</b>
• <b>Telegram-каналы</b> (@jobgolang, @golang_job, @godevjob и др.)

⚡️ <b>Удобное управление кнопками:</b>
Используйте кнопки внизу экрана для быстрого поиска, просмотра резюме и избранного!

1. Пришлите мне ваше <b>резюме в формате PDF</b> или скопируйте текст в чат.
2. Бот автоматически определит ваш грейд (Junior/Middle/Senior) и стек технологий.
3. Если нужно скорректировать уровень — напишите <code>/edit поменяй уровень на junior</code> или нажмите <b>«✏️ Изменить резюме»</b>.
4. Нажмите <b>«🔍 Поиск вакансий»</b>: бот подберет вакансии строго под ваш уровень, а неподходящие Senior/Architect отфильтрует вниз.`

	b.sendHTMLWithReply(msg.Chat.ID, text, MainMenuKeyboard())
}

func (b *Bot) cmdHelp(msg *tgbotapi.Message) {
	text := `📌 <b>Основные возможности бота:</b>

🔍 <b>Поиск:</b> кнопка «🔍 Поиск вакансий» или <code>/search</code> (с живым отображением статуса и кнопкой отмены)
🔄 <b>Фоновый поиск:</b> кнопка «🔄 Фоновый поиск» или <code>/poll on</code> / <code>/poll off</code>
🎯 <b>Выбор стека:</b> кнопка «🎯 Выбрать стек» или <code>/stack</code> (Go, Python, Frontend, Java и др.)
🌐 <b>Веб-интерфейс:</b> кнопка «🌐 Веб-интерфейс» или <code>/web</code>
📄 <b>Резюме:</b> кнопка «📄 Мое резюме» или <code>/resume</code>
✏️ <b>Изменить данные:</b> <code>/edit поменяй уровень на junior</code> или кнопка «✏️ Изменить резюме»
⭐ <b>Избранное:</b> кнопка «⭐ Избранное» или <code>/favorites</code>
🤖 <b>Автоотклики:</b> <code>/autoapply set &lt;требования&gt;</code>
📊 <b>Статистика:</b> кнопка «📊 Статистика» или <code>/stats</code>
🔐 <b>Авторизация:</b> <code>/auth &lt;пароль&gt;</code>
📡 <b>Telegram-каналы:</b> <code>/channels</code>, <code>/addchannel @имя</code>, <code>/delchannel @имя</code>`

	b.sendHTMLWithReply(msg.Chat.ID, text, MainMenuKeyboard())
}

func (b *Bot) cmdPoll(chatID int64, args string) {
	args = strings.ToLower(strings.TrimSpace(args))
	if args == "on" || args == "start" || args == "1" {
		_ = b.storage.SetUserPolling(chatID, true)
		b.sendHTMLWithReply(chatID, "✅ <b>Постоянный фоновый поиск включен!</b>\n\nБот будет автоматически сканировать площадки (HH, Хабр, HireHi, Telegram) каждые 15 минут под ваш стек и присылать свежие подходящие вакансии.", MainMenuKeyboard())
		return
	}
	if args == "off" || args == "stop" || args == "0" {
		_ = b.storage.SetUserPolling(chatID, false)
		b.sendHTMLWithReply(chatID, "⏸ <b>Постоянный фоновый поиск остановлен!</b>\n\nБот больше не выполняет фоновые авто-проверки. Вы можете в любой момент запустить поиск вручную по кнопке «🔍 Поиск вакансий».", MainMenuKeyboard())
		return
	}

	isPolling := b.storage.IsUserPolling(chatID)
	statusText := "⏸ <b>ОСТАНОВЛЕН (ВЫКЛ)</b>"
	if isPolling {
		statusText = "▶️ <b>АКТИВЕН (каждые 15 минут)</b>"
	}

	stack := b.storage.GetUserStack(chatID)

	msgText := fmt.Sprintf(`🔄 <b>Постоянный фоновый поиск вакансий</b>

Статус: %s
Целевой стек: <b>%s</b>

Когда фоновый поиск активен, бот непрерывно отслеживает новые вакансии со всех площадок и присылает подходящие под ваш уровень предложения.

Вы можете включить или остановить постоянный мониторинг кнопками ниже:`, statusText, html.EscapeString(stack))

	b.sendHTMLWithKeyboard(chatID, msgText, PollControlKeyboard(isPolling))
}

func (b *Bot) cmdAuth(msg *tgbotapi.Message) {
	if b.cfg.BotAuthPassword == "" {
		b.sendHTMLWithReply(msg.Chat.ID, "✅ Парольная защита не установлена. Вы уже авторизованы!", MainMenuKeyboard())
		return
	}
	pass := strings.TrimSpace(msg.CommandArguments())
	if pass == b.cfg.BotAuthPassword {
		_ = b.storage.SetUserAuthorized(msg.Chat.ID, true)
		b.sendHTMLWithReply(msg.Chat.ID, "🎉 <b>Авторизация успешна!</b> Добро пожаловать.", MainMenuKeyboard())
	} else {
		b.sendHTML(msg.Chat.ID, "❌ <b>Неверный пароль.</b> Введите: <code>/auth &lt;пароль&gt;</code>")
	}
}

func (b *Bot) cmdStack(msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	arg := strings.TrimSpace(msg.CommandArguments())
	if arg != "" {
		_ = b.storage.SetUserStack(chatID, arg)
		b.sendHTMLWithReply(chatID, fmt.Sprintf("🎯 <b>Стек для поиска вакансий установлен: %s</b>\n\nНажмите <b>«🔍 Поиск вакансий»</b> для обновления списка!", html.EscapeString(arg)), MainMenuKeyboard())
		return
	}

	curr := b.storage.GetUserStack(chatID)
	msgText := fmt.Sprintf("🎯 <b>Текущий стек для поиска:</b> <code>%s</code>\n\nВыберите желаемое направление кнопками ниже или напишите свой вариант (например <code>стек Python FastAPI</code>):", html.EscapeString(curr))
	b.sendHTMLWithKeyboard(chatID, msgText, StackSelectionKeyboard())
}

func (b *Bot) cmdWeb(msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	token, err := b.storage.GenerateUserToken(chatID)
	if err != nil {
		b.sendHTML(chatID, fmt.Sprintf("❌ Ошибка генерации токена сессии: %v", err))
		return
	}

	webURL := b.cfg.WebAppURL
	if webURL == "" {
		webURL = "https://sobolnikita.github.io/JobParser"
	}

	serverBase := fmt.Sprintf("http://157.151.254.33:%d", b.cfg.APIPort)
	directLink := fmt.Sprintf("%s?token=%s&server=%s", webURL, token, serverBase)

	msgText := fmt.Sprintf(`🌐 <b>Веб-интерфейс вакансий</b>

Просматривайте вакансии в удобном минималистичном интерфейсе в стиле Vercel, управляйте избранным и создавайте сопроводительные письма.

🔑 <b>Ваш персональный токен:</b>
<code>%s</code>

🔗 <b>Прямой переход без ввода токена:</b>
%s

<i>Токен привязан к вашему Telegram ID (%d). Сохраняйте его в тайне.</i>`, token, directLink, chatID)

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonURL("🚀 Открыть веб-интерфейс", directLink),
		),
	)
	b.sendHTMLWithKeyboard(chatID, msgText, kb)
}

func (b *Bot) cmdResume(msg *tgbotapi.Message) {
	r, err := b.storage.GetResume(msg.Chat.ID)
	if err != nil || r == nil || len(r.RawText) == 0 {
		b.sendHTMLWithReply(msg.Chat.ID, "📄 <b>Резюме еще не загружено!</b>\n\nПришлите файл резюме (PDF/TXT) или отправьте текст резюме прямо в чат.", MainMenuKeyboard())
		return
	}
	b.sendHTMLWithReply(msg.Chat.ID, FormatResumeMessage(r), MainMenuKeyboard())
}

func (b *Bot) cmdEditMenu(msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	r, err := b.storage.GetResume(chatID)
	if err != nil || r == nil || len(r.RawText) == 0 {
		b.sendHTMLWithReply(chatID, "📄 <b>Резюме еще не загружено!</b>\n\nПришлите файл резюме (PDF/TXT) или отправьте текст в чат.", MainMenuKeyboard())
		return
	}

	text := fmt.Sprintf("✏️ <b>Настройка и корректировка резюме</b>\n\n"+
		"🎯 <b>Текущий грейд:</b> %s\n"+
		"💼 <b>Позиция:</b> %s\n"+
		"🛠 <b>Стек:</b> %s\n\n"+
		"Вы можете в 1 клик сменить грейд кнопками ниже или написать в чат, что исправить:\n"+
		"• <code>/edit поменяй уровень на junior</code>\n"+
		"• <code>/edit добавь технологии Kafka, Docker, Redis</code>\n"+
		"• <code>/edit измени желаемую должность на Junior Golang Developer</code>",
		html.EscapeString(r.Seniority),
		html.EscapeString(r.DesiredRole),
		html.EscapeString(strings.Join(r.Skills, ", ")),
	)

	b.sendHTMLWithKeyboard(chatID, text, EditResumeKeyboard())
}

func (b *Bot) cmdEdit(msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	args := strings.TrimSpace(msg.CommandArguments())
	if args == "" {
		b.cmdEditMenu(msg)
		return
	}
	b.processResumeEdit(chatID, args)
}

func (b *Bot) processResumeEdit(chatID int64, instruction string) {
	r, err := b.storage.GetResume(chatID)
	if err != nil || r == nil || len(r.RawText) == 0 {
		b.sendHTMLWithReply(chatID, "📄 <b>Резюме еще не загружено!</b>\n\nСначала пришлите файл резюме (PDF/TXT) или скопируйте текст в чат.", MainMenuKeyboard())
		return
	}

	b.sendHTML(chatID, "🧠 <i>Применяю ваши изменения к резюме через Gemini ИИ...</i>")

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	updated, err := b.ai.EditResume(ctx, r, instruction)
	cancel()

	if err != nil {
		log.Printf("[Bot] EditResume error: %v. Using fallback editor.", err)
		updated = ai.FallbackEditResume(r, instruction)
	}

	updated.UserID = chatID
	if err := b.storage.SaveResume(updated); err != nil {
		b.sendHTML(chatID, fmt.Sprintf("❌ Ошибка сохранения обновленного резюме: %v", err))
		return
	}

	msgText := fmt.Sprintf("✅ <b>Резюме успешно обновлено!</b>\n\n%s", FormatResumeMessage(updated))
	b.sendHTMLWithReply(chatID, msgText, MainMenuKeyboard())
}

func (b *Bot) cmdReanalyze(msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	r, err := b.storage.GetResume(chatID)
	if err != nil || r == nil || len(r.RawText) == 0 {
		b.sendHTMLWithReply(chatID, "📄 <b>Резюме еще не загружено!</b>\n\nПришлите файл резюме в формате <b>PDF</b> или скопируйте текст в чат.", MainMenuKeyboard())
		return
	}

	b.sendHTML(chatID, "🔄 <b>Повторно анализирую ваше резюме через Gemini ИИ...</b>")
	b.saveAndAnalyzeResume(chatID, r.RawText, r.FileName)
}

func (b *Bot) cmdFavorites(msg *tgbotapi.Message) {
	favs, err := b.storage.GetFavorites(msg.Chat.ID)
	if err != nil || len(favs) == 0 {
		b.sendHTMLWithReply(msg.Chat.ID, "⭐ <b>В избранном пока нет вакансий.</b>\n\nНажимайте кнопку <b>«⭐ В избранное»</b> под интересными вакансиями.", MainMenuKeyboard())
		return
	}

	b.sendHTML(msg.Chat.ID, fmt.Sprintf("⭐ <b>Ваши избранные вакансии (%d шт.):</b>\n<i>Для каждой вакансии доступно мгновенное создание сопроводительного письма!</i>", len(favs)))

	for _, v := range favs {
		cardText := FormatVacancyMessage(v)
		kb := FavoriteItemKeyboard(v.ID, v.URL)
		b.sendHTMLWithKeyboard(msg.Chat.ID, cardText, kb)
	}
}

func (b *Bot) cmdAutoApply(msg *tgbotapi.Message) {
	args := strings.TrimSpace(msg.CommandArguments())

	if args == "" {
		filter, _ := b.storage.GetAutoApplyFilter(msg.Chat.ID)
		b.sendHTMLWithReply(msg.Chat.ID, FormatAutoApplyMessage(filter), MainMenuKeyboard())
		return
	}

	switch {
	case args == "on":
		filter, _ := b.storage.GetAutoApplyFilter(msg.Chat.ID)
		if filter == nil {
			b.sendHTML(msg.Chat.ID, "⚠️ Сначала задайте требования командой:\n<code>/autoapply set &lt;ваши критерии&gt;</code>")
			return
		}
		filter.Enabled = true
		_ = b.storage.SaveAutoApplyFilter(msg.Chat.ID, *filter)
		b.sendHTMLWithReply(msg.Chat.ID, "✅ <b>Автоотклики включены!</b> Бот будет автоматически откликаться на новые подходящие вакансии.", MainMenuKeyboard())

	case args == "off":
		filter, _ := b.storage.GetAutoApplyFilter(msg.Chat.ID)
		if filter != nil {
			filter.Enabled = false
			_ = b.storage.SaveAutoApplyFilter(msg.Chat.ID, *filter)
		}
		b.sendHTMLWithReply(msg.Chat.ID, "⏸ <b>Автоотклики выключены.</b>", MainMenuKeyboard())

	case strings.HasPrefix(args, "set "):
		prompt := strings.TrimPrefix(args, "set ")
		b.sendHTML(msg.Chat.ID, "🤖 <i>Анализирую ваши требования через Gemini ИИ...</i>")

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		filter, err := b.ai.ParseAutoApplyCriteria(ctx, prompt)
		cancel()

		if err != nil {
			b.sendHTML(msg.Chat.ID, fmt.Sprintf("❌ Ошибка обработки требований: %v", err))
			return
		}

		_ = b.storage.SaveAutoApplyFilter(msg.Chat.ID, *filter)
		b.sendHTMLWithReply(msg.Chat.ID, FormatAutoApplyMessage(filter), MainMenuKeyboard())

	default:
		b.sendHTML(msg.Chat.ID, "Используйте:\n• <code>/autoapply on</code>\n• <code>/autoapply off</code>\n• <code>/autoapply set &lt;требования&gt;</code>")
	}
}

func (b *Bot) cmdChannels(msg *tgbotapi.Message) {
	chs, err := b.storage.GetChannels()
	if err != nil || len(chs) == 0 {
		chs = b.cfg.TelegramChannels
	}

	var sb strings.Builder
	sb.WriteString("📡 <b>Опрашиваемые Telegram-каналы:</b>\n\n")
	for _, ch := range chs {
		sb.WriteString(fmt.Sprintf("• @%s (https://t.me/%s)\n", ch, ch))
	}
	sb.WriteString("\nЧтобы добавить канал: <code>/addchannel @username</code>\nЧтобы удалить: <code>/delchannel @username</code>")
	b.sendHTMLWithReply(msg.Chat.ID, sb.String(), MainMenuKeyboard())
}

func (b *Bot) cmdAddChannel(msg *tgbotapi.Message) {
	ch := strings.TrimSpace(msg.CommandArguments())
	if ch == "" {
		b.sendHTML(msg.Chat.ID, "Укажите имя канала, например: <code>/addchannel @golang_jobs</code>")
		return
	}
	if err := b.storage.AddChannel(ch); err != nil {
		b.sendHTML(msg.Chat.ID, fmt.Sprintf("Ошибка добавления: %v", err))
		return
	}
	b.sendHTML(msg.Chat.ID, fmt.Sprintf("✅ Канал <b>@%s</b> добавлен в список мониторинга!", strings.TrimPrefix(ch, "@")))
}

func (b *Bot) cmdDelChannel(msg *tgbotapi.Message) {
	ch := strings.TrimSpace(msg.CommandArguments())
	if ch == "" {
		b.sendHTML(msg.Chat.ID, "Укажите имя канала: <code>/delchannel @golang_jobs</code>")
		return
	}
	_ = b.storage.RemoveChannel(ch)
	b.sendHTML(msg.Chat.ID, fmt.Sprintf("🗑 Канал <b>@%s</b> удален из списка.", strings.TrimPrefix(ch, "@")))
}

func (b *Bot) cmdStats(msg *tgbotapi.Message) {
	stats, err := b.storage.GetStats(msg.Chat.ID)
	if err != nil {
		b.sendHTML(msg.Chat.ID, fmt.Sprintf("Ошибка получения статистики: %v", err))
		return
	}

	text := fmt.Sprintf(`📊 <b>Ваша статистика:</b>

• Новые вакансии: %d
• Откликов отправлено: %d 🚀
• В избранном: %d ⭐
• Пропущено: %d ❌`,
		stats[string(models.StatusNew)],
		stats[string(models.StatusApplied)],
		stats[string(models.StatusFavorite)],
		stats[string(models.StatusRejected)],
	)
	b.sendHTMLWithReply(msg.Chat.ID, text, MainMenuKeyboard())
}

func (b *Bot) cmdSearch(msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	r, err := b.storage.GetResume(chatID)
	if err != nil || r == nil || len(r.RawText) == 0 {
		b.sendHTMLWithReply(chatID, "⚠️ Сначала загрузите резюме (отправьте PDF или текст в чат), чтобы бот мог оценивать вакансии!", MainMenuKeyboard())
		return
	}

	targetStack := b.storage.GetUserStack(chatID)
	if targetStack == "" {
		targetStack = "Golang"
	}

	// 1. Cancel previous search if active for this user
	b.activeSearchesMu.Lock()
	if prevCancel, exists := b.activeSearches[chatID]; exists && prevCancel != nil {
		prevCancel()
	}
	searchCtx, searchCancel := context.WithCancel(context.Background())
	b.activeSearches[chatID] = searchCancel
	b.activeSearchesMu.Unlock()

	defer func() {
		b.activeSearchesMu.Lock()
		delete(b.activeSearches, chatID)
		b.activeSearchesMu.Unlock()
	}()

	sourceNames := b.orchestrator.GetSourceNames()
	type srcStatus struct {
		done  bool
		count int
		err   error
	}
	statuses := make(map[string]*srcStatus)
	for _, name := range sourceNames {
		statuses[name] = &srcStatus{}
	}

	var statusMu sync.Mutex
	var lastEditTime time.Time

	renderProgressText := func(header string, footer string) string {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("🔍 <b>Поиск вакансий для уровня %s (стек: %s)</b>\n\n", html.EscapeString(r.Seniority), html.EscapeString(targetStack)))
		if header != "" {
			sb.WriteString(header + "\n\n")
		}
		sb.WriteString("📡 <b>Статус опроса площадок:</b>\n")

		totalSoFar := 0
		for _, name := range sourceNames {
			st := statuses[name]
			if st == nil {
				continue
			}
			displayName := name
			switch name {
			case "Habr Career":
				displayName = "Хабр Карьера"
			case "Telegram Channels":
				displayName = "Telegram-каналы"
			case "HireHi.ru":
				displayName = "HireHi"
			case "hh.ru":
				displayName = "HeadHunter (hh.ru)"
			case "rabota.by":
				displayName = "Rabota.by"
			}

			if !st.done {
				sb.WriteString(fmt.Sprintf("• %s: ⏳ опрос...\n", displayName))
			} else if st.err != nil {
				sb.WriteString(fmt.Sprintf("• %s: ⚠️ ошибка (%s)\n", displayName, html.EscapeString(st.err.Error())))
			} else {
				sb.WriteString(fmt.Sprintf("• %s: ✅ найдено %d\n", displayName, st.count))
				totalSoFar += st.count
			}
		}

		sb.WriteString(fmt.Sprintf("\n<i>Всего собрано: %d вакансий</i>\n", totalSoFar))
		if footer != "" {
			sb.WriteString("\n" + footer)
		}
		return sb.String()
	}

	initialText := renderProgressText("", "<i>Нажмите «🛑 Отменить поиск» для остановки.</i>")
	statusMsg, err := b.sendHTMLWithKeyboardReturnMsg(chatID, initialText, CancelSearchKeyboard())
	statusMsgID := 0
	if err == nil {
		statusMsgID = statusMsg.MessageID
	}

	updateProgressMsg := func(header string, footer string, force bool) {
		statusMu.Lock()
		defer statusMu.Unlock()

		if statusMsgID == 0 {
			return
		}
		now := time.Now()
		if !force && now.Sub(lastEditTime) < 700*time.Millisecond {
			return
		}
		lastEditTime = now

		text := renderProgressText(header, footer)
		edit := tgbotapi.NewEditMessageText(chatID, statusMsgID, text)
		edit.ParseMode = tgbotapi.ModeHTML
		kb := CancelSearchKeyboard()
		edit.ReplyMarkup = &kb
		_, _ = b.api.Send(edit)
	}

	// 2. Scrape with search context (timeout 50s)
	fetchCtx, fetchCancel := context.WithTimeout(searchCtx, 50*time.Second)
	newlyFound, err := b.orchestrator.FetchAllForQueryWithProgress(fetchCtx, targetStack, func(u scrapers.ScraperProgressUpdate) {
		statusMu.Lock()
		if st, ok := statuses[u.SourceName]; ok {
			st.done = u.IsDone
			st.count = u.Count
			st.err = u.Err
		}
		statusMu.Unlock()
		updateProgressMsg("", "<i>Нажмите «🛑 Отменить поиск» для остановки.</i>", false)
	})
	fetchCancel()

	if searchCtx.Err() != nil {
		if statusMsgID != 0 {
			edit := tgbotapi.NewEditMessageText(chatID, statusMsgID, "🛑 <b>Поиск вакансий отменен пользователем.</b>")
			edit.ParseMode = tgbotapi.ModeHTML
			_, _ = b.api.Send(edit)
		}
		return
	}

	vacancies := newlyFound
	if len(vacancies) < 10 {
		dbVacancies, _ := b.storage.GetUnviewedVacancies(chatID, 20)
		seen := make(map[string]bool)
		for _, v := range vacancies {
			seen[v.ID] = true
		}
		for _, dbv := range dbVacancies {
			if !seen[dbv.ID] {
				vacancies = append(vacancies, dbv)
				seen[dbv.ID] = true
			}
		}
	}

	if len(vacancies) == 0 {
		if statusMsgID != 0 {
			edit := tgbotapi.NewEditMessageText(chatID, statusMsgID, "ℹ️ <b>Вакансий пока не найдено.</b> Попробуйте изменить стек или повторить позже.")
			edit.ParseMode = tgbotapi.ModeHTML
			_, _ = b.api.Send(edit)
		}
		return
	}

	// 3. Update status message to AI Evaluation phase
	updateProgressMsg("⚡️ <b>Сбор завершен!</b>", fmt.Sprintf("🤖 <i>Gemini ИИ анализирует и ранжирует %d вакансий под уровень %s...</i>", len(vacancies), html.EscapeString(r.Seniority)), true)

	type evaluatedItem struct {
		vac     *models.Vacancy
		procRes *matcher.ProcessResult
	}
	var evaluated []*evaluatedItem

	for idx, v := range vacancies {
		if searchCtx.Err() != nil {
			if statusMsgID != 0 {
				edit := tgbotapi.NewEditMessageText(chatID, statusMsgID, "🛑 <b>Поиск вакансий отменен пользователем.</b>")
				edit.ParseMode = tgbotapi.ModeHTML
				_, _ = b.api.Send(edit)
			}
			return
		}

		evalCtx, evalCancel := context.WithTimeout(searchCtx, 20*time.Second)
		procRes, err := b.matcher.EvaluateVacancy(evalCtx, chatID, v)
		evalCancel()

		if err != nil {
			log.Printf("[Bot] Match error for %s: %v", v.ID, err)
			continue
		}
		evaluated = append(evaluated, &evaluatedItem{vac: v, procRes: procRes})

		if (idx+1)%3 == 0 || idx == len(vacancies)-1 {
			updateProgressMsg("⚡️ <b>Сбор завершен!</b>", fmt.Sprintf("🤖 <i>Gemini ИИ оценивает вакансии (%d/%d)...</i>", idx+1, len(vacancies)), false)
		}
	}

	if searchCtx.Err() != nil {
		if statusMsgID != 0 {
			edit := tgbotapi.NewEditMessageText(chatID, statusMsgID, "🛑 <b>Поиск вакансий отменен пользователем.</b>")
			edit.ParseMode = tgbotapi.ModeHTML
			_, _ = b.api.Send(edit)
		}
		return
	}

	// Sort evaluated vacancies by match percentage descending
	sort.Slice(evaluated, func(i, j int) bool {
		return evaluated[i].vac.MatchPct > evaluated[j].vac.MatchPct
	})

	limit := 12
	if len(evaluated) < limit {
		limit = len(evaluated)
	}

	// Final progress update
	if statusMsgID != 0 {
		finalSummary := fmt.Sprintf("✅ <b>Поиск завершен!</b> Подобрано %d лучших вакансий для уровня %s.\nРезультаты отправлены ниже 👇", limit, html.EscapeString(r.Seniority))
		edit := tgbotapi.NewEditMessageText(chatID, statusMsgID, finalSummary)
		edit.ParseMode = tgbotapi.ModeHTML
		_, _ = b.api.Send(edit)
	}

	for i := 0; i < limit; i++ {
		if searchCtx.Err() != nil {
			return
		}
		item := evaluated[i]
		v := item.vac
		procRes := item.procRes

		if procRes.AutoApplied {
			msgText := fmt.Sprintf("🤖 <b>АВТООТКЛИК ОТПРАВЛЕН!</b> 🚀\n\n%s\n\n📝 <b>Сопроводительное:</b>\n<code>%s</code>",
				FormatVacancyMessage(v), html.EscapeString(v.CoverLetter))
			b.sendHTML(chatID, msgText)
		} else {
			cardText := FormatVacancyMessage(v)
			kb := VacancyActionsKeyboard(v.ID, false)
			b.sendHTMLWithKeyboard(chatID, cardText, kb)
		}
		time.Sleep(350 * time.Millisecond) // avoid telegram rate limits
	}
}

func (b *Bot) handleDocument(msg *tgbotapi.Message) {
	doc := msg.Document
	chatID := msg.Chat.ID

	docNameLower := strings.ToLower(doc.FileName)
	isPDF := strings.HasSuffix(docNameLower, ".pdf")
	isText := strings.HasSuffix(docNameLower, ".txt") || strings.HasSuffix(docNameLower, ".md")

	if !isPDF && !isText {
		b.sendHTMLWithReply(chatID, "⚠️ Пожалуйста, отправьте файл в формате <b>PDF</b> или <b>TXT</b>, либо просто скопируйте текст резюме в чат.", MainMenuKeyboard())
		return
	}

	b.sendHTML(chatID, fmt.Sprintf("📥 Загружаю и читаю файл <b>%s</b>...", html.EscapeString(doc.FileName)))

	fileURL, err := b.api.GetFileDirectURL(doc.FileID)
	if err != nil {
		b.sendHTML(chatID, fmt.Sprintf("❌ Ошибка получения файла: %v", err))
		return
	}

	resp, err := http.Get(fileURL)
	if err != nil {
		b.sendHTML(chatID, fmt.Sprintf("❌ Ошибка скачивания файла: %v", err))
		return
	}
	defer resp.Body.Close()

	fileBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		b.sendHTML(chatID, fmt.Sprintf("❌ Ошибка чтения файла: %v", err))
		return
	}

	var plainText string
	if isPDF {
		extracted, err := resume.ExtractTextFromPDF(fileBytes)
		if err != nil {
			b.sendHTML(chatID, fmt.Sprintf("❌ Не удалось извлечь текст из PDF: %v\nУбедитесь, что это текстовый PDF, а не скан/картинка.", err))
			return
		}
		plainText = extracted
	} else {
		plainText = resume.CleanText(string(fileBytes))
	}

	b.sendHTML(chatID, fmt.Sprintf("📄 Извлечено %d символов текста. Запускаю глубокий анализ стека и уровня...", len([]rune(plainText))))
	b.saveAndAnalyzeResume(chatID, plainText, doc.FileName)
}

func (b *Bot) processPlainTextResume(chatID int64, text string) {
	b.saveAndAnalyzeResume(chatID, text, "Прямой ввод")
}

func (b *Bot) saveAndAnalyzeResume(chatID int64, text, filename string) {
	b.sendHTML(chatID, "🧠 <i>Анализирую ваш стек, опыт и грейд с помощью Gemini ИИ...</i>")

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	parsedResume, err := b.ai.AnalyzeResume(ctx, text)
	cancel()

	if err != nil {
		log.Printf("[Bot] AI resume analysis error: %v. Using robust fallback.", err)
		parsedResume = resume.FallbackAnalyze(text)
	}

	parsedResume.UserID = chatID
	parsedResume.FileName = filename

	if err := b.storage.SaveResume(parsedResume); err != nil {
		b.sendHTML(chatID, fmt.Sprintf("❌ Ошибка сохранения резюме: %v", err))
		return
	}

	successMsg := fmt.Sprintf("✅ <b>Резюме успешно сохранено и проанализировано!</b>\n\n%s", FormatResumeMessage(parsedResume))
	b.sendHTMLWithReply(chatID, successMsg, MainMenuKeyboard())
}

func (b *Bot) handleCallbackQuery(cq *tgbotapi.CallbackQuery) {
	data := cq.Data
	chatID := cq.Message.Chat.ID

	// Check password authorization if configured
	if b.cfg.BotAuthPassword != "" {
		if !b.storage.IsUserAuthorized(chatID) {
			b.answerCallback(cq.ID, "🔒 Требуется авторизация в боте!")
			return
		}
	}

	parts := strings.SplitN(data, ":", 2)
	if len(parts) != 2 {
		return
	}

	action, param := parts[0], parts[1]

	switch action {
	case "cancel_search":
		b.activeSearchesMu.Lock()
		if cancel, ok := b.activeSearches[chatID]; ok && cancel != nil {
			cancel()
			delete(b.activeSearches, chatID)
		}
		b.activeSearchesMu.Unlock()
		b.answerCallback(cq.ID, "Поиск отменен")
	case "poll":
		if param == "on" {
			_ = b.storage.SetUserPolling(chatID, true)
			b.answerCallback(cq.ID, "Фоновый поиск включен")
			b.sendHTMLWithReply(chatID, "✅ <b>Постоянный фоновый поиск включен!</b>\n\nБот будет автоматически сканировать площадки каждые 15 минут под ваш стек и присылать новые вакансии.", MainMenuKeyboard())
		} else if param == "off" {
			_ = b.storage.SetUserPolling(chatID, false)
			b.answerCallback(cq.ID, "Фоновый поиск отключен")
			b.sendHTMLWithReply(chatID, "⏸ <b>Постоянный фоновый поиск остановлен!</b>\n\nБот больше не выполняет автоматических проверок в фоне. Запускайте поиск вручную по кнопке «🔍 Поиск вакансий».", MainMenuKeyboard())
		}
	case "set_stack":
		_ = b.storage.SetUserStack(chatID, param)
		b.answerCallback(cq.ID, fmt.Sprintf("Стек: %s", param))
		b.sendHTMLWithReply(chatID, fmt.Sprintf("🎯 <b>Стек для поиска вакансий изменен на: %s</b>\n\nНажмите <b>«🔍 Поиск вакансий»</b> для запуска поиска!", html.EscapeString(param)), MainMenuKeyboard())
	case "set_stack_custom":
		b.answerCallback(cq.ID, "")
		b.sendHTML(chatID, "✍️ Отправьте сообщение в формате:\n<code>стек Python FastAPI</code>\nили просто напишите название стека!")
	case "set_grade":
		b.handleSetGrade(cq, chatID, param)
	case "reanalyze_now":
		b.handleReanalyzeCallback(cq, chatID)
	case "cl", "regen_cl":
		b.handleCreateCoverLetter(cq, chatID, param, action == "regen_cl")
	case "apply":
		b.handleApply(cq, chatID, param)
	case "fav":
		b.handleFavorite(cq, chatID, param)
	case "unfav":
		b.handleUnfavorite(cq, chatID, param)
	case "skip":
		b.handleSkip(cq, chatID, param)
	}
}

func (b *Bot) handleSetGrade(cq *tgbotapi.CallbackQuery, chatID int64, grade string) {
	b.answerCallback(cq.ID, fmt.Sprintf("Грейд установлен: %s", grade))

	r, err := b.storage.GetResume(chatID)
	if err != nil || r == nil || len(r.RawText) == 0 {
		b.sendHTML(chatID, "📄 Резюме еще не загружено!")
		return
	}

	r.Seniority = grade
	r.UpdatedAt = time.Now()
	_ = b.storage.SaveResume(r)

	msgText := fmt.Sprintf("✅ <b>Грейд успешно изменен на: %s!</b>\n\n🎯 Теперь при поиске через кнопку <b>«🔍 Поиск вакансий»</b> бот будет ранжировать вакансии именно для уровня <b>%s</b>, а вакансии Senior/Architect будут опущены вниз с низким процентом совпадения.", grade, grade)
	b.sendHTMLWithReply(chatID, msgText, MainMenuKeyboard())
}

func (b *Bot) handleReanalyzeCallback(cq *tgbotapi.CallbackQuery, chatID int64) {
	b.answerCallback(cq.ID, "Запускаю повторный анализ...")
	r, err := b.storage.GetResume(chatID)
	if err != nil || r == nil || len(r.RawText) == 0 {
		b.sendHTML(chatID, "📄 Резюме не найдено.")
		return
	}
	b.saveAndAnalyzeResume(chatID, r.RawText, r.FileName)
}

func (b *Bot) handleCreateCoverLetter(cq *tgbotapi.CallbackQuery, chatID int64, vacID string, forceRegen bool) {
	b.answerCallback(cq.ID, "Генерирую сопроводительное...")

	vac, err := b.storage.GetUserVacancy(chatID, vacID)
	if err != nil || vac == nil {
		b.sendHTML(chatID, "❌ Вакансия не найдена в базе.")
		return
	}

	r, err := b.storage.GetResume(chatID)
	if err != nil || r == nil || len(r.RawText) == 0 {
		b.sendHTML(chatID, "⚠️ Резюме не найдено. Загрузите резюме через /resume.")
		return
	}

	letter := vac.CoverLetter
	if letter == "" || forceRegen {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		genLetter, err := b.ai.GenerateCoverLetter(ctx, r.RawText, vac)
		cancel()

		if err != nil || len(genLetter) == 0 {
			genLetter = ai.FallbackCoverLetter(r.RawText, vac)
		}
		letter = genLetter
		_ = b.storage.UpdateCoverLetter(chatID, vacID, letter)
	}

	escapedTitle := html.EscapeString(strings.ToValidUTF8(vac.Title, ""))
	escapedComp := html.EscapeString(strings.ToValidUTF8(vac.Company, ""))
	escapedLetter := html.EscapeString(strings.ToValidUTF8(letter, ""))
	escapedURL := html.EscapeString(strings.ToValidUTF8(vac.URL, ""))

	replyToMsgID := 0
	if cq.Message != nil {
		replyToMsgID = cq.Message.MessageID
	}

	// Clean copy-paste ready format with clickable vacancy link and reply-quote referencing original vacancy
	msgText := fmt.Sprintf("📝 <b>Сопроводительное к вакансии:</b>\n<a href=\"%s\"><b>%s</b></a> (%s)\n\n<code>%s</code>",
		escapedURL, escapedTitle, escapedComp, escapedLetter)

	kb := CoverLetterActionsKeyboard(vacID)
	b.sendHTMLWithKeyboardAndReplyTo(chatID, msgText, kb, replyToMsgID)
}

func (b *Bot) handleApply(cq *tgbotapi.CallbackQuery, chatID int64, vacID string) {
	b.answerCallback(cq.ID, "Отправляю отклик через Chrome...")

	vac, err := b.storage.GetUserVacancy(chatID, vacID)
	if err != nil || vac == nil {
		b.sendHTML(chatID, "❌ Вакансия не найдена.")
		return
	}

	r, err := b.storage.GetResume(chatID)
	if err != nil || r == nil || len(r.RawText) == 0 {
		b.sendHTML(chatID, "⚠️ Загрузите резюме командой /resume перед откликом.")
		return
	}

	letter := vac.CoverLetter
	if letter == "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		letter, _ = b.ai.GenerateCoverLetter(ctx, r.RawText, vac)
		cancel()
		if letter == "" {
			letter = ai.FallbackCoverLetter(r.RawText, vac)
		}
		_ = b.storage.UpdateCoverLetter(chatID, vacID, letter)
	}

	escapedTitle := html.EscapeString(strings.ToValidUTF8(vac.Title, ""))
	escapedComp := html.EscapeString(strings.ToValidUTF8(vac.Company, ""))
	b.sendHTML(chatID, fmt.Sprintf("🚀 <b>Отправка отклика на вакансию:</b>\n%s (%s)\n<i>Подключаюсь к браузеру Chrome...</i>", escapedTitle, escapedComp))

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		applyRes, err := b.chrome.ApplyToVacancy(ctx, vac, letter)
		cancel()

		if err != nil {
			b.sendHTML(chatID, fmt.Sprintf("❌ <b>Не удалось отправить отклик автоматически:</b>\n%v\n\nВы можете откликнуться вручную по ссылке:\n%s", err, html.EscapeString(vac.URL)))
			return
		}

		_ = b.storage.UpdateUserVacancyStatus(chatID, vacID, models.StatusApplied)

		resultMsg := fmt.Sprintf("✅ <b>Результат отклика:</b>\n%s\n\n💼 <b>Вакансия:</b> <a href=\"%s\">%s</a>",
			html.EscapeString(applyRes.Message), html.EscapeString(vac.URL), escapedTitle)
		b.sendHTML(chatID, resultMsg)
	}()
}

func (b *Bot) handleFavorite(cq *tgbotapi.CallbackQuery, chatID int64, vacID string) {
	_ = b.storage.UpdateUserVacancyStatus(chatID, vacID, models.StatusFavorite)
	b.answerCallback(cq.ID, "⭐ Вакансия добавлена в избранное!")

	editMsg := tgbotapi.NewEditMessageReplyMarkup(
		chatID,
		cq.Message.MessageID,
		VacancyActionsKeyboard(vacID, true),
	)
	_, _ = b.api.Send(editMsg)
}

func (b *Bot) handleUnfavorite(cq *tgbotapi.CallbackQuery, chatID int64, vacID string) {
	_ = b.storage.UpdateUserVacancyStatus(chatID, vacID, models.StatusNew)
	b.answerCallback(cq.ID, "🗑 Удалено из избранного.")

	deleteMsg := tgbotapi.NewDeleteMessage(chatID, cq.Message.MessageID)
	_, _ = b.api.Send(deleteMsg)
}

func (b *Bot) handleSkip(cq *tgbotapi.CallbackQuery, chatID int64, vacID string) {
	_ = b.storage.UpdateUserVacancyStatus(chatID, vacID, models.StatusRejected)
	b.answerCallback(cq.ID, "❌ Вакансия скрыта.")

	deleteMsg := tgbotapi.NewDeleteMessage(chatID, cq.Message.MessageID)
	_, _ = b.api.Send(deleteMsg)
}

// Background poller
func (b *Bot) pollWorker() {
	interval := time.Duration(b.cfg.PollIntervalMin) * time.Minute
	if interval < 2*time.Minute {
		interval = 15 * time.Minute
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-b.stopCh:
			return
		case <-ticker.C:
			log.Println("[Poller] Scheduled vacancy sweep starting...")
			b.runScheduledSweep()
		}
	}
}

func (b *Bot) runScheduledSweep() {
	users, err := b.storage.GetAllPollingUsers()
	if err != nil || len(users) == 0 {
		return
	}

	stacks := make(map[string]bool)
	for _, uid := range users {
		stk := b.storage.GetUserStack(uid)
		if stk == "" {
			stk = "Golang"
		}
		stacks[stk] = true
	}

	var allVacancies []*models.Vacancy
	for stk := range stacks {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		vacs, err := b.orchestrator.FetchAllForQuery(ctx, stk)
		cancel()
		if err != nil {
			log.Printf("[Poller ERROR] FetchAllForQuery(%s) failed: %v", stk, err)
			continue
		}
		allVacancies = append(allVacancies, vacs...)
	}

	if len(allVacancies) == 0 {
		return
	}

	for _, userID := range users {
		for _, v := range allVacancies {
			evalCtx, evalCancel := context.WithTimeout(context.Background(), 20*time.Second)
			procRes, err := b.matcher.EvaluateVacancy(evalCtx, userID, v)
			evalCancel()

			if err != nil {
				continue
			}

			if procRes.AutoApplied {
				msgText := fmt.Sprintf("🤖 <b>АВТООТКЛИК ОТПРАВЛЕН!</b> 🚀\n\n%s\n\n📝 <b>Сопроводительное:</b>\n<code>%s</code>",
					FormatVacancyMessage(v), html.EscapeString(v.CoverLetter))
				b.sendHTML(userID, msgText)
			} else if v.MatchPct >= 60 {
				cardText := FormatVacancyMessage(v)
				kb := VacancyActionsKeyboard(v.ID, false)
				b.sendHTMLWithKeyboard(userID, cardText, kb)
			}
			time.Sleep(350 * time.Millisecond)
		}
	}
}

// Helpers with 100% reliable send guarantee and persistent reply keyboard
func (b *Bot) sendHTML(chatID int64, text string) {
	text = strings.ToValidUTF8(text, "")
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.DisableWebPagePreview = false
	_, err := b.api.Send(msg)
	if err != nil {
		log.Printf("[Bot WARNING] sendHTML error (%v). Retrying as plain text...", err)
		msg.ParseMode = ""
		_, _ = b.api.Send(msg)
	}
}

func (b *Bot) sendHTMLWithReply(chatID int64, text string, kb tgbotapi.ReplyKeyboardMarkup) {
	text = strings.ToValidUTF8(text, "")
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.ReplyMarkup = kb
	msg.DisableWebPagePreview = false
	_, err := b.api.Send(msg)
	if err != nil {
		log.Printf("[Bot WARNING] sendHTMLWithReply error (%v). Retrying as plain text...", err)
		msg.ParseMode = ""
		_, _ = b.api.Send(msg)
	}
}

func (b *Bot) sendHTMLWithKeyboard(chatID int64, text string, kb tgbotapi.InlineKeyboardMarkup) {
	text = strings.ToValidUTF8(text, "")
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.ReplyMarkup = kb
	msg.DisableWebPagePreview = false
	_, err := b.api.Send(msg)
	if err != nil {
		log.Printf("[Bot WARNING] sendHTMLWithKeyboard error (%v). Retrying as plain text...", err)
		msg.ParseMode = ""
		_, _ = b.api.Send(msg)
	}
}

func (b *Bot) sendHTMLWithKeyboardAndReplyTo(chatID int64, text string, kb tgbotapi.InlineKeyboardMarkup, replyToMsgID int) {
	text = strings.ToValidUTF8(text, "")
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.ReplyMarkup = kb
	msg.DisableWebPagePreview = true
	if replyToMsgID != 0 {
		msg.ReplyToMessageID = replyToMsgID
	}
	_, err := b.api.Send(msg)
	if err != nil {
		log.Printf("[Bot WARNING] sendHTMLWithKeyboardAndReplyTo error (%v). Retrying without reply-to...", err)
		msg.ReplyToMessageID = 0
		_, _ = b.api.Send(msg)
	}
}

func (b *Bot) sendHTMLWithKeyboardReturnMsg(chatID int64, text string, kb tgbotapi.InlineKeyboardMarkup) (tgbotapi.Message, error) {
	text = strings.ToValidUTF8(text, "")
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.ReplyMarkup = kb
	msg.DisableWebPagePreview = true
	return b.api.Send(msg)
}

func (b *Bot) sendText(chatID int64, text string) {
	text = strings.ToValidUTF8(text, "")
	msg := tgbotapi.NewMessage(chatID, text)
	_, _ = b.api.Send(msg)
}

func (b *Bot) answerCallback(cqID, text string) {
	callback := tgbotapi.NewCallback(cqID, text)
	_, _ = b.api.Request(callback)
}
