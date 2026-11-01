# 🚀 Golang Job Hunter Bot & Auto-Apply AI

Телеграм-бот на языке **Golang** для непрерывного мониторинга, парсинга и автоматического отклика на вакансии Go-разработчика со всех популярных ресурсов с использованием ИИ Gemini и автономного браузерного движка Chrome.

---

## 🌟 Архитектура и возможности

1. **Агрегация вакансий со всех ключевых ресурсов:**
   - **HeadHunter (`hh.ru`)**
   - **Хабр Карьера (`career.habr.com`)**
   - **Rabota.by**
   - **HireHi.ru**
   - **Telegram-каналы** (`@jobgolang`, `@golang_job`, `@godevjob` и любые добавленные каналы).

2. **Запоминание и анализ резюме:**
   - Прием резюме файлом **PDF** (с нативным парсингом текста) или обычным сообщением в чат.
   - ИИ Gemini структурирует опыт, грейд (Junior/Middle/Senior/Lead) и список навыков.

3. **Скоринг вакансий (AI Match):**
   - Процент релевантности (% совпадения резюме с вакансией).
   - Выявление совпадающих и недостающих технологий.
   - Краткий вердикт ИИ.

4. **4 интерактивных действия под каждой вакансией:**
   - 📝 **Создать сопроводительное** — генерирует сжатое, естественное письмо через Gemini (без роботизированных штампов).
   - 🚀 **Откликнуться** — бот открывает вкладку в Chrome, нажимает кнопку отклика, подставляет письмо и отправляет заявку.
   - ⭐ **В избранное** — сохранение вакансии для последующего просмотра.
   - ❌ **Пропустить / Отказаться** — скрывает вакансию.

5. **Автоотклики на естественном языке:**
   - Настройка требований обычной фразой (например: *"Откликайся на Golang Middle+ от 250 000 руб, только удаленка, без крипты и гемблинга, совпадение от 70%"*).
   - ИИ Gemini преобразует требования в фильтры. При появлении новых подходящих вакансий бот автоматически формирует сопроводительное и отправляет отклик.

6. **Кроссплатформенный автономный движок Chrome:**
   - Запускается в фоновом Headless-режиме с флагами `--no-sandbox`, `--disable-dev-shm-usage`, `--disable-gpu`.
   - Идеально работает на **Ubuntu Server / VPS** без GUI и без X11.
   - Чистый Go SQLite (без CGO) — статически скомпилированный бинарник запускается на любом дистрибутиве Linux.

---

## 🐧 Развертывание на сервере Ubuntu

### Вариант 1: Запуск бинарника (быстро)

1. **Установите Google Chrome на Ubuntu:**
   ```bash
   sudo apt update
   sudo apt install -y wget ca-certificates
   wget https://dl.google.com/linux/direct/google-chrome-stable_current_amd64.deb
   sudo apt install -y ./google-chrome-stable_current_amd64.deb
   ```

2. **Скопируйте файлы на сервер в `/opt/jobparser`:**
   - `jobparser-linux` (готовый скомпилированный бинарник)
   - `config.json`

3. **Сделайте файл исполняемым и запустите:**
   ```bash
   chmod +x /opt/jobparser/jobparser-linux
   /opt/jobparser/jobparser-linux
   ```

4. **Настройка автозапуска через Systemd:**
   Скопируйте файл `jobparser.service` в `/etc/systemd/system/`:
   ```bash
   sudo cp jobparser.service /etc/systemd/system/
   sudo systemctl daemon-reload
   sudo systemctl enable --now jobparser
   ```
   Просмотр логов:
   ```bash
   journalctl -u jobparser -f
   ```

---

### Вариант 2: Запуск через Docker / Docker Compose

Если вы используете Docker на сервере:
```bash
docker compose up -d --build
```
Внутри Docker-контейнера уже установлен Chromium и настроены все необходимые флаги.

---

## ⚙️ Настройка `config.json`

```json
{
  "telegram_bot_token": "ВАШ_ТОКЕН_ОТ_BOTFATHER",
  "gemini_api_key": "ВАШ_БЕСПЛАТНЫЙ_КЛЮЧ_GEMINI",
  "gemini_model": "gemini-1.5-flash",
  "headless_browser": true,
  "chrome_profile_dir": "./data/chrome_profile",
  "poll_interval_min": 15
}
```

* **Telegram Bot Token:** создается у [@BotFather](https://t.me/BotFather) командой `/newbot`.
* **Gemini API Key:** бесплатный ключ в [Google AI Studio](https://aistudio.google.com/app/apikey).

---

## 💬 Команды Телеграм-бота

| Команда | Описание |
|---|---|
| `/start` | Приветствие и руководство |
| `/resume` | Просмотр текущего резюме и навыков |
| `/search` или `/parse` | Запуск немедленного сканирования вакансий |
| `/favorites` | Список избранных вакансий |
| `/autoapply` | Статус и правила автооткликов |
| `/autoapply on` / `/autoapply off` | Включить / отключить автоотклики |
| `/autoapply set <требования>` | Настроить критерии отклика на обычном языке |
| `/channels` | Список опрашиваемых Telegram-каналов |
| `/addchannel @username` | Добавить Telegram-канал |
| `/delchannel @username` | Удалить Telegram-канал |
| `/stats` | Статистика по вакансиям и откликам |
| `/help` | Справка |
