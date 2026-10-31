// Package stashbox reads a stash-box GraphQL server (stashapp/stash-box): StashDB, FansDB and ThePornDB share the schema
// and are one client with an endpoint, which is how Stash itself and Whisparr's import lists reach them. Each needs the
// owner's own API key.
package stashbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// ErrNoKey means no key is stored; ErrNotFound that the server knows nothing under that id.
var (
	ErrNoKey    = errors.New("stashbox: no API key")
	ErrNotFound = errors.New("stashbox: not found")
)

// The servers a provider is configured for.
const (
	StashDBEndpoint    = "https://stashdb.org/graphql"
	FansDBEndpoint     = "https://fansdb.cc/graphql"
	ThePornDBEndpoint  = "https://theporndb.net/graphql"
	maxResponseBytes   = 8 << 20
	defaultSearchLimit = 10
)

// Fingerprint algorithms a server matches files by.
const (
	AlgorithmOSHash = "OSHASH"
	AlgorithmPHash  = "PHASH"
	AlgorithmMD5    = "MD5"
)

// Doer is the one method of httpx.Client this package needs.
type Doer interface {
	Do(ctx context.Context, request *http.Request) (*http.Response, error)
}

// Client asks one stash-box server with the owner's key.
type Client struct {
	http     Doer
	key      func() string
	endpoint string
}

// New builds a client of the server at endpoint; key answers the owner's key, empty while there is none.
func New(client Doer, endpoint string, key func() string) Client {
	return Client{http: client, key: key, endpoint: endpoint}
}

// Configured says a key is stored.
func (c Client) Configured() bool { return c.key != nil && strings.TrimSpace(c.key()) != "" }

// Image is one picture with its size, 0 where the server does not say.
type Image struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// Link is one address of a scene, studio or performer: a store page, a home page, a social profile.
type Link struct {
	URL  string `json:"url"`
	Type string `json:"type"`
}

// Fingerprint is a hash of a scene's file; Duration is in seconds.
type Fingerprint struct {
	Hash      string `json:"hash"`
	Algorithm string `json:"algorithm"`
	Duration  int    `json:"duration"`
}

// Tag is one of the server's descriptive tags.
type Tag struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Studio is a site or label; Parent is the network it belongs to.
type Studio struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Parent *Studio `json:"parent"`
	URLs   []Link  `json:"urls"`
	Images []Image `json:"images"`
}

// BodyModification is a tattoo or piercing and where it is.
type BodyModification struct {
	Location    string `json:"location"`
	Description string `json:"description"`
}

// Performer is a person as the server describes them; the scenes they appear in are asked for separately.
type Performer struct {
	ID              string             `json:"id"`
	Name            string             `json:"name"`
	Disambiguation  string             `json:"disambiguation"`
	Aliases         []string           `json:"aliases"`
	Gender          string             `json:"gender"`
	BirthDate       string             `json:"birth_date"`
	DeathDate       string             `json:"death_date"`
	Ethnicity       string             `json:"ethnicity"`
	Country         string             `json:"country"`
	EyeColor        string             `json:"eye_color"`
	HairColor       string             `json:"hair_color"`
	Height          int                `json:"height"`
	CupSize         string             `json:"cup_size"`
	BandSize        int                `json:"band_size"`
	WaistSize       int                `json:"waist_size"`
	HipSize         int                `json:"hip_size"`
	BreastType      string             `json:"breast_type"`
	CareerStartYear int                `json:"career_start_year"`
	CareerEndYear   int                `json:"career_end_year"`
	Tattoos         []BodyModification `json:"tattoos"`
	Piercings       []BodyModification `json:"piercings"`
	URLs            []Link             `json:"urls"`
	Images          []Image            `json:"images"`
	SceneCount      int                `json:"scene_count"`
	MergedIntoID    string             `json:"merged_into_id"`
}

// Appearance is a performer in a scene, as the name they were credited under (empty when it is their own).
type Appearance struct {
	As        string    `json:"as"`
	Performer Performer `json:"performer"`
}

// Scene is one scene, or a film where the server holds one; Duration is in seconds.
type Scene struct {
	ID           string        `json:"id"`
	Title        string        `json:"title"`
	Details      string        `json:"details"`
	ReleaseDate  string        `json:"release_date"`
	Duration     int           `json:"duration"`
	Director     string        `json:"director"`
	Code         string        `json:"code"`
	URLs         []Link        `json:"urls"`
	Studio       *Studio       `json:"studio"`
	Tags         []Tag         `json:"tags"`
	Images       []Image       `json:"images"`
	Performers   []Appearance  `json:"performers"`
	Fingerprints []Fingerprint `json:"fingerprints"`
}

// sceneFields is what every scene query selects.
const sceneFields = `id title details release_date duration director code urls{url type}
	studio{id name parent{id name} urls{url type} images{url width height}}
	tags{id name} images{url width height}
	performers{as performer{id name disambiguation gender aliases images{url width height}}}
	fingerprints{hash algorithm duration}`

// performerFields is what every performer query selects.
const performerFields = `id name disambiguation aliases gender birth_date death_date ethnicity country eye_color hair_color
	height cup_size band_size waist_size hip_size breast_type career_start_year career_end_year
	tattoos{location description} piercings{location description} urls{url type} images{url width height}
	scene_count merged_into_id`

