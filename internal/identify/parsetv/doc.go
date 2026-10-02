// Package parsetv turns a TV release or file name into season, episode, absolute and
// air-date claims (ADR-18, stage 8 T6). The pattern table is Sonarr's, which needs
// lookaround and backreferences, hence regexp2 rather than the standard regexp.
package parsetv
