package tmdb

// Keys TMDB's ids are stored under. TMDB numbers movies, series, people, networks and companies apart, so two kinds kept
// in one table need two keys: a movie and a series may both be 1234.
const (
	KeySeries  = "tmdb"
	KeyMovie   = "tmdb:movie"
	KeyPerson  = "tmdb"
	KeyCompany = "tmdb:company"
)
