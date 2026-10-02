package parsetv

import (
	"strconv"
	"strings"

	"github.com/dlclark/regexp2"
)

// Claims is the technical half of a release name: claims, never facts (ADR-39). A dimension the name does not state stays zero.
type Claims struct {
	Resolution    int    // vertical lines: 360, 480, 540, 576, 720, 1080, 2160
	Source        string // bluray, webdl, webrip, hdtv, dvd, bdrip, brrip, dsr, pdtv, sdtv, tvrip, rawhd
	Remux         bool
	VideoCodec    string // h264, h265, av1, xvid, divx, mpeg2, vp9
	HDR           string // dolby-vision, hdr10plus, hdr10, hdr, hlg
	AudioCodec    string
	AudioChannels string // "5.1", "2.0"
	Service       string // streaming service the release names, upper case
	Version       int    // 1 unless a PROPER, REPACK or vN says otherwise
	Real          int    // how many REAL tags
	Multi         bool
	Languages     []string
}

// HasQuality reports whether the name states a resolution or a source at all.
func (c Claims) HasQuality() bool { return c.Resolution != 0 || c.Source != "" }

var (
	sourceRe = compile(`\b(?:
		(?<bluray>BluRay|Blu-Ray|HD-?DVD|BDMux|BD(?!$))|
		(?<webdl>WEB[-_. ]DL(?:mux)?|WEBDL|AmazonHD|AmazonSD|iTunesHD|MaxdomeHD|NetflixU?HD|WebHD|HBOMaxHD|DisneyHD|[. ]WEB[. ](?:[xh][ .]?26[45]|AVC|HEVC|DDP?5[. ]1)|[. ](?-i:WEB)$|(?:720|1080|2160)p[-. ]WEB[-. ]|[-. ]WEB[-. ](?:720|1080|2160)p|\b\s\/\sWEB\s\/\s\b|(?:AMZN|NF|DP)[. -]WEB[. -](?!Rip))|
		(?<webrip>WebRip|Web-Rip|WEBMux)|
		(?<hdtv>HDTV)|
		(?<bdrip>BDRip|BDLight)|
		(?<brrip>BRRip)|
		(?<dvd>DVD|DVDRip|NTSC|PAL|xvidvd)|
		(?<dsr>WS[-_. ]DSR|DSR)|
		(?<pdtv>PDTV)|
		(?<sdtv>SDTV)|
		(?<tvrip>TVRip)
		)(?:\b|$|[ .])`, regexp2.IgnoreCase|regexp2.IgnorePatternWhitespace)
	sourceNames     = []string{"bluray", "webdl", "webrip", "hdtv", "bdrip", "brrip", "dvd", "dsr", "pdtv", "sdtv", "tvrip"}
	rawHDRe         = compile(`\b(?<rawhd>RawHD|Raw[-_. ]HD)\b`, regexp2.IgnoreCase)
	mpeg2Re         = compile(`\b(?<mpeg2>MPEG[-_. ]?2)\b`, 0)
	properRe        = compile(`\b(?<proper>proper)\b`, regexp2.IgnoreCase)
	repackRe        = compile(`\b(?<repack>repack\d?|rerip\d?)\b`, regexp2.IgnoreCase)
	versionRe       = compile(`\d[-._ ]?v(?<version>\d)[-._ ]|\[v(?<version>\d)\]|repack(?<version>\d)|rerip(?<version>\d)|(?:480|576|720|1080|2160)p[._ ]v(?<version>\d)`, regexp2.IgnoreCase)
	realRe          = compile(`\b(?<real>REAL)\b`, 0)
	resolutionRe    = compile(`\b(?:(?<R360p>360p)|(?<R480p>480p|480i|640x480|848x480)|(?<R540p>540p)|(?<R576p>576p)|(?<R720p>720p|1280x720|960p)|(?<R1080p>1080p|1920x1080|1440p|FHD|1080i|4kto1080p)|(?<R2160p>2160p|3840x2160|4k[-_. ](?:UHD|HEVC|BD|H265)|(?:UHD|HEVC|BD|H265)[-_. ]4k))\b`, regexp2.IgnoreCase)
	altResolutionRe = compile(`\b(?<R2160p>UHD)\b|(?<R2160p>\[4K\])`, regexp2.IgnoreCase)
	resolutionNames = []struct {
		group string
		lines int
	}{{"R360p", 360}, {"R480p", 480}, {"R540p", 540}, {"R576p", 576}, {"R720p", 720}, {"R1080p", 1080}, {"R2160p", 2160}}
	otherSourceRe  = compile(`(?<hdtv>HD[-_. ]TV)|(?<sdtv>SD[-_. ]TV)`, regexp2.IgnoreCase)
	animeBlurayRe  = compile(`bd(?:720|1080|2160)|bluray(?:480|576|720|1080|2160)p|(?<=[-_. (\[])bd(?=[-_. )\]])`, regexp2.IgnoreCase)
	animeWebDLRe   = compile(`\[WEB\]|[\[\(]WEB[ .]`, regexp2.IgnoreCase)
	remuxRe        = compile(`(?:[_. ]|\d{4}p-|\bHybrid-)(?<remux>(?:(BD|UHD)[-_. ]?)?Remux)\b|(?<remux>(?:(BD|UHD)[-_. ]?)?Remux[_. ]\d{4}p)`, regexp2.IgnoreCase)
	highDefPdtvRe  = compile(`hr[-_. ]ws`, regexp2.IgnoreCase)
	videoCodecRe   = compile(`\b(?:(?<h265>x265|h[-_. ]?265|hevc)|(?<h264>x264|h[-_. ]?264|avc)|(?<av1>av1)|(?<vp9>vp9)|(?<xvid>xvid(?:hd)?)|(?<divx>divx)|(?<mpeg2>mpeg[-_. ]?2))\b`, regexp2.IgnoreCase)
	hdrRe          = compile(`\b(?:(?<dv>DV|DoVi|Dolby[-_. ]?Vision)|(?<hdr10plus>HDR10(?:\+|Plus)|HDR10P)|(?<hdr10>HDR10)|(?<hlg>HLG)|(?<hdr>HDR))\b`, regexp2.IgnoreCase)
	audioCodecRe   = compile(`\b(?:(?<truehd>TrueHD)|(?<dtshdma>DTS[-_. ]?HD[-_. ]?MA|DTS[-_. ]?MA)|(?<dtsx>DTS[-_. ]?X)|(?<dts>DTS(?:[-_. ]?HD|[-_. ]?ES)?)|(?<eac3>DDP|DD\+|E[-_. ]?AC[-_. ]?3)|(?<ac3>DD|AC[-_. ]?3)|(?<aac>AAC)|(?<flac>FLAC)|(?<opus>OPUS)|(?<mp3>MP3))`, regexp2.IgnoreCase)
	audioNames     = []string{"truehd", "dtshdma", "dtsx", "dts", "eac3", "ac3", "aac", "flac", "opus", "mp3"}
	audioChannelRe = compile(`(?<![\d.])(?<channels>[1-9])[. ]?(?<lfe>[01])(?![\dp])`, 0)
	serviceRe      = compile(`\b(?<service>AMZN|NF|ATVP|DSNP|HMAX|MAX|HULU|PCOK|PMTP|PMNP|STAN|CRAV|iT|SHO|STZ|ROKU|CC|BBC|iP|ITV|ALL4|MA|VIKI|CR|FUNI|HIDI|TUBI|PLUZ|PLEX|RED|DSCP|APTV|STRP|ATV|NOW|SKST|ALL4|U4|ID|AE|DCU|DSNY|CBS|NBC|ABC|FOX|TVNZ|SBS|9NOW|10P)\b`, 0)
	multiRe        = compile(`[_. ](?<multi>multi)[_. ]`, regexp2.IgnoreCase)
)

