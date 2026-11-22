package models

import (
	"crypto/rand"
	"math/big"
	"regexp"
	"strings"
	"time"
)

// Allowed sets
var AllowedStatuses = []string{
	"new",
	"contacted",
	"advance_paid",
	"in_progress",
	"preview_ready",
	"delivered",
	"cancelled",
}

var AllowedPackages = []string{
	"starter",
	"business",
	"premium",
}

var AllowedBusinessTypes = []string{
	"Shop",
	"Clinic",
	"Tuition / Coaching",
	"Salon",
	"Restaurant / Cafe",
	"Other",
}

func IsValidStatus(s string) bool {
	for _, a := range AllowedStatuses {
		if a == s {
			return true
		}
	}
	return false
}

func IsValidPackage(p string) bool {
	if p == "" {
		return true // optional in validation if empty
	}
	for _, a := range AllowedPackages {
		if a == p {
			return true
		}
	}
	return false
}

func IsValidBusinessType(b string) bool {
	if b == "" {
		return true // optional in validation if empty
	}
	for _, a := range AllowedBusinessTypes {
		if a == b {
			return true
		}
	}
	return false
}

// GenerateReference generates a unique 6-character unambiguous uppercase alphanumeric code
// prefixed with KB-, e.g. KB-7F3K9A.
// Excludes confusing chars: 0, O, 1, I.
const unambiguousChars = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"

func GenerateReference() string {
	const length = 6
	buf := make([]byte, length)
	charsetLen := big.NewInt(int64(len(unambiguousChars)))
	for i := 0; i < length; i++ {
		idx, err := rand.Int(rand.Reader, charsetLen)
		if err != nil {
			buf[i] = unambiguousChars[i%len(unambiguousChars)]
		} else {
			buf[i] = unambiguousChars[idx.Int64()]
		}
	}
	return "KB-" + string(buf)
}

var nonDigitRegex = regexp.MustCompile(`\D`)

// NormalizePhone strips non-essential spaces. Handles standard Indian formats:
// - 10 digits (9812345678) -> +919812345678
// - 11 digits starting with 0 (09812345678) -> +919812345678
// - 12 digits starting with 91 (919812345678) -> +919812345678
// - Existing with country code (+91 98123 45678, +1 800 555 0199) -> kept normalized with +
func NormalizePhone(input string) (string, bool) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", false
	}

	// Remove all whitespace
	clean := strings.ReplaceAll(trimmed, " ", "")
	clean = strings.ReplaceAll(clean, "\t", "")

	digitsOnly := nonDigitRegex.ReplaceAllString(clean, "")
	if len(digitsOnly) < 10 {
		return "", false
	}

	// If 11 digits starting with 0: strip leading 0, prefix +91
	if len(digitsOnly) == 11 && strings.HasPrefix(digitsOnly, "0") {
		return "+91" + digitsOnly[1:], true
	}

	// If 12 digits starting with 91 and no + prefix: prefix +
	if len(digitsOnly) == 12 && strings.HasPrefix(digitsOnly, "91") && !strings.HasPrefix(clean, "+") {
		return "+" + digitsOnly, true
	}

	// If exactly 10 digits and not prefixed with +, prefix +91
	if len(digitsOnly) == 10 && !strings.HasPrefix(clean, "+") {
		return "+91" + digitsOnly, true
	}

	return clean, true
}

type Order struct {
	ID              int64     `json:"id"`
	Reference       string    `json:"reference"`
	Name            string    `json:"name"`
	BusinessName    string    `json:"businessName"`
	BusinessType    string    `json:"businessType"`
	Package         string    `json:"package"`
	Phone           string    `json:"phone"`
	WhatsApp        string    `json:"whatsapp"`
	Email           *string   `json:"email"`
	ContentLanguage string    `json:"contentLanguage"`
	Message         string    `json:"message"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type Customer struct {
	Name          string    `json:"name"`
	BusinessName  string    `json:"businessName"`
	Phone         string    `json:"phone"`
	WhatsApp      string    `json:"whatsapp"`
	Email         *string   `json:"email"`
	OrdersCount   int       `json:"ordersCount"`
	LastOrderDate time.Time `json:"lastOrderDate"`
}

type Campaign struct {
	ID              int64     `json:"id"`
	Subject         string    `json:"subject"`
	BodyHTML        string    `json:"bodyHtml"`
	RecipientsCount int       `json:"recipientsCount"`
	SentCount       int       `json:"sentCount"`
	FailedCount     int       `json:"failedCount"`
	Status          string    `json:"status"`
	Detail          string    `json:"detail"`
	CreatedAt       time.Time `json:"createdAt"`
}

type CreateOrderRequest struct {
	Name            string  `json:"name"`
	BusinessName    string  `json:"businessName"`
	BusinessType    string  `json:"businessType"`
	Package         string  `json:"package"`
	Phone           string  `json:"phone"`
	WhatsApp        string  `json:"whatsapp"`
	Email           *string `json:"email"`
	ContentLanguage string  `json:"contentLanguage"`
	Message         string  `json:"message"`
	TurnstileToken  string  `json:"turnstileToken"`
}

type UpdateOrderStatusRequest struct {
	Status string `json:"status"`
}

type CreateCampaignRequest struct {
	Subject    string `json:"subject"`
	HTML       string `json:"html"`
	Recipients any    `json:"recipients"` // "all" or []string
}

type AdminLoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type AdminLoginResponse struct {
	Token string `json:"token"`
	Email string `json:"email"`
}
