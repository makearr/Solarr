package tv

import "strings"

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