func (c Client) query(ctx context.Context, query string, variables map[string]any, into any) error {
	if !c.Configured() {
		return ErrNoKey
	}
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("ApiKey", strings.TrimSpace(c.key()))
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(ctx, request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("stashbox: status %d", response.StatusCode)
	}
	raw, err := httpx.ReadLimited(response.Body, maxResponseBytes)
	if err != nil {
		return err
	}
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("stashbox: decode: %w", err)
	}
	if len(envelope.Errors) > 0 {
		return fmt.Errorf("stashbox: %s", envelope.Errors[0].Message)
	}
	return json.Unmarshal(envelope.Data, into)
}

// SearchScenes finds scenes by words of their title, studio or performers.
func (c Client) SearchScenes(ctx context.Context, term string, limit int) ([]Scene, error) {
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	var found struct {
		Scenes []Scene `json:"searchScene"`
	}
	err := c.query(ctx, `query ($t: String!, $n: Int!) { searchScene(term: $t, limit: $n) {`+sceneFields+`} }`,
		map[string]any{"t": strings.TrimSpace(term), "n": limit}, &found)
	return found.Scenes, err
}

// FindScene reads one scene by id.
func (c Client) FindScene(ctx context.Context, id string) (Scene, error) {
	var found struct {
		Scene *Scene `json:"findScene"`
	}
	if err := c.query(ctx, `query ($id: ID!) { findScene(id: $id) {`+sceneFields+`} }`, map[string]any{"id": id}, &found); err != nil {
		return Scene{}, err
	}
	if found.Scene == nil {
		return Scene{}, ErrNotFound
	}
	return *found.Scene, nil
}

// ScenesByFingerprint finds the scenes a file's hashes name: one result list per file, in the order asked.
func (c Client) ScenesByFingerprint(ctx context.Context, files [][]Fingerprint) ([][]Scene, error) {
	type hash struct {
		Hash      string `json:"hash"`
		Algorithm string `json:"algorithm"`
	}
	asked := make([][]hash, len(files))
	for i, file := range files {
		for _, fingerprint := range file {
			asked[i] = append(asked[i], hash{Hash: fingerprint.Hash, Algorithm: fingerprint.Algorithm})
		}
	}
	var found struct {
		Scenes [][]Scene `json:"findScenesBySceneFingerprints"`
	}
	err := c.query(ctx, `query ($f: [[FingerprintQueryInput!]!]!) { findScenesBySceneFingerprints(fingerprints: $f) {`+sceneFields+`} }`,
		map[string]any{"f": asked}, &found)
	return found.Scenes, err
}

// SearchPerformers finds performers by name or alias.
func (c Client) SearchPerformers(ctx context.Context, term string, limit int) ([]Performer, error) {
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	var found struct {
		Performers []Performer `json:"searchPerformer"`
	}
	err := c.query(ctx, `query ($t: String!, $n: Int!) { searchPerformer(term: $t, limit: $n) {`+performerFields+`} }`,
		map[string]any{"t": strings.TrimSpace(term), "n": limit}, &found)
	return found.Performers, err
}

// FindPerformer reads one performer by id.
func (c Client) FindPerformer(ctx context.Context, id string) (Performer, error) {
	var found struct {
		Performer *Performer `json:"findPerformer"`
	}
	if err := c.query(ctx, `query ($id: ID!) { findPerformer(id: $id) {`+performerFields+`} }`, map[string]any{"id": id}, &found); err != nil {
		return Performer{}, err
	}
	if found.Performer == nil {
		return Performer{}, ErrNotFound
	}
	return *found.Performer, nil
}

// ErrUnsupported means the server does not answer that query: ThePornDB takes the scene filter as plain strings.
var ErrUnsupported = errors.New("stashbox: query not supported by this server")

// SceneFilter narrows ScenesOf to a performer's or a studio's scenes.
type SceneFilter struct {
	PerformerID, StudioID string
}

// ScenesOf lists the scenes of a performer or studio, newest first, the page asked for (from 1). ThePornDB's filter is
// shaped otherwise and answers ErrUnsupported.
func (c Client) ScenesOf(ctx context.Context, filter SceneFilter, page, perPage int) ([]Scene, error) {
	if c.endpoint == ThePornDBEndpoint {
		return nil, ErrUnsupported
	}
	input := map[string]any{"sort": "DATE", "direction": "DESC", "page": max(page, 1), "per_page": max(perPage, 1)}
	switch {
	case filter.PerformerID != "":
		input["performers"] = map[string]any{"value": []string{filter.PerformerID}, "modifier": "INCLUDES"}
	case filter.StudioID != "":
		input["studios"] = map[string]any{"value": []string{filter.StudioID}, "modifier": "INCLUDES"}
	default:
		return nil, nil
	}
	var found struct {
		Query struct {
			Scenes []Scene `json:"scenes"`
		} `json:"queryScenes"`
	}
	err := c.query(ctx, `query ($i: SceneQueryInput!) { queryScenes(input: $i) { scenes {`+sceneFields+`} } }`, map[string]any{"i": input}, &found)
	return found.Query.Scenes, err
}
