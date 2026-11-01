package bot

import (
	"fmt"
	"html"
	"strings"

	"jobparser/internal/matcher"
	"jobparser/internal/models"
)

func MatchBadge(pct int) string {
	switch {
	case pct >= 80:
		return fmt.Sprintf("🟢 <b>%d%% совпадение</b> (Отличный матч!)", pct)
	case pct >= 60:
		return fmt.Sprintf("🟡 <b>%d%% совпадение</b> (Хороший матч)", pct)
	case pct >= 40:
		return fmt.Sprintf("🟠 <b>%d%% совпадение</b> (Умеренное соответствие)", pct)
	default:
		return fmt.Sprintf("⚪ <b>%d%% совпадение</b>", pct)
	}
}

// FormatVacancyMessage builds clean, structured Telegram HTML message
func FormatVacancyMessage(v *models.Vacancy) string {
	var sb strings.Builder

	title := html.EscapeString(strings.ToValidUTF8(v.Title, ""))
	sb.WriteString(fmt.Sprintf("💼 <b>%s</b>\n", title))

	if v.Company != "" {
		comp := html.EscapeString(strings.ToValidUTF8(v.Company, ""))
		sb.WriteString(fmt.Sprintf("🏢 <b>Компания:</b> %s\n", comp))
	}
	if v.Salary != "" {
		sal := html.EscapeString(strings.ToValidUTF8(v.Salary, ""))
		sb.WriteString(fmt.Sprintf("💰 <b>Зарплата:</b> %s\n", sal))
	}
	if v.Location != "" {
		loc := html.EscapeString(strings.ToValidUTF8(v.Location, ""))
		sb.WriteString(fmt.Sprintf("📍 <b>Формат / Локация:</b> %s\n", loc))
	}

	src := html.EscapeString(strings.ToValidUTF8(v.Source, ""))
	sb.WriteString(fmt.Sprintf("🌐 <b>Источник:</b> %s\n", src))

	// Expected grade display
	grade := v.ExpectedGrade
	if grade == "" {
		grade = matcher.DetectVacancyGrade(v.Title, v.Requirements, v.Description)
	}
	gradeBadge := "⚪ " + grade
	switch {
	case strings.Contains(grade, "Junior"):
		gradeBadge = "🟢 <b>" + grade + "</b>"
	case strings.Contains(grade, "Middle"):
		gradeBadge = "🟡 <b>" + grade + "</b>"
	case strings.Contains(grade, "Senior"):
		gradeBadge = "🔴 <b>" + grade + "</b>"
	case strings.Contains(grade, "Lead") || strings.Contains(grade, "Architect"):
		gradeBadge = "🟣 <b>" + grade + "</b>"
	}
	sb.WriteString(fmt.Sprintf("🎯 <b>Ожидаемый уровень:</b> %s\n\n", gradeBadge))

	// Match info
	sb.WriteString(fmt.Sprintf("📊 %s\n", MatchBadge(v.MatchPct)))
	if v.MatchAnalysis != "" {
		analysis := html.EscapeString(strings.ToValidUTF8(v.MatchAnalysis, ""))
		sb.WriteString(fmt.Sprintf("💡 <i>%s</i>\n\n", analysis))
	}

	// Requirements or description snippet
	reqText := strings.ToValidUTF8(v.Requirements, "")
	if reqText == "" {
		reqText = strings.ToValidUTF8(v.Description, "")
	}
	reqRunes := []rune(reqText)
	if len(reqRunes) > 300 {
		reqText = string(reqRunes[:300]) + "..."
	}
	if reqText != "" {
		sb.WriteString(fmt.Sprintf("📋 <b>Требования:</b>\n%s\n\n", html.EscapeString(reqText)))
	}

	escapedURL := html.EscapeString(strings.ToValidUTF8(v.URL, ""))
	sb.WriteString(fmt.Sprintf("🔗 <a href=\"%s\">Перейти к вакансии</a>", escapedURL))

	return sb.String()
}

