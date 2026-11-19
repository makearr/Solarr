package apiserver

import (
	"fmt"
	"unicode/utf8"
)

// Password bounds: short enough to guess is refused, and so is one so long that hashing it is a cheap way to tie up the server.
const (
	minPasswordRunes = 8
	maxPasswordBytes = 1024
)

// validPassword answers what is wrong with a new password, "" when nothing is. Existing passwords are not re-checked.
func validPassword(password string) string {
	switch {
	case password == "":
		return "a password is required"
	case utf8.RuneCountInString(password) < minPasswordRunes:
		return fmt.Sprintf("a password is at least %d characters", minPasswordRunes)
	case len(password) > maxPasswordBytes:
		return fmt.Sprintf("a password is at most %d bytes", maxPasswordBytes)
	}
	return ""
}
