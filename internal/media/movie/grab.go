package movie

import "github.com/makearr/Solarr/internal/media/works"

// Grab is Radarr's automatic search, the shared one over the movie Search: search one movie, then send its best
// accepted release to a download client.
type Grab = works.Grab

// GrabResult is one automatic search: what was found and, when something was, what was sent.
type GrabResult = works.GrabResult
