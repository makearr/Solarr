package parsetv

// Info is what a TV release name claims about itself: identity (series, numbering)
// separate from the technical Claims, as in ADR-18.
type Info struct {
	ReleaseTitle string
	SeriesTitle  string
	// AllTitles holds the alternatives of "A / B", "A (B)" and "A AKA B" titles; empty otherwise.
	AllTitles        []string
	TitleWithoutYear string
	Year             int
	SeasonNumber     int
	Seasons          []int
	EpisodeNumbers   []int
	AbsoluteNumbers  []int
	SpecialAbsolutes []float64
	AirDate          string // yyyy-MM-dd, daily shows only
	DailyPart        int
	FullSeason       bool
	PartialSeason    bool
	SeasonPart       int
	MultiSeason      bool
	MiniSeries       bool
	Special          bool
	SplitEpisode     bool
	SeasonExtra      bool
	ReleaseGroup     string
	ReleaseHash      string
	// ReleaseTokens is what follows the numbering, the input of the claim readers.
	ReleaseTokens string
	Claims        Claims
}
