package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/platform/secrets"
)

// calendarTokenPrefix tells a leaked calendar link apart from an API key: it reads one feed only.
const calendarTokenPrefix = "cal_"

// GenerateCalendarToken returns a calendar feed token and the hash to look it up by.
func GenerateCalendarToken() (secrets.Secret, string, error) {
	raw := make([]byte, apiKeyBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("auth: generate calendar token: %w", err)
	}
	token := calendarTokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	return secrets.Secret(token), HashAPIKey(token), nil
}

// LooksLikeCalendarToken rejects an obviously wrong value without a database lookup.
func LooksLikeCalendarToken(candidate string) bool {
	return strings.HasPrefix(candidate, calendarTokenPrefix) && len(candidate) > len(calendarTokenPrefix)+16
}
