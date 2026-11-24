package transcoder

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
)

// selectionPage bounds one read of the selection (200 rows).
const selectionPage = 200

// ErrBadOverride is a retry setting no encoder takes.
var ErrBadOverride = errors.New("transcoder: the retry settings are not valid")

// Selection is the files a retry touches: the listed ids, or every file matching the filter except the excluded ones (bulk selection is a query).
type Selection struct {
	IDs        []int64
	State      library.TranscodeState
	CategoryID int64
	Kind       string
	Search     string
	Excluded   []int64
}

// Retry gives failed and blocked files in the selection another chance, with the override when there is one, and answers how many it
// touched; with apply false it only counts, by the same walk, so the preview is the applied number.
func (s *Service) Retry(ctx context.Context, selection Selection, override Override, apply bool) (int64, error) {
	encoded, err := encodeOverride(override)
	if err != nil {
		return 0, err
	}
	var touched int64
	retryOne := func(item library.TranscodeItem) error {
		if (item.State != library.TranscodeFailed && item.State != library.TranscodeBlocked) || slices.Contains(selection.Excluded, item.MediaObjectID) {
			return nil
		}
		if !apply {
			touched++
			return nil
		}
		err := s.Items.Transition(ctx, item.MediaObjectID, item.State, library.TranscodeNeeded, func(one *library.TranscodeItem) {
			one.Attempts, one.Pass, one.Interrupted, one.FailureCode, one.LastError, one.OverrideJSON = 0, 0, false, "", "", encoded
		})
		if errors.Is(err, library.ErrTranscodeState) {
			return nil
		}
		if err == nil {
			touched++
		}
		return err
	}
	if len(selection.IDs) > 0 {
		for _, id := range selection.IDs {
			item, found, err := s.Items.Get(ctx, id)
			if err != nil {
				return touched, err
			}
			if found {
				if err := retryOne(item); err != nil {
					return touched, err
				}
			}
		}
		return touched, nil
	}
	states := []library.TranscodeState{library.TranscodeFailed, library.TranscodeBlocked}
	if selection.State != "" {
		states = []library.TranscodeState{selection.State}
	}
	for _, state := range states {
		var after int64
		for {
			page, err := s.Items.List(ctx, library.TranscodeFilter{CategoryID: selection.CategoryID, State: state, Kind: selection.Kind, Search: selection.Search, AfterID: after, PageSize: selectionPage})
			if err != nil {
				return touched, err
			}
			for _, listed := range page {
				if err := retryOne(listed.Item); err != nil {
					return touched, err
				}
				after = listed.Item.MediaObjectID
			}
			if len(page) < selectionPage {
				break
			}
		}
	}
	return touched, nil
}

// encodeOverride validates a person's retry settings and answers the stored form; empty settings are the setup's own.
func encodeOverride(override Override) (string, error) {
	if override == (Override{}) {
		return "", nil
	}
	if override.Quality < 0 || override.VideoBitrateKbps < 0 {
		return "", ErrBadOverride
	}
	if override.Preset != "" && !slices.Contains([]string{"fast", "normal", "slow", "slower", "veryslow"}, override.Preset) {
		return "", ErrBadOverride
	}
	if override.Engine != "" && !slices.Contains([]string{string(settings.EngineAuto), string(settings.EngineSoftware), "qsv", "vaapi", "nvenc"}, override.Engine) {
		return "", ErrBadOverride
	}
	raw, err := json.Marshal(override)
	return string(raw), err
}

// ResumeHealth ends a kind's probing or backoff so the next dispatch tries ordinary files again ("Try now").
func (s *Service) ResumeHealth(ctx context.Context, kind string) error {
	stored, err := s.Items.Health(ctx, kind)
	if err != nil {
		return err
	}
	return s.Items.SaveHealth(ctx, storedHealth(kind, Health{Mode: ModeOK}, stored.RunSeq, ""))
}
