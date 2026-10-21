// Package apiserver serves the HTTP API (Invariant 22).
package apiserver

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
)

// problemContentType is RFC 7807's media type.
const problemContentType = "application/problem+json"

// Problem is an RFC 7807 error body. Type is a stable slug rather than a
// resolvable URL, so a client can branch on it without following a link.
type Problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// Problem type slugs. These are part of the API contract: renaming one breaks
// clients that switch on it.
const (
	TypeNotFound     = "not-found"
	TypeForbidden    = "forbidden"
	TypeConflict     = "conflict"
	TypeBadRequest   = "bad-request"
	TypeUnavailable  = "unavailable"
	TypeInternal     = "internal"
	TypeUnauthorized = "unauthorized"
	// TypeTooManyRequests is a caller that must wait before trying again.
	TypeTooManyRequests = "too-many-requests"
	// A wrong verb is not a malformed request.
	TypeMethodNotAllowed = "method-not-allowed"
)

// writeProblem renders an error as RFC 7807.
func writeProblem(w http.ResponseWriter, logger *slog.Logger, status int, slug, title, detail string) {
	w.Header().Set("Content-Type", problemContentType)
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(Problem{
		Type: slug, Title: title, Status: status, Detail: detail,
	}); err != nil {
		// The status line is already written, so there is nothing left to tell
		// the client. Logging is all that remains.
		logger.Warn("could not write problem response", slog.Any("error", err))
	}
}

// writeError maps a domain error onto a status (Invariant 37).
func writeError(w http.ResponseWriter, logger *slog.Logger, err error) {
	switch {
	case errors.Is(err, authz.ErrNotVisible), errors.Is(err, settings.ErrNotFound),
		errors.Is(err, jobs.ErrNotFound), errors.Is(err, jobs.ErrUnknownTask):
		writeProblem(w, logger, http.StatusNotFound, TypeNotFound, "Not found", err.Error())

	case errors.Is(err, ErrNotBuiltForMediaType):
		writeProblem(w, logger, http.StatusConflict, TypeConflict, "Not available yet", err.Error())

	case errors.Is(err, ErrInvalidChoice):
		writeProblem(w, logger, http.StatusBadRequest, TypeBadRequest, "Bad request", err.Error())

	case errors.Is(err, ErrFollowSourceMissing):
		writeProblem(w, logger, http.StatusConflict, TypeConflict, "Cannot follow yet", err.Error())

	case errors.Is(err, ErrSeriesNotFound), errors.Is(err, ErrCandidateNotFound):
		writeProblem(w, logger, http.StatusNotFound, TypeNotFound, "Not found", err.Error())

	case errors.Is(err, ErrSeriesSourceUnavailable):
		writeProblem(w, logger, http.StatusBadGateway, TypeUnavailable, "Metadata sources unavailable", err.Error())

	case errors.Is(err, jobs.ErrAlreadyRunning):
		writeProblem(w, logger, http.StatusConflict, TypeConflict, "Already running", err.Error())

	case errors.Is(err, authz.ErrInsufficientPermission):
		writeProblem(w, logger, http.StatusForbidden, TypeForbidden, "Forbidden", err.Error())

	case errors.Is(err, ErrQueryTooShort), errors.Is(err, ErrDiscoverUnsupportedMediaType),
		errors.Is(err, jobs.ErrNoFullMode):
		writeProblem(w, logger, http.StatusBadRequest, TypeBadRequest, "Bad request", err.Error())

	case errors.Is(err, settings.ErrInUse):
		writeProblem(w, logger, http.StatusConflict, TypeConflict, "Still in use", err.Error())

	case errors.Is(err, ErrNothingToDrain):
		writeProblem(w, logger, http.StatusConflict, TypeConflict, "Nothing to drain", err.Error())

	case errors.Is(err, ErrNothingToMove):
		writeProblem(w, logger, http.StatusConflict, TypeConflict, "Nothing to move", err.Error())

	case errors.Is(err, ErrRootNotInWorkLibrary):
		writeProblem(w, logger, http.StatusBadRequest, TypeBadRequest, "Bad request", err.Error())

	case errors.Is(err, authz.ErrNoAccess):
		// A route that skipped its middleware. It is a bug, not a client error,
		// and the client learns nothing about it.
		logger.Error("request reached a handler with no access context", slog.Any("error", err))
		writeProblem(w, logger, http.StatusInternalServerError, TypeInternal,
			"Internal error", "the request could not be authorised")

	case errors.Is(err, settings.ErrSettingNotRegistered):
		// A write path used a key ADR-26's registry has never heard of --
		// always a bug in the write path itself, never something the caller did.
		logger.Error("write path used an unregistered setting key", slog.Any("error", err))
		writeProblem(w, logger, http.StatusInternalServerError, TypeInternal,
			"Internal error", "this setting is not available yet")

	default:
		logger.Error("unhandled error", slog.Any("error", err))
		writeProblem(w, logger, http.StatusInternalServerError, TypeInternal,
			"Internal error", "the request could not be completed")
	}
}

// writeJSON renders a successful response.
func writeJSON(w http.ResponseWriter, logger *slog.Logger, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		logger.Warn("could not write response", slog.Any("error", err))
	}
}
