package adult

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/metadata/stashbox"
	"github.com/makearr/Solarr/internal/metadata/wikidata"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// PerformersJobType is the registered type of the sweep that reads performers' details.
const PerformersJobType jobs.Type = "adult.performers"

const (
	// performersPerRun bounds one sweep so a large library is spread over the day's runs.
	performersPerRun = 40
	// detailsRefresh is how often a performer's details are read again.
	detailsRefresh = 90 * 24 * time.Hour
	// maxGallery bounds the pictures kept of one performer.
	maxGallery = 12
)

// PerformersDefinition declares the sweep: background metadata work, retried on transient failure.
func PerformersDefinition() jobs.Definition {
	return jobs.Definition{Name: "Reading performers", Type: PerformersJobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 3}
}

// EnqueuePerformers submits one sweep, one at a time (Invariant 16).
func EnqueuePerformers(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: PerformersJobType, ActionKey: string(PerformersJobType), Priority: jobs.PriorityLow, Payload: []byte("{}")})
}

// PerformerStore is the people store as the sweep needs it.
type PerformerStore interface {
	DueForDetails(ctx context.Context, mediaType, role string, before time.Time, limit int) ([]int64, error)
	ExternalIDFor(ctx context.Context, personID int64, provider string) (string, bool, error)
	BackfillDetails(ctx context.Context, personID int64, identity library.PersonIdentity) error
	SetExtras(ctx context.Context, personID int64, extras library.PersonExtras) error
	SetBiography(ctx context.Context, personID int64, biography string) error
	RecordRefresh(ctx context.Context, personID, at int64) error
}

// Biographies is Wikidata as the sweep needs it: the Wikipedia lead of an item the servers link a performer to.
type Biographies interface {
	Biography(ctx context.Context, qid string) (wikidata.Biography, error)
}

// Performers reads the performers of the library's scenes from the servers that know them and fills their page: name,
// gender, country, birth, aliases, picture, and the facts, links and gallery of a Stash or Whisparr performer page.
type Performers struct {
	Service Service
	People  PerformerStore
	// Wikidata, when set, supplies a biography for a performer a server links to an item; nil leaves them without.
	Wikidata Biographies
	Clock    clock.Clock
	Logger   *slog.Logger
}

// serverOrder is the order a performer's servers are asked in: the one Stash itself is built around first.
var serverOrder = []string{ProviderStashDB, ProviderThePornDB, ProviderFansDB}

// Run reads the performers that are due; one failing never stops the rest.
func (p Performers) Run(ctx context.Context, _ jobs.Job) error {
	now := p.Clock.Now()
	due, err := p.People.DueForDetails(ctx, MediaType, RolePerformer, now.Add(-detailsRefresh), performersPerRun)
	if err != nil {
		return fmt.Errorf("adult: performers sweep: %w", err)
	}
	read := 0
	for _, personID := range due {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := p.read(ctx, personID); err != nil {
			p.Logger.Warn("performer not read", slog.Int64("person_id", personID), slog.Any("error", err))
		} else {
			read++
		}
		// Recorded either way: a performer no server knows is asked again in a quarter, not every run.
		if err := p.People.RecordRefresh(ctx, personID, now.Unix()); err != nil {
			return err
		}
	}
	p.Logger.Info("adult performers sweep finished", slog.Int("due", len(due)), slog.Int("read", read))
	return nil
}

