// Package releasefacts turns what a video release name claims into the facts a profile or a target judges, before
// anything is downloaded (ADR-39: a name's claims stand in for measured facts until the probe replaces them). TV and
// movies read a release the same way.
package releasefacts

import (
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/parsetv"
	"github.com/makearr/Solarr/internal/identify/score"
)

// FromRelease stands a release name's claims in for measured facts.
func FromRelease(claims parsetv.Claims, releaseGroup string, raw indexer.RawItem, impl string) score.Facts {
	facts := score.Facts{
		Release: score.ReleaseFacts{Title: raw.Title, ReleaseGroup: releaseGroup, Seeders: raw.Seeders, Leechers: raw.Peers, Protocol: judge.ProtocolFor(impl)},
		File: score.FileFacts{
			Source: Source(claims), VideoCodec: claims.VideoCodec, HDR: claims.HDR, AudioCodec: claims.AudioCodec,
			AudioChannels: claims.AudioChannels, AudioLanguages: claims.Languages, ReleaseGroup: releaseGroup, ProperRepack: claims.Version > 1,
		},
	}
	if claims.Resolution > 0 {
		facts.File.Resolution = strconv.Itoa(claims.Resolution) + "p"
	}
	return facts
}

// Source is the source a profile ranks: a remux is its own, a rip is the disc or the broadcast it came from.
func Source(claims parsetv.Claims) string {
	if claims.Remux {
		return "remux"
	}
	switch claims.Source {
	case "bdrip", "brrip":
		return "bluray"
	case "pdtv", "sdtv", "dsr", "tvrip":
		return "hdtv"
	}
	return claims.Source
}

// videoShare is the part of a file's bits the picture takes; the sound is the rest. A guess, as the size is the
// only fact before download, and good enough to rank a bloated release above a starved one.
const videoShare = 0.92

// EstimateBitrate stands the release's size over its length in for the bitrate the probe will measure; items is how
// many episodes (or movies) the size covers.
func EstimateBitrate(facts *score.Facts, sizeBytes int64, items int, runtimeMs int64) {
	if sizeBytes <= 0 || items <= 0 || runtimeMs <= 0 {
		return
	}
	overall := float64(sizeBytes) * 8 / (float64(items) * float64(runtimeMs) / 1000)
	facts.File.OverallBitrate = int64(overall)
	facts.File.VideoBitrate = int64(overall * videoShare)
}

// QualityTitle is Sonarr's and Radarr's "WEBDL-1080p": the source and the resolution a name claims.
func QualityTitle(claims parsetv.Claims) string {
	source := map[string]string{"webdl": "WEBDL", "webrip": "WEBRip", "bluray": "Bluray", "bdrip": "Bluray", "brrip": "Bluray", "hdtv": "HDTV", "dvd": "DVD", "sdtv": "SDTV", "pdtv": "SDTV", "dsr": "SDTV", "tvrip": "SDTV", "rawhd": "Raw-HD"}[claims.Source]
	if claims.Remux {
		source = "Bluray"
	}
	switch {
	case source == "":
		return ""
	case claims.Resolution == 0:
		return source
	case claims.Remux:
		return source + "-" + strconv.Itoa(claims.Resolution) + "p Remux"
	}
	return source + "-" + strconv.Itoa(claims.Resolution) + "p"
}

// FromFile is what the probe measured of a library file, as the facts a profile or a target judges.
func FromFile(file library.EpisodeFile) score.Facts {
	return score.Facts{File: score.FileFacts{
		Resolution: file.Resolution, VideoCodec: file.VideoCodec, HDR: file.HDR, AudioCodec: file.AudioCodec,
		AudioChannels: file.AudioChannels, Container: file.Container, AudioLanguages: file.Languages, SubtitleLanguages: file.Subtitles,
		FrameRate: file.FrameRate, VideoBitrate: file.VideoBitrate, AudioBitrate: file.AudioBitrate, CRF: file.CRF,
	}}
}

// CleanQuery drops the punctuation indexers' text search chokes on.
func CleanQuery(title string) string {
	var out strings.Builder
	for _, r := range strings.ReplaceAll(title, "&", " and ") {
		switch {
		case r == '\'' || r == '’':
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r > 127:
			out.WriteRune(r)
		default:
			out.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(out.String()), " ")
}
