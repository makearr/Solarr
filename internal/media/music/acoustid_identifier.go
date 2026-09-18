package music

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/metadata/acoustid"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// OrderFingerprint puts this last in the chain (ADR-24: cheaper, more reliable
// strategies first). Fingerprinting decodes audio and then makes a network
// call, so it only ever runs for a file nothing else could name.
const OrderFingerprint = 80

// Fingerprinter produces a Chromaprint for a file. mediatool.Tool satisfies it;
// narrowed to an interface so a test does not need fpcalc on PATH.
type Fingerprinter interface {
	Fingerprint(ctx context.Context, paths []string) ([]mediatool.Fingerprint, error)
}

// AcoustIDLookup is the acoustid client, narrowed the same way.
type AcoustIDLookup interface {
	Enabled() bool
	Lookup(ctx context.Context, fingerprint string, duration time.Duration) ([]acoustid.Match, error)
}

// AcoustIDIdentifier is ADR-29's fingerprint rung (D12): fingerprint the audio,
// ask AcoustID what it is, and report the release groups it names as
// candidates. It identifies only -- the ids it returns are resolved through the
// ordered metadata sources like any other candidate, which is why nothing here
// touches a provider.
type AcoustIDIdentifier struct {
	fingerprints Fingerprinter
	lookup       AcoustIDLookup
	absolute     func(relPath string) string
}

// AcoustIDIdentifierConfig is what it needs. Absolute resolves a FileRef's own
// relative path against its root, the same way the other rungs' callers do.
type AcoustIDIdentifierConfig struct {
	Fingerprints Fingerprinter
	Lookup       AcoustIDLookup
	Absolute     func(relPath string) string
}

// NewAcoustIDIdentifier builds it.
func NewAcoustIDIdentifier(config AcoustIDIdentifierConfig) AcoustIDIdentifier {
	return AcoustIDIdentifier{
		fingerprints: config.Fingerprints, lookup: config.Lookup, absolute: config.Absolute,
	}
}

// Name identifies the strategy in a Decision.
func (AcoustIDIdentifier) Name() string { return "acoustid" }

// Order is OrderFingerprint.
func (AcoustIDIdentifier) Order() int { return OrderFingerprint }

// Identify fingerprints the file and asks AcoustID what it is. With no key
// configured it answers nothing rather than an error: the rung is disabled,
// not broken, and a file skipped for that reason is identified normally once a
// key appears (D12).
func (a AcoustIDIdentifier) Identify(ctx context.Context, f model.FileRef) ([]model.Identification, error) {
	if a.lookup == nil || !a.lookup.Enabled() {
		return nil, nil
	}

	path := f.RelPath
	if a.absolute != nil {
		path = a.absolute(f.RelPath)
	}
	prints, err := a.fingerprints.Fingerprint(ctx, []string{path})
	if err != nil {
		return nil, fmt.Errorf("music: fingerprint %s: %w", f.RelPath, err)
	}
	if len(prints) == 0 || prints[0].Err != nil || prints[0].Chromaprint == "" {
		// A file that cannot be fingerprinted is not an identification failure
		// worth stopping the chain for: it simply has no answer from this rung,
		// the same reasoning SearchIdentifier's own swallowed search failure has.
		return nil, nil //nolint:nilerr
	}

	matches, err := a.lookup.Lookup(ctx, prints[0].Chromaprint, prints[0].Duration)
	if err != nil {
		if errors.Is(err, acoustid.ErrNoKey) {
			// The key was cleared between Enabled() and here: the rung is off,
			// not broken, so it answers nothing like any other disabled run.
			return nil, nil //nolint:nilerr
		}
		return nil, fmt.Errorf("music: acoustid lookup %s: %w", f.RelPath, err)
	}

	var found []model.Identification
	for _, match := range matches {
		for _, group := range match.ReleaseGroupMBIDs {
			found = append(found, model.Identification{
				Strategy: a.Name(),
				// AcoustID's own score is the confidence: it says how well the
				// audio matched, which is exactly what this rung is claiming.
				// It is never 1.0 the way an embedded MBID is, because this is
				// a guess with a confidence (ADR-24).
				Confidence: match.Score,
				ExternalID: &model.ExternalID{
					Provider: ProviderReleaseGroup, ExternalID: group, Confidence: match.Score,
				},
			})
		}
	}
	return found, nil
}
