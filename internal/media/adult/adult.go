// Package adult is the adult media type: scenes and films, as Whisparr holds them. A scene is a work without parts whose
// one wanted item needs its file; performers are its people and the studio its organisation. Metadata is read from
// Whisparr's server and the stash-box servers (StashDB, ThePornDB, FansDB) in a library's order and merged field by field
// (ADR-8). Core sees works and items only (ADR-23).
package adult

import (
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/platform/filekind"
)

// MediaType is the key of the media type and of the category's media_type.
const MediaType = "adult"

// Provider names, as stored in work_external_id and person_external_id.
const (
	ProviderStashDB   = "stashdb"
	ProviderThePornDB = "theporndb"
	ProviderFansDB    = "fansdb"
	// ProviderStudio keeps a studio under its name, so one studio is one organisation in every adult library.
	ProviderStudio = "adult:studio"
	// ProviderPerformerName names a performer a source gave no id for.
	ProviderPerformerName = "adult:performer"
)

// Roles a scene stores, in the credit table's vocabulary.
const (
	RolePerformer = "performer"
	RoleDirector  = "director"
)

// Status is where a scene stands before it is out.
type Status string

// A scene is announced until its release date, then released.
const (
	StatusAnnounced Status = "announced"
	StatusReleased  Status = "released"
)

// IDs are the identifiers one scene carries at the providers; empty means unknown. Whisparr's server answers with
// ThePornDB's ids, so it adds to ThePornDB's.
type IDs struct {
	StashDB   string
	ThePornDB string
	FansDB    string
}

// Empty says no provider id is known.
func (ids IDs) Empty() bool { return ids == IDs{} }

// Credit is one performer's part in a scene. Provider and ExternalID name the performer at the source that supplied
// the credit: the same person has a different id at each server.
type Credit struct {
	Provider, ExternalID string
	Name                 string
	// As is the name the scene credits them under, when it is not their own.
	As     string
	Gender string
	Image  string
	Role   string
	Order  int
}

// Image is one picture of a scene, kept as a "poster" as every work's picture is.
type Image struct {
	Kind   string `json:"kind"`
	URL    string `json:"url"`
	Source string `json:"source,omitempty"`
}

// Scene is one scene or film, merged from every source that knew it. Date is yyyy-mm-dd, or empty.
type Scene struct {
	IDs      IDs
	Title    string
	Overview string
	Date     string
	Year     int
	// DurationSec is the scene's length as the sources give it.
	DurationSec int
	Studio      string
	// StudioID and NetworkID are the studio's and its parent's ids at the first source, StudioProvider the key they are stored under.
	StudioID, NetworkID, StudioProvider string
	// Network is the studio's parent, when it has one.
	Network     string
	StudioLogo  string
	Director    string
	Code        string
	Tags        []string
	URLs        []string
	Images      []Image
	Credits     []Credit
	Fingerprint []Fingerprint
	// Sources names the providers that supplied it, in the order they were read.
	Sources []string
}

// Fingerprint is a hash of the scene's file as a server holds it.
type Fingerprint struct {
	Hash, Algorithm string
	DurationSec     int
}

// Performers are the scene's performers' names, in credit order.
func (s Scene) Performers() []string {
	var names []string
	for _, credit := range s.Credits {
		if credit.Role == RolePerformer {
			names = append(names, credit.Name)
		}
	}
	return names
}

// NameKey is a name as compared: lower case, single spaced, punctuation gone.
func NameKey(name string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r > 127:
			return r
		}
		return ' '
	}, name)
	return strings.Join(strings.Fields(strings.ToLower(cleaned)), " ")
}

// StatusOn says whether the scene is out at now; a scene with no date is wanted at once.
func (s Scene) StatusOn(now time.Time) Status {
	if len(s.Date) < 10 {
		return StatusReleased
	}
	at, err := time.Parse("2006-01-02", s.Date[:10])
	if err != nil || !at.After(now) {
		return StatusReleased
	}
	return StatusAnnounced
}

// IsScene reports a path worth probing as a scene: a video file that is not a sample, trailer or extra.
func IsScene(relPath string) bool { return filekind.IsFeature(relPath) }