func (p Performers) read(ctx context.Context, personID int64) error {
	var identity library.PersonIdentity
	var extras library.PersonExtras
	known := false
	qid := ""
	for _, name := range serverOrder {
		server, ok := p.Service.Servers[name]
		if !ok || server == nil || !server.Configured() {
			continue
		}
		id, held, err := p.People.ExternalIDFor(ctx, personID, performerProvider(name))
		if err != nil || !held {
			continue
		}
		performer, err := server.FindPerformer(ctx, id)
		if err != nil {
			if !errors.Is(err, stashbox.ErrNotFound) {
				p.Service.warn(name, id, err)
			}
			continue
		}
		more, moreExtras := PerformerDetails(performer)
		if qid == "" {
			qid = wikidataID(performer.URLs)
		}
		if !known {
			identity, extras, known = more, moreExtras, true
			continue
		}
		identity, extras = mergePerformer(identity, extras, more, moreExtras)
	}
	if !known {
		return errors.New("no server knows the performer")
	}
	if err := p.People.BackfillDetails(ctx, personID, identity); err != nil {
		return err
	}
	if err := p.People.SetExtras(ctx, personID, extras); err != nil {
		return err
	}
	return p.biography(ctx, personID, qid)
}

// biography stores the Wikipedia lead of the performer's Wikidata item, or its one-line description where there is no article.
func (p Performers) biography(ctx context.Context, personID int64, qid string) error {
	if p.Wikidata == nil || qid == "" {
		return nil
	}
	found, err := p.Wikidata.Biography(ctx, qid)
	if err != nil {
		p.Service.warn("wikidata", qid, err)
		return nil
	}
	text := strings.TrimSpace(found.Extract)
	if text == "" {
		text = strings.TrimSpace(found.Description)
	}
	if text == "" {
		return nil
	}
	return p.People.SetBiography(ctx, personID, text)
}

// wikidataID is the item a server links a performer to: "https://www.wikidata.org/wiki/Q123" is Q123. A link is known by
// its address, since ThePornDB types none.
func wikidataID(links []stashbox.Link) string {
	for _, link := range links {
		if _, id, found := strings.Cut(link.URL, "wikidata.org/wiki/"); found && strings.HasPrefix(id, "Q") {
			return strings.TrimRight(id, "/")
		}
	}
	return ""
}

// PerformerDetails turns a server's performer into the person columns and the extras of the page.
func PerformerDetails(in stashbox.Performer) (library.PersonIdentity, library.PersonExtras) {
	identity := library.PersonIdentity{
		Name: strings.TrimSpace(in.Name), Disambiguation: in.Disambiguation, Kind: "Performer", Gender: genderLabel(in.Gender),
		Country: in.Country, Born: in.BirthDate, Died: in.DeathDate, Aliases: slices.Clone(in.Aliases),
	}
	extras := library.PersonExtras{}
	fact := func(label, value string) {
		if value = strings.TrimSpace(value); value != "" {
			extras.Facts = append(extras.Facts, library.PersonFact{Label: label, Value: value})
		}
	}
	fact("Ethnicity", enumLabel(in.Ethnicity))
	fact("Hair colour", enumLabel(in.HairColor))
	fact("Eye colour", enumLabel(in.EyeColor))
	if in.Height > 0 {
		fact("Height", strconv.Itoa(in.Height)+" cm")
	}
	fact("Measurements", measurements(in))
	fact("Breasts", enumLabel(in.BreastType))
	fact("Career", career(in.CareerStartYear, in.CareerEndYear))
	fact("Tattoos", modifications(in.Tattoos))
	fact("Piercings", modifications(in.Piercings))
	if in.SceneCount > 0 {
		fact("Scenes on the server", strconv.Itoa(in.SceneCount))
	}
	extras.Attributes = library.PersonAttributes{
		Nationality: in.Country, HeightCm: in.Height, Measurements: measurements(in), HairColor: enumLabel(in.HairColor),
		Ethnicity: enumLabel(in.Ethnicity), Tattoos: modifications(in.Tattoos), Piercings: modifications(in.Piercings),
		Status: performerStatus(in),
	}
	for _, link := range in.URLs {
		extras.Links = append(extras.Links, library.PersonWebLink{Label: linkLabel(link), URL: link.URL})
	}
	for _, image := range in.Images {
		if len(extras.Images) < maxGallery {
			extras.Images = append(extras.Images, image.URL)
		}
	}
	if len(extras.Images) > 0 {
		identity.ImageURL = extras.Images[0]
	}
	return identity, extras
}

