package bot

import (
	"fmt"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// MainMenuKeyboard creates the persistent bottom reply keyboard for easy one-tap control
func MainMenuKeyboard() tgbotapi.ReplyKeyboardMarkup {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("🔍 Поиск вакансий"),
			tgbotapi.NewKeyboardButton("🎯 Выбрать стек"),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("⭐ Избранное"),
			tgbotapi.NewKeyboardButton("🌐 Веб-интерфейс"),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("📄 Мое резюме"),
			tgbotapi.NewKeyboardButton("✏️ Изменить резюме"),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("🔄 Фоновый поиск"),
			tgbotapi.NewKeyboardButton("📊 Статистика"),
		),
	)
	kb.ResizeKeyboard = true
	kb.InputFieldPlaceholder = "Выберите действие или введите команду..."
	return kb
}

// VacancyActionsKeyboard returns the 4 requested actions for each vacancy
func VacancyActionsKeyboard(vacID string, hasCoverLetter bool) tgbotapi.InlineKeyboardMarkup {
	clButtonText := "📝 Создать сопроводительное"
	if hasCoverLetter {
		clButtonText = "📝 Посмотреть сопроводительное"
	}

	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(clButtonText, fmt.Sprintf("cl:%s", vacID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🚀 Откликнуться", fmt.Sprintf("apply:%s", vacID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⭐ В избранное", fmt.Sprintf("fav:%s", vacID)),
			tgbotapi.NewInlineKeyboardButtonData("❌ Пропустить", fmt.Sprintf("skip:%s", vacID)),
		),
	)
}

// CoverLetterActionsKeyboard gives options to apply immediately or regenerate
func CoverLetterActionsKeyboard(vacID string) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🚀 Откликнуться с этим письмом", fmt.Sprintf("apply:%s", vacID)),
			tgbotapi.NewInlineKeyboardButtonData("🔄 Перегенерировать", fmt.Sprintf("regen_cl:%s", vacID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⭐ В избранное", fmt.Sprintf("fav:%s", vacID)),
		),
	)
}

// FavoriteItemKeyboard for favorites list - now includes direct cover letter generation!
func FavoriteItemKeyboard(vacID, url string) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📝 Сопроводительное", fmt.Sprintf("cl:%s", vacID)),
			tgbotapi.NewInlineKeyboardButtonData("🚀 Откликнуться", fmt.Sprintf("apply:%s", vacID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonURL("🔗 Открыть вакансию", url),
			tgbotapi.NewInlineKeyboardButtonData("🗑 Удалить", fmt.Sprintf("unfav:%s", vacID)),
		),
	)
}

// EditResumeKeyboard presents quick one-tap grade selection buttons
func EditResumeKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🟢 Junior", "set_grade:Junior"),
			tgbotapi.NewInlineKeyboardButtonData("🟡 Middle", "set_grade:Middle"),
			tgbotapi.NewInlineKeyboardButtonData("🔴 Senior", "set_grade:Senior"),
			tgbotapi.NewInlineKeyboardButtonData("🟣 Lead", "set_grade:Lead"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 Переанализировать через ИИ", "reanalyze_now:all"),
		),
	)
}

// StackSelectionKeyboard provides instant buttons for tech stack selection
func StackSelectionKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🐹 Golang", "set_stack:golang"),
			tgbotapi.NewInlineKeyboardButtonData("🐍 Python", "set_stack:python"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⚛️ Frontend", "set_stack:frontend"),
			tgbotapi.NewInlineKeyboardButtonData("☕ Java", "set_stack:java"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⚙️ DevOps", "set_stack:devops"),
			tgbotapi.NewInlineKeyboardButtonData("🦀 Rust", "set_stack:rust"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✍️ Ввести свой стек вручную", "set_stack_custom:input"),
		),
	)
}

// PollControlKeyboard creates toggle buttons for continuous background search
func PollControlKeyboard(isPolling bool) tgbotapi.InlineKeyboardMarkup {
	if isPolling {
		return tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("⏸ Отключить постоянный поиск", "poll:off"),
			),
		)
	}
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("▶️ Включить постоянный поиск", "poll:on"),
		),
	)
}

// CancelSearchKeyboard provides a 1-tap cancel button during vacancy search
func CancelSearchKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🛑 Отменить поиск", "cancel_search:now"),
		),
	)
}
