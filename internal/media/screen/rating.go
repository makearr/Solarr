package screen

import (
	"strconv"
	"strings"
)

// ratingAges are the youngest viewer each known certification admits, across the countries providers fall back to
// when a work has no US one (plan 10-libraries L2). A bare number ("12", "16+", "-16") is read as that age instead.
var ratingAges = map[string]int64{
	// United States, film and television.
	"G": 0, "PG": 7, "PG-13": 13, "R": 17, "NC-17": 18,
	"TV-Y": 0, "TV-Y7": 7, "TV-Y7-FV": 7, "TV-G": 0, "TV-PG": 10, "TV-14": 14, "TV-MA": 17,
	// United Kingdom, Ireland.
	"U": 0, "UC": 0, "12A": 12, "15A": 15, "16": 16,
	// Canada, Australia, New Zealand, Singapore, Philippines.
	"14A": 14, "18A": 18, "A": 18, "M": 15, "MA15+": 15, "MA 15+": 15, "R18+": 18, "R16": 16, "R18": 18, "NC16": 16,
	"M18": 18, "R21": 21, "C": 0, "C8": 8, "T": 13,
	// France, Spain, Brazil, Netherlands, Sweden, Greece.
	"TP": 0, "APTA": 0, "ATP": 0, "L": 0, "AL": 0, "ALL": 0, "BTL": 0, "K-18": 18, "Κ-18": 18,
}

// RatingAge answers the youngest age a certification admits; false when it is no rating ("NR") or one not known.
func RatingAge(rating string) (int64, bool) {
	normalized := strings.ToUpper(strings.TrimSpace(rating))
	if age, known := ratingAges[normalized]; known {
		return age, true
	}
	number := strings.Trim(normalized, "+- ")
	if age, err := strconv.ParseInt(number, 10, 64); err == nil && age >= 0 && age <= 21 {
		return age, true
	}
	return 0, false
}

// AgeOrUnrated is RatingAge as stored: -1 for a certification that admits no age known.
func AgeOrUnrated(rating string) int64 {
	if age, ok := RatingAge(rating); ok {
		return age
	}
	return -1
}