// performerStatus is deceased with a death date, inactive once the career has an end, else active.
func performerStatus(in stashbox.Performer) string {
	switch {
	case in.DeathDate != "":
		return library.PersonDeceased
	case in.CareerEndYear > 0:
		return library.PersonInactive
	}
	return library.PersonActive
}

// mergePerformer keeps the first server's values and adds what a later one has that the first lacks.
func mergePerformer(identity library.PersonIdentity, extras library.PersonExtras, more library.PersonIdentity, moreExtras library.PersonExtras) (library.PersonIdentity, library.PersonExtras) {
	for _, pair := range []struct {
		into *string
		from string
	}{
		{&identity.Gender, more.Gender}, {&identity.Country, more.Country}, {&identity.Born, more.Born}, {&identity.Died, more.Died},
		{&identity.ImageURL, more.ImageURL}, {&identity.Disambiguation, more.Disambiguation},
	} {
		if *pair.into == "" {
			*pair.into = pair.from
		}
	}
	identity.Aliases = unionNames(identity.Aliases, more.Aliases, 40)
	haveLabel := map[string]bool{}
	for _, fact := range extras.Facts {
		haveLabel[fact.Label] = true
	}
	for _, fact := range moreExtras.Facts {
		if !haveLabel[fact.Label] {
			extras.Facts = append(extras.Facts, fact)
		}
	}
	haveURL := map[string]bool{}
	for _, link := range extras.Links {
		haveURL[link.URL] = true
	}
	for _, link := range moreExtras.Links {
		if !haveURL[link.URL] {
			extras.Links = append(extras.Links, link)
		}
	}
	for _, image := range moreExtras.Images {
		if len(extras.Images) < maxGallery && !slices.Contains(extras.Images, image) {
			extras.Images = append(extras.Images, image)
		}
	}
	extras.Attributes = moreExtras.Attributes.Over(extras.Attributes)
	return identity, extras
}

func genderLabel(gender string) string {
	switch gender {
	case "TRANSGENDER_FEMALE":
		return "Trans woman"
	case "TRANSGENDER_MALE":
		return "Trans man"
	case "NON_BINARY":
		return "Non-binary"
	case "UNKNOWN", "":
		return ""
	}
	return enumLabel(gender)
}

// enumLabel is a server's enum as a person reads it: "CAUCASIAN" is "Caucasian", "NOT_SURE" is "Not sure".
func enumLabel(value string) string {
	value = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "_", " "))
	if value == "" || value == "unknown" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func measurements(in stashbox.Performer) string {
	if in.BandSize == 0 && in.WaistSize == 0 && in.HipSize == 0 {
		return ""
	}
	bust := ""
	if in.BandSize > 0 {
		bust = strconv.Itoa(in.BandSize) + in.CupSize
	}
	parts := []string{bust, sizeOrDash(in.WaistSize), sizeOrDash(in.HipSize)}
	return strings.Join(parts, "-")
}

func sizeOrDash(size int) string {
	if size == 0 {
		return "?"
	}
	return strconv.Itoa(size)
}

func career(start, end int) string {
	switch {
	case start == 0 && end == 0:
		return ""
	case end == 0:
		return strconv.Itoa(start) + " - present"
	case start == 0:
		return "until " + strconv.Itoa(end)
	}
	return strconv.Itoa(start) + " - " + strconv.Itoa(end)
}

func modifications(list []stashbox.BodyModification) string {
	var parts []string
	for _, one := range list {
		text := strings.TrimSpace(one.Location)
		if description := strings.TrimSpace(one.Description); description != "" {
			text += ": " + description
		}
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "; ")
}

// linkLabel names a link by what the server says it is, else by its host.
func linkLabel(link stashbox.Link) string {
	if label := enumLabel(link.Type); label != "" {
		if label == "Onlyfans" {
			return "OnlyFans"
		}
		return label
	}
	if parsed, err := url.Parse(link.URL); err == nil && parsed.Host != "" {
		return strings.TrimPrefix(parsed.Host, "www.")
	}
	return "Link"
}
