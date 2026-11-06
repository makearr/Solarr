package importjob

import (
	"slices"

	importbatch "github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
)

// Library is one library a Best match import may route a unit to (documentation/guide/importing.md).
type Library struct {
	CategoryID int64
	MediaType  string
	// General is a library no rule narrows, neither its kind's rules nor a rating limit: a type's unit goes there first.
	General bool
	// Rank is the library's priority, lower first, which chooses among several of one type.
	Rank int64
}

// routed is one walked unit with the files its library's type takes, where it is placed and what places it.
type routed struct {
	unit inspect.Unit
	// own is the library the unit itself was routed to, recorded on its row; nil follows the session's.
	own       *int64
	category  *int64
	mediaType string
	importer  importbatch.Importer
	// skip is why no library takes the unit; "" for one that is handed to its importer.
	skip string
}

// routeBySession hands every unit to the session's library, or to the unassigned type while it has none.
func (j *Job) routeBySession(categoryID *int64, batch []inspect.Unit) []routed {
	importer, mediaType := j.importerFor(categoryID)
	routes := make([]routed, len(batch))
	for i, unit := range batch {
		routes[i] = routed{unit: narrowFor(importer, unit), category: categoryID, mediaType: mediaType, importer: importer}
	}
	return routes
}

// routeByFit is Best match: each unit goes to a library of the type that fits it best; a tie between types leaves it
// to the unassigned type, as a session without a library, and a unit no type fits is skipped.
func (j *Job) routeByFit(libraries []Library, batch []inspect.Unit) []routed {
	routes := make([]routed, len(batch))
	for i, unit := range batch {
		mediaType, tied := j.bestFit(libraries, unit)
		switch {
		case tied:
			routes[i] = j.routeBySession(nil, []inspect.Unit{unit})[0]
			if routes[i].importer == nil {
				routes[i].skip = importbatch.ReasonNoLibraryFits
			}
		case mediaType == "":
			routes[i] = routed{unit: unit, mediaType: j.unassigned, skip: importbatch.ReasonNoLibraryFits}
		default:
			categoryID := chooseLibrary(libraries, mediaType)
			importer := j.importers[mediaType]
			routes[i] = routed{
				unit: narrowFor(importer, unit), own: &categoryID, category: &categoryID, mediaType: mediaType, importer: importer,
			}
		}
	}
	return routes
}

// bestFit is the media type among the libraries' whose importer fits the unit best, "" when none does; tied says two
// types fit it equally well.
func (j *Job) bestFit(libraries []Library, unit inspect.Unit) (mediaType string, tied bool) {
	var best float64
	var seen []string
	for _, library := range libraries {
		if slices.Contains(seen, library.MediaType) {
			continue
		}
		seen = append(seen, library.MediaType)
		importer := j.importers[library.MediaType]
		fitter, ok := importer.(importbatch.Fitter)
		if !ok {
			continue
		}
		narrowed := narrowFor(importer, unit)
		if len(narrowed.Media) == 0 {
			continue
		}
		fit := fitter.Fit(narrowed)
		switch {
		case fit > best:
			best, mediaType, tied = fit, library.MediaType, false
		case fit == best && fit > 0:
			tied = true
		}
	}
	return mediaType, tied
}

// chooseLibrary is the library of a type a unit goes to: a general one before one its rules narrow, then by priority.
func chooseLibrary(libraries []Library, mediaType string) int64 {
	var chosen *Library
	for i := range libraries {
		candidate := &libraries[i]
		if candidate.MediaType != mediaType {
			continue
		}
		if chosen == nil || preferred(*candidate, *chosen) {
			chosen = candidate
		}
	}
	return chosen.CategoryID
}

func preferred(a, b Library) bool {
	if a.General != b.General {
		return a.General
	}
	if a.Rank != b.Rank {
		return a.Rank < b.Rank
	}
	return a.CategoryID < b.CategoryID
}

// narrowFor is the unit as a walk with the importer's own options would have found it.
func narrowFor(importer importbatch.Importer, unit inspect.Unit) inspect.Unit {
	floor, books := typeWalk(importer)
	return inspect.Narrow(unit, floor, books)
}

// typeWalk is an importer's size floor (0 for the default) and whether it takes ebook files.
func typeWalk(importer importbatch.Importer) (floor int64, books bool) {
	if sized, ok := importer.(importbatch.SizeFloor); ok {
		floor = sized.MediaSizeFloorBytes()
	}
	if takes, ok := importer.(importbatch.BookFiles); ok {
		books = takes.TakesBookFiles()
	}
	return floor, books
}

// loosestWalk is a walk that keeps every file any of the libraries' types, or the unassigned one, would count as media;
// each unit is narrowed to its own type's once routed.
func (j *Job) loosestWalk(libraries []Library) (floor int64, books bool) {
	floor = inspect.DefaultMediaSizeFloorBytes
	consider := func(importer importbatch.Importer) {
		if importer == nil {
			return
		}
		typeFloor, typeBooks := typeWalk(importer)
		if typeFloor <= 0 {
			typeFloor = inspect.DefaultMediaSizeFloorBytes
		}
		floor, books = min(floor, typeFloor), books || typeBooks
	}
	consider(j.importers[j.unassigned])
	for _, library := range libraries {
		consider(j.importers[library.MediaType])
	}
	return floor, books
}