// ParseClaims reads what a name says about its encoding, without deciding what is good.
func ParseClaims(name string) Claims {
	normalized := strings.TrimSpace(strings.ReplaceAll(name, "_", " "))
	claims := Claims{Version: 1}
	claims.Multi = isMatch(multiRe, normalized)
	parseRevision(&claims, name, normalized)

	if isMatch(rawHDRe, normalized) {
		claims.Source = "rawhd"
		return finishClaims(claims, normalized)
	}

	claims.Resolution = parseResolution(normalized)
	claims.Remux = isMatch(remuxRe, normalized)
	claims.Source = parseSource(normalized)
	if claims.Source == "" && (claims.Remux || strings.HasSuffix(strings.ToLower(normalized), ".m2ts")) {
		claims.Source = "bluray"
	}
	if claims.Resolution == 0 {
		claims.Resolution = impliedResolution(claims.Source, normalized)
	}
	return finishClaims(claims, normalized)
}

func parseResolution(normalized string) int {
	m := firstMatch(resolutionRe, normalized)
	if m == nil {
		m = firstMatch(altResolutionRe, normalized)
	}
	if m == nil {
		return 0
	}
	for _, entry := range resolutionNames {
		if _, found := groupValue(m, entry.group); found {
			return entry.lines
		}
	}
	return 0
}

