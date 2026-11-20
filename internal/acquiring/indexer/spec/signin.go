package spec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/platform/keyedlock"
)

// signedInFor is how long a sign-in a sibling search just made counts as done, for sites that set no cookie to look for.
const signedInFor = time.Minute

// refusedFor is how long a refused sign-in is not tried again: a few wrong tries lock the account on most trackers.
const refusedFor = 15 * time.Minute

// signIns serialises the sign-ins to one site, so searches running side by side make one between them.
var signIns keyedlock.Locks

// signInOutcome is what the last sign-in to a site with these settings came to.
type signInOutcome struct {
	at      time.Time
	refusal error
}

// signInMemory remembers the outcomes by site and settings, so new credentials are tried at once.
var signInMemory = struct {
	sync.Mutex
	outcomes map[string]signInOutcome
}{outcomes: map[string]signInOutcome{}}

func (d *Driver) signInKey() string {
	names := make([]string, 0, len(d.settings))
	for name := range d.settings {
		names = append(names, name)
	}
	slices.Sort(names)
	var joined strings.Builder
	for _, name := range names {
		joined.WriteString(name + "\x00" + d.settings[name] + "\x00")
	}
	sum := sha256.Sum256([]byte(joined.String()))
	return d.baseURL + "\x00" + hex.EncodeToString(sum[:8])
}

func recallSignIn(key string) (signInOutcome, bool) {
	signInMemory.Lock()
	defer signInMemory.Unlock()
	outcome, found := signInMemory.outcomes[key]
	return outcome, found
}

func rememberSignIn(key string, outcome signInOutcome, now time.Time) {
	signInMemory.Lock()
	defer signInMemory.Unlock()
	for other, old := range signInMemory.outcomes {
		if now.Sub(old.at) > refusedFor {
			delete(signInMemory.outcomes, other)
		}
	}
	signInMemory.outcomes[key] = outcome
}

// login signs in once for every search waiting on the site: a sign-in a sibling made a moment ago counts, and one
// the site refused is not repeated until refusedFor has passed or the owner changed the settings.
func (d *Driver) login(ctx context.Context) error {
	if d.spec.Login == nil || d.spec.Login.Method == "" || d.spec.Login.Method == "cookie" || d.spec.Login.Method == "none" {
		return nil
	}
	defer signIns.Lock(d.baseURL)()
	key, now := d.signInKey(), d.clock.Now()
	if outcome, found := recallSignIn(key); found {
		switch age := now.Sub(outcome.at); {
		case outcome.refusal != nil && age < refusedFor:
			return outcome.refusal
		case outcome.refusal == nil && age < signedInFor:
			return nil
		}
	}
	err := d.signIn(ctx)
	switch {
	case err == nil:
		rememberSignIn(key, signInOutcome{at: now}, now)
	case errors.Is(err, ErrLoginFailed) && ctx.Err() == nil:
		rememberSignIn(key, signInOutcome{at: now, refusal: err}, now)
	}
	return err
}
