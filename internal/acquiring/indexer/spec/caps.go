package spec

import (
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
)

// newznabIDs are the standard category names a spec files releases under.
var newznabIDs = map[string]int{
	"Console": 1000, "Console/NDS": 1010, "Console/PSP": 1020, "Console/Wii": 1030, "Console/XBox": 1040, "Console/XBox 360": 1050,
	"Console/Wiiware": 1060, "Console/XBox 360 DLC": 1070, "Console/PS3": 1080, "Console/Other": 1090, "Console/3DS": 1110,
	"Console/PS Vita": 1120, "Console/WiiU": 1130, "Console/XBox One": 1140, "Console/PS4": 1180,
	"Movies": 2000, "Movies/Foreign": 2010, "Movies/Other": 2020, "Movies/SD": 2030, "Movies/HD": 2040, "Movies/UHD": 2045,
	"Movies/BluRay": 2050, "Movies/3D": 2060, "Movies/DVD": 2070, "Movies/WEB-DL": 2080,
	"Audio": 3000, "Audio/MP3": 3010, "Audio/Video": 3020, "Audio/Audiobook": 3030, "Audio/Lossless": 3040, "Audio/Other": 3050,
	"Audio/Foreign": 3060,
	"PC":            4000, "PC/0day": 4010, "PC/ISO": 4020, "PC/Mac": 4030, "PC/Mobile-Other": 4040, "PC/Games": 4050, "PC/Mobile-iOS": 4060,
	"PC/Mobile-Android": 4070,
	"TV":                5000, "TV/WEB-DL": 5010, "TV/Foreign": 5020, "TV/SD": 5030, "TV/HD": 5040, "TV/UHD": 5045, "TV/Other": 5050,
	"TV/Sport": 5060, "TV/Anime": 5070, "TV/Documentary": 5080,
	"XXX": 6000, "XXX/DVD": 6010, "XXX/WMV": 6020, "XXX/XviD": 6030, "XXX/x264": 6040, "XXX/UHD": 6045, "XXX/Pack": 6050,
	"XXX/ImageSet": 6060, "XXX/Packs": 6050, "XXX/SD": 6080, "XXX/WEB-DL": 6090, "XXX/Other": 6070,
	"Books": 7000, "Books/Mags": 7010, "Books/EBook": 7020, "Books/Comics": 7030, "Books/Technical": 7040, "Books/Other": 7050,
	"Books/Foreign": 7060,
	"Other":         8000, "Other/Misc": 8010, "Other/Hashed": 8020,
}

// customBase is where categories a site names itself start, as Newznab leaves 100000 and up to indexers.
const customBase = 100000

// categoryID is the Newznab id of a mapping: its standard name's, or customBase plus the site's own number.
func (m CategoryMapping) categoryID() int {
	if id, ok := newznabIDs[m.Cat]; ok {
		return id
	}
	site, _ := strconv.Atoi(m.ID)
	return customBase + site
}

// Categories answers the spec's categories as caps list them: parents with their children, then the site's own.
func (s Spec) Categories() []indexer.Category {
	parents := map[int]*indexer.Category{}
	var order []int
	var custom []indexer.Category
	seen := map[int]bool{}
	for _, mapping := range s.Caps.Categories {
		id := mapping.categoryID()
		if seen[id] {
			continue
		}
		seen[id] = true
		name := mapping.Cat
		if id >= customBase {
			custom = append(custom, indexer.Category{ID: id, Name: mapping.Description})
			continue
		}
		parent := id / 1000 * 1000
		if parents[parent] == nil {
			parents[parent] = &indexer.Category{ID: parent, Name: strings.SplitN(name, "/", 2)[0]}
			order = append(order, parent)
		}
		if id != parent {
			parents[parent].Subcategories = append(parents[parent].Subcategories, indexer.Category{ID: id, Name: name})
		}
	}
	slices.Sort(order)
	result := make([]indexer.Category, 0, len(order)+len(custom))
	for _, parent := range order {
		result = append(result, *parents[parent])
	}
	return append(result, custom...)
}

// siteCategories answers the site's own category ids for the Newznab ids asked for; a parent id takes its children.
func (s Spec) siteCategories(wanted []int) []string {
	var ids []string
	for _, mapping := range s.Caps.Categories {
		id := mapping.categoryID()
		for _, one := range wanted {
			if one == id || (one%1000 == 0 && id/1000*1000 == one && id < customBase) {
				ids = append(ids, mapping.ID)
				break
			}
		}
	}
	return slices.Compact(ids)
}

// categoryOf answers the Newznab id of a site category id or description, 8000 (other) when the site's is not mapped.
func (s Spec) categoryOf(site string) (int, bool) {
	for _, mapping := range s.Caps.Categories {
		if mapping.ID == site || (site != "" && strings.EqualFold(mapping.Description, site)) {
			return mapping.categoryID(), true
		}
	}
	return 0, false
}

// SearchModes answers the caps search functions the spec's modes give, with the parameters each takes.
func (s Spec) SearchModes() map[string]indexer.SearchFunction {
	functions := map[string]indexer.SearchFunction{}
	for mode, params := range s.Caps.Modes {
		functions[mode] = indexer.SearchFunction{Available: true, SupportedParams: params}
	}
	if _, ok := functions["search"]; !ok {
		functions["search"] = indexer.SearchFunction{Available: true, SupportedParams: []string{"q"}}
	}
	return functions
}

// NewznabID is the Newznab id a mapping files its releases under.
func (m CategoryMapping) NewznabID() int { return m.categoryID() }