func parseSource(normalized string) string {
	var last *regexp2.Match
	for _, m := range allMatches(sourceRe, normalized) {
		last = m
	}
	if last != nil {
		for _, source := range sourceNames {
			if _, found := groupValue(last, source); found {
				if source == "hdtv" && isMatch(mpeg2Re, normalized) {
					return "rawhd"
				}
				return source
			}
		}
	}
	switch {
	case isMatch(animeBlurayRe, normalized):
		return "bluray"
	case isMatch(animeWebDLRe, normalized):
		return "webdl"
	}
	if m := firstMatch(otherSourceRe, normalized); m != nil {
		if _, found := groupValue(m, "hdtv"); found {
			return "hdtv"
		}
		return "sdtv"
	}
	return ""
}

// impliedResolution is what a high-definition source means by itself; only the name's own words count otherwise.
func impliedResolution(source, normalized string) int {
	switch source {
	case "pdtv", "sdtv", "dsr", "tvrip":
		switch {
		case strings.Contains(strings.ToLower(normalized), "1080p"):
			return 1080
		case strings.Contains(strings.ToLower(normalized), "720p"), isMatch(highDefPdtvRe, normalized):
			return 720
		}
	}
	return 0
}

func parseRevision(claims *Claims, name, normalized string) {
	version := 0
	if m := firstMatch(versionRe, normalized); m != nil {
		value, _ := groupValue(m, "version")
		version, _ = strconv.Atoi(value)
		claims.Version = version
	}
	if isMatch(properRe, normalized) || isMatch(repackRe, normalized) {
		if version > 0 {
			claims.Version = version + 1
		} else {
			claims.Version = 2
		}
	}
	claims.Real = len(allMatches(realRe, name))
}

func finishClaims(claims Claims, normalized string) Claims {
	if m := firstMatch(videoCodecRe, normalized); m != nil {
		for _, codec := range []string{"h265", "h264", "av1", "vp9", "xvid", "divx", "mpeg2"} {
			if _, found := groupValue(m, codec); found {
				claims.VideoCodec = codec
				break
			}
		}
	}
	if m := firstMatch(hdrRe, normalized); m != nil {
		for _, entry := range []struct{ group, name string }{{"dv", "dolby-vision"}, {"hdr10plus", "hdr10plus"}, {"hdr10", "hdr10"}, {"hlg", "hlg"}, {"hdr", "hdr"}} {
			if _, found := groupValue(m, entry.group); found {
				claims.HDR = entry.name
				break
			}
		}
	}
	if m := firstMatch(audioCodecRe, normalized); m != nil {
		for _, codec := range audioNames {
			if _, found := groupValue(m, codec); found {
				claims.AudioCodec = codec
				break
			}
		}
	}
	if m := firstMatch(audioChannelRe, normalized); m != nil {
		channels, _ := groupValue(m, "channels")
		lfe, _ := groupValue(m, "lfe")
		claims.AudioChannels = channels + "." + lfe
	}
	if m := firstMatch(serviceRe, normalized); m != nil {
		claims.Service, _ = groupValue(m, "service")
	}
	return claims
}
