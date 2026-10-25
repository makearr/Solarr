package indexer

// usenetMedia is what a general usenet indexer carries.
var usenetMedia = []string{MediaMusic, MediaTV, MediaMovies, MediaAnime, MediaBooks}

func newznabPreset(id, name, baseURL string) Definition {
	return Definition{
		ID: id, Name: name, Impl: ImplNewznab, Privacy: PrivacyPrivate, Language: "en-US", BaseURLs: []string{baseURL},
		MediaTypes: usenetMedia, Credentials: Credentials{SecretLabel: "API key"},
		Description: name + " is a Newznab usenet indexer that needs an account and its API key.",
	}
}

// NewznabDefinitions are Prowlarr's Newznab and Torznab presets, and the generic entries for any other.
func NewznabDefinitions() []Definition {
	definitions := []Definition{
		{
			ID: "newznab", Name: "Newznab (generic)", Impl: ImplNewznab, Privacy: PrivacyPrivate, Language: "en-US",
			MediaTypes: usenetMedia, Credentials: Credentials{SecretLabel: "API key", Optional: true},
			Description: "Any usenet indexer that speaks the Newznab API, or Prowlarr's own per-indexer address.",
		},
		{
			ID: "torznab", Name: "Torznab (generic)", Impl: ImplTorznab, Privacy: PrivacyPrivate, Language: "en-US",
			MediaTypes: usenetMedia, Credentials: Credentials{SecretLabel: "API key", Optional: true},
			Description: "Any torrent indexer that speaks the Torznab API: Jackett, or Prowlarr's own per-indexer address.",
		},
		{
			ID: "animetosho-usenet", Name: "AnimeTosho (Usenet)", Impl: ImplNewznab, Privacy: PrivacyPublic, Language: "en-US",
			BaseURLs: []string{"https://feed.animetosho.org"}, MediaTypes: []string{MediaAnime},
			Credentials: Credentials{SecretLabel: "API key", Optional: true},
			Description: "AnimeTosho mirrors anime releases to usenet; its Newznab feed needs no account.",
		},
		{
			ID: "animetosho-torrent", Name: "AnimeTosho (Torrent)", Impl: ImplTorznab, Privacy: PrivacyPublic, Language: "en-US",
			BaseURLs: []string{"https://feed.animetosho.org"}, MediaTypes: []string{MediaAnime},
			Credentials: Credentials{SecretLabel: "API key", Optional: true},
			Description: "AnimeTosho's torrent feed of anime releases, as Torznab; it needs no account.",
		},
	}
	for _, preset := range []struct{ id, name, url string }{
		{"abnzb", "abNZB", "https://abnzb.com"},
		{"althub", "altHUB", "https://api.althub.co.za"},
		{"dognzb", "DOGnzb", "https://api.dognzb.cr"},
		{"drunkenslug", "DrunkenSlug", "https://drunkenslug.com"},
		{"gingadaddy", "GingaDADDY", "https://www.gingadaddy.com"},
		{"miatrix", "Miatrix", "https://www.miatrix.com"},
		{"newzcomplex", "Newz-Complex", "https://newz-complex.org/www"},
		{"ninjacentral", "NinjaCentral", "https://ninjacentral.co.za"},
		{"nzbsu", "Nzb.su", "https://api.nzb.su"},
		{"nzbcat", "NZBCat", "https://nzb.cat"},
		{"nzbfinder", "NZBFinder", "https://nzbfinder.ws"},
		{"nzbgeek", "NZBgeek", "https://api.nzbgeek.info"},
		{"nzbndx", "NZBNDX", "https://www.nzbndx.com"},
		{"nzbplanet", "NzbPlanet", "https://api.nzbplanet.net"},
		{"nzbstars", "NZBStars", "https://nzbstars.com"},
		{"oznzb", "OZnzb", "https://api.oznzb.com"},
		{"simplynzbs", "SimplyNZBs", "https://simplynzbs.com"},
		{"spotnzb", "SpotNZB", "https://spotnzb.xyz"},
		{"tabularasa", "Tabula Rasa", "https://www.tabula-rasa.pw/api/v1"},
		{"usenetcrawler", "Usenet Crawler", "https://www.usenet-crawler.com"},
	} {
		definitions = append(definitions, newznabPreset(preset.id, preset.name, preset.url))
	}
	return definitions
}