// FormatResumeMessage summarizes user's loaded resume
func FormatResumeMessage(r *models.Resume) string {
	if r == nil || len(r.RawText) == 0 {
		return "📄 <b>Резюме еще не загружено!</b>\n\nОтправьте мне файл резюме в формате <b>PDF</b> или просто пришлите текст резюме в чат."
	}

	var sb strings.Builder
	sb.WriteString("📄 <b>Ваше активное резюме:</b>\n\n")

	if r.Seniority != "" {
		sb.WriteString(fmt.Sprintf("🎯 <b>Текущий грейд:</b> %s\n", html.EscapeString(strings.ToValidUTF8(r.Seniority, ""))))
	}
	if r.DesiredRole != "" {
		sb.WriteString(fmt.Sprintf("💼 <b>Целевая позиция:</b> %s\n", html.EscapeString(strings.ToValidUTF8(r.DesiredRole, ""))))
	}
	if len(r.Skills) > 0 {
		skillsStr := strings.Join(r.Skills, ", ")
		sb.WriteString(fmt.Sprintf("🛠 <b>Ключевые навыки:</b> %s\n", html.EscapeString(strings.ToValidUTF8(skillsStr, ""))))
	}
	if r.Summary != "" {
		sb.WriteString(fmt.Sprintf("\n📝 <b>Опыт и стек:</b>\n%s\n", html.EscapeString(strings.ToValidUTF8(r.Summary, ""))))
	}

	sb.WriteString(fmt.Sprintf("\n🕒 <i>Обновлено: %s</i>\n", r.UpdatedAt.Format("02.01.2006 15:04")))
	sb.WriteString("\n<i>💡 Чтобы скорректировать данные: напишите команду /edit поменяй уровень на junior (или нажмите «✏️ Изменить резюме» в меню).</i>")
	return sb.String()
}

// FormatAutoApplyMessage formats auto-apply settings
func FormatAutoApplyMessage(f *models.AutoApplyFilter) string {
	if f == nil || !f.Enabled {
		return "🤖 <b>Автоотклики: ВЫКЛЮЧЕНЫ</b> ❌\n\n" +
			"Чтобы включить и настроить требования обычным языком, отправьте команду:\n" +
			"<code>/autoapply set Откликайся на все вакансии Golang от Junior с зарплатой от 120 000 руб, где совпадение больше 70%% и удаленка</code>"
	}

	var sb strings.Builder
	sb.WriteString("🤖 <b>Автоотклики: ВКЛЮЧЕНЫ</b> ✅\n\n")
	sb.WriteString(fmt.Sprintf("💬 <b>Ваши требования:</b>\n<i>\"%s\"</i>\n\n", html.EscapeString(strings.ToValidUTF8(f.NaturalPrompt, ""))))
	sb.WriteString(fmt.Sprintf("🎯 <b>Минимальное совпадение:</b> %d%%\n", f.MinMatchPct))

	if f.MinSalaryRub > 0 {
		sb.WriteString(fmt.Sprintf("💰 <b>Мин. зарплата (RUB):</b> %d ₽\n", f.MinSalaryRub))
	}
	if f.MinSalaryUsd > 0 {
		sb.WriteString(fmt.Sprintf("💵 <b>Мин. зарплата (USD/EUR):</b> %d $\n", f.MinSalaryUsd))
	}
	if f.RemoteOnly {
		sb.WriteString("🏠 <b>Только удаленная работа:</b> Да\n")
	}
	if len(f.Seniority) > 0 {
		sb.WriteString(fmt.Sprintf("🎖 <b>Грейды:</b> %s\n", html.EscapeString(strings.ToValidUTF8(strings.Join(f.Seniority, ", "), ""))))
	}
	if len(f.BlacklistKeywords) > 0 {
		sb.WriteString(fmt.Sprintf("🚫 <b>Черный список слов:</b> %s\n", html.EscapeString(strings.ToValidUTF8(strings.Join(f.BlacklistKeywords, ", "), ""))))
	}

	sb.WriteString("\nКоманды:\n" +
		"• <code>/autoapply off</code> — отключить\n" +
		"• <code>/autoapply set &lt;новые требования&gt;</code> — изменить")
	return sb.String()
}
