package resume

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"jobparser/internal/models"

	"github.com/dslipak/pdf"
)

var (
	spacesRegex   = regexp.MustCompile(`[ \t]+`)
	newlinesRegex = regexp.MustCompile(`\n{3,}`)

	// Skill patterns for Go ecosystem
	skillKeywords = []struct {
		Pattern string
		Name    string
	}{
		{`(?i)\b(?:golang|go)\b`, "Golang"},
		{`(?i)\b(?:postgresql|postgres)\b`, "PostgreSQL"},
		{`(?i)\bmysql\b`, "MySQL"},
		{`(?i)\bredis\b`, "Redis"},
		{`(?i)\bdocker\b`, "Docker"},
		{`(?i)\b(?:k8s|kubernetes)\b`, "Kubernetes"},
		{`(?i)\bgrpc\b`, "gRPC"},
		{`(?i)\bprotobuf\b`, "Protobuf"},
		{`(?i)\bkafka\b`, "Kafka"},
		{`(?i)\brabbitmq\b`, "RabbitMQ"},
		{`(?i)\bclickhouse\b`, "ClickHouse"},
		{`(?i)\belasticsearch\b`, "ElasticSearch"},
		{`(?i)\bmongodb\b`, "MongoDB"},
		{`(?i)\bci/cd\b`, "CI/CD"},
		{`(?i)\bgit\b`, "Git"},
		{`(?i)\blinux\b`, "Linux"},
		{`(?i)\bmicroservices\b|микросервис`, "Microservices"},
		{`(?i)\brest(?:ful)?\b`, "REST API"},
		{`(?i)\bwebsocket\b`, "WebSocket"},
		{`(?i)\bgraphql\b`, "GraphQL"},
		{`(?i)\b(?:gin|echo|fiber|chi)\b`, "Go Web Frameworks"},
		{`(?i)\b(?:gorm|sqlx)\b`, "ORM / SQLX"},
		{`(?i)\bprometheus\b`, "Prometheus"},
		{`(?i)\bgrafana\b`, "Grafana"},
		{`(?i)\bclean\s+arch\b|чистая\s+архитектур`, "Clean Architecture"},
		{`(?i)\bunittesting\b|unit\s+test|тестирован`, "Unit Testing"},
		{`(?i)\baws\b`, "AWS"},
		{`(?i)\bterraform\b`, "Terraform"},
	}
)

// CleanText normalizes extracted resume text
func CleanText(text string) string {
	text = strings.ToValidUTF8(text, "")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = spacesRegex.ReplaceAllString(text, " ")
	text = newlinesRegex.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}

// ExtractTextFromPDF reads all text content from PDF bytes
func ExtractTextFromPDF(pdfBytes []byte) (string, error) {
	if len(pdfBytes) == 0 {
		return "", fmt.Errorf("pdf file is empty")
	}

	reader, err := pdf.NewReader(bytes.NewReader(pdfBytes), int64(len(pdfBytes)))
	if err != nil {
		return "", fmt.Errorf("failed to open pdf: %w", err)
	}

	plainText, err := reader.GetPlainText()
	if err != nil {
		return "", fmt.Errorf("failed to get plain text from pdf: %w", err)
	}

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, plainText); err != nil {
		return "", fmt.Errorf("failed to read pdf text stream: %w", err)
	}

	text := CleanText(buf.String())
	if len(text) < 20 {
		return "", fmt.Errorf("extracted text from PDF is too short or empty (scanned/image PDF)")
	}

	return text, nil
}

// ExtractSkills uses pattern matching to extract Go developer skills from text
func ExtractSkills(text string) []string {
	seen := make(map[string]bool)
	var skills []string

	for _, sk := range skillKeywords {
		re := regexp.MustCompile(sk.Pattern)
		if re.MatchString(text) {
			if !seen[sk.Name] {
				seen[sk.Name] = true
				skills = append(skills, sk.Name)
			}
		}
	}

	if len(skills) == 0 {
		skills = []string{"Golang", "PostgreSQL", "Docker", "Git"}
	}
	return skills
}

// ExtractSeniority detects level from resume text
func ExtractSeniority(text string) string {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "lead") || strings.Contains(lower, "тимлид") || strings.Contains(lower, "team lead") || strings.Contains(lower, "tech lead"):
		return "Lead"
	case strings.Contains(lower, "senior") || strings.Contains(lower, "сеньор") || strings.Contains(lower, "синиор"):
		return "Senior"
	case strings.Contains(lower, "middle") || strings.Contains(lower, "мидл"):
		return "Middle"
	case strings.Contains(lower, "junior") || strings.Contains(lower, "джуниор"):
		return "Junior"
	default:
		return "Middle"
	}
}

// ExtractDesiredRole detects role
func ExtractDesiredRole(text string) string {
	lower := strings.ToLower(text)
	if strings.Contains(lower, "backend") || strings.Contains(lower, "бэкенд") {
		return "Golang Backend Developer"
	}
	if strings.Contains(lower, "team lead") || strings.Contains(lower, "тимлид") {
		return "Lead Golang Developer"
	}
	return "Golang Developer"
}

// FallbackAnalyze creates a structured Resume using rule-based heuristics when AI is unavailable
func FallbackAnalyze(text string) *models.Resume {
	cleaned := CleanText(text)
	skills := ExtractSkills(cleaned)
	seniority := ExtractSeniority(cleaned)
	role := ExtractDesiredRole(cleaned)

	// Build a concise summary from the first 2-3 readable sentences
	lines := strings.Split(cleaned, "\n")
	var summaryParts []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if len(l) > 30 && !strings.Contains(l, "http") && !strings.Contains(l, "@") {
			summaryParts = append(summaryParts, l)
			if len(summaryParts) >= 2 {
				break
			}
		}
	}

	summary := strings.Join(summaryParts, ". ")
	if summary == "" {
		summary = fmt.Sprintf("%s со стеком: %s.", role, strings.Join(skills, ", "))
	}
	if len(summary) > 250 {
		summary = summary[:247] + "..."
	}

	return &models.Resume{
		RawText:     cleaned,
		Summary:     summary,
		Skills:      skills,
		Seniority:   seniority,
		DesiredRole: role,
		UpdatedAt:   time.Now(),
	}
}
