package webhook

import "encoding/json"

// SchemaVersion is the payload's schema; changes are additive.
const SchemaVersion = 1

// Instance says which Solarr sent the event.
type Instance struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Library is where the event happened.
type Library struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Kind      string `json:"kind"`
}

// Work is the item the event is about.
type Work struct {
	ID    int64             `json:"id"`
	Title string            `json:"title"`
	Year  int               `json:"year,omitempty"`
	IDs   map[string]string `json:"ids,omitempty"`
}

// Part is one part of a work (an episode, an issue, a track).
type Part struct {
	Label string `json:"label"`
	Title string `json:"title,omitempty"`
}

// Release is what was grabbed.
type Release struct {
	Title    string `json:"title"`
	Indexer  string `json:"indexer,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	Size     int64  `json:"size,omitempty"`
	Quality  string `json:"quality,omitempty"`
}

// File is one file of the event; Path is set only for a webhook that asked for paths.
type File struct {
	Name    string `json:"name"`
	Size    int64  `json:"size,omitempty"`
	Quality string `json:"quality,omitempty"`
	Path    string `json:"path,omitempty"`
}

// Occurrence is one thing that happened, as the code that saw it tells it.
type Occurrence struct {
	Event         Event
	Library       Library
	Adult         bool
	Work          *Work
	Parts         []Part
	Release       *Release
	Files         []File
	PreviousFiles []File
	Message       string
}

// Payload is the JSON body of schema 1; it never holds a secret, a user name or another service's address.
type Payload struct {
	Schema        int      `json:"schema"`
	Event         Event    `json:"event"`
	At            int64    `json:"at"`
	Delivery      int64    `json:"delivery"`
	Instance      Instance `json:"instance"`
	Library       Library  `json:"library"`
	Work          *Work    `json:"work,omitempty"`
	Parts         []Part   `json:"parts,omitempty"`
	Release       *Release `json:"release,omitempty"`
	Files         []File   `json:"files,omitempty"`
	PreviousFiles []File   `json:"previous_files,omitempty"`
	Message       string   `json:"message,omitempty"`
}

// payloadOf builds the payload for one webhook: paths only when it asked for them.
func payloadOf(occurrence Occurrence, instance Instance, at int64, includePaths bool) Payload {
	strip := func(files []File) []File {
		if includePaths {
			return files
		}
		out := make([]File, len(files))
		for index, file := range files {
			file.Path = ""
			out[index] = file
		}
		return out
	}
	return Payload{
		Schema: SchemaVersion, Event: occurrence.Event, At: at, Instance: instance, Library: occurrence.Library, Work: occurrence.Work, Parts: occurrence.Parts,
		Release: occurrence.Release, Files: strip(occurrence.Files), PreviousFiles: strip(occurrence.PreviousFiles), Message: occurrence.Message,
	}
}

// withDelivery sets the delivery id (the outbox row id) on a stored payload.
func withDelivery(stored string, id int64) ([]byte, error) {
	var payload Payload
	if err := json.Unmarshal([]byte(stored), &payload); err != nil {
		return nil, err
	}
	payload.Delivery = id
	return json.Marshal(payload)
}

// Message is the one line chat and push presets show.
func (p Payload) line() string {
	if p.Message != "" {
		return string(p.Event) + ": " + p.Message
	}
	if p.Work != nil {
		return string(p.Event) + ": " + p.Work.Title
	}
	return string(p.Event)
}
