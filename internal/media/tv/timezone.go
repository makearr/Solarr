package tv

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/media"
)

// capitalZones is the zone a network of a country most likely airs in, for a show whose air time no provider gave a
// zone for: the country's main zone, which is right for most of it and shown as a guess.
var capitalZones = map[string]string{
	"us": "America/New_York", "usa": "America/New_York", "ca": "America/Toronto", "can": "America/Toronto",
	"mx": "America/Mexico_City", "br": "America/Sao_Paulo", "gb": "Europe/London", "uk": "Europe/London", "gbr": "Europe/London",
	"ie": "Europe/Dublin", "de": "Europe/Berlin", "deu": "Europe/Berlin", "fr": "Europe/Paris", "fra": "Europe/Paris",
	"es": "Europe/Madrid", "esp": "Europe/Madrid", "it": "Europe/Rome", "ita": "Europe/Rome", "nl": "Europe/Amsterdam",
	"se": "Europe/Stockholm", "no": "Europe/Oslo", "dk": "Europe/Copenhagen", "fi": "Europe/Helsinki", "pl": "Europe/Warsaw",
	"ru": "Europe/Moscow", "rus": "Europe/Moscow", "jp": "Asia/Tokyo", "jpn": "Asia/Tokyo", "kr": "Asia/Seoul", "kor": "Asia/Seoul",
	"cn": "Asia/Shanghai", "chn": "Asia/Shanghai", "tw": "Asia/Taipei", "in": "Asia/Kolkata", "th": "Asia/Bangkok",
	"au": "Australia/Sydney", "aus": "Australia/Sydney", "nz": "Pacific/Auckland",
}

// GuessZone answers the zone a country's networks most likely air in, empty when it is not one of the common ones.
func GuessZone(country string) string {
	return capitalZones[strings.ToLower(strings.TrimSpace(country))]
}

// FieldAirTimeZone is the work field a person sets when the providers' zone for a series' air time is wrong.
const FieldAirTimeZone = "air_time_zone"

// zoneChoices are the zones a series' air time can be set in: every zone a network's country is guessed in.
func zoneChoices() []media.Choice {
	var zones []string
	for _, zone := range capitalZones {
		if !slices.Contains(zones, zone) {
			zones = append(zones, zone)
		}
	}
	zones = append(zones, "UTC")
	slices.Sort(zones)
	choices := make([]media.Choice, len(zones))
	for i, zone := range zones {
		choices[i] = media.Choice{Key: zone, Label: strings.ReplaceAll(zone, "_", " ")}
	}
	return choices
}

// applyAirTimeZone makes every dated episode air at the series' local time in zone, a person's word over the
// providers' instants; a series without a known air time, or a zone not known here, is left as the providers said.
func applyAirTimeZone(series *Series, zone string) {
	if zone == "" || series.AirTime == nil {
		return
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return
	}
	series.AirTime.Timezone, series.AirTime.Guessed = zone, false
	for i, episode := range series.Episodes {
		day, err := time.ParseInLocation("2006-01-02", episode.AirDate, location)
		if err != nil {
			continue
		}
		series.Episodes[i].AirUTC = time.Date(day.Year(), day.Month(), day.Day(), series.AirTime.Hour, series.AirTime.Minute, 0, 0, location).UTC()
		series.Episodes[i].AirConfidence = ConfidenceMedium
		series.Episodes[i].AirBasis = fmt.Sprintf("series air time %02d:%02d %s", series.AirTime.Hour, series.AirTime.Minute, zone)
	}
}
