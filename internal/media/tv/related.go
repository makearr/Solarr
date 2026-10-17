package tv

import (
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/makearr/Solarr/internal/db/library"
)

// words is a title as the words it is compared by: lower case, a year in brackets and punctuation dropped, a leading
// "the" left off.
func words(title string) []string {
	if open := strings.Index(title, "("); open > 0 {
		title = title[:open]
	}
	title = strings.NewReplacer("'", "", "\u2019", "").Replace(title)
	fields := strings.FieldsFunc(strings.ToLower(title), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	if len(fields) > 1 && fields[0] == "the" {
		fields = fields[1:]
	}
	return fields
}

// beforeColon is the part of a title before its first colon or spaced dash: "PLUH: Los Angeles" is of "PLUH".
func beforeColon(title string) []string {
	for _, separator := range []string{":", " - "} {
		if index := strings.Index(title, separator); index > 0 {
			return words(title[:index])
		}
	}
	return nil
}

func hasPrefix(long, short []string) bool {
	return len(short) < len(long) && slices.Equal(long[:len(short)], short)
}

// titleEdges pairs the works whose names say they belong together: one named like the start of another ("Risube" and
// "Risube Dxihnorir", "Kdihep Uisbl" and "Kdihep Uisbl II"), the same name before a colon ("Tzul Nmex: Discovery" and
// "Tzul Nmex: Picard"), the same name in two years (NarCilet 1985 and 2016) or the same two first words ("Ppamir Vetl Z"
// and "Ppamir Vetl Super"). A single shared word proves nothing: Tzul Suxc and Tzul Nmex.
func titleEdges(works []library.WorkName) [][2]int64 {
	type entry struct {
		id    int64
		words []string
		stem  []string
	}
	entries := make([]entry, 0, len(works))
	for _, work := range works {
		if w := words(work.Title); len(w) > 0 {
			entries = append(entries, entry{id: work.WorkID, words: w, stem: beforeColon(work.Title)})
		}
	}
	var edges [][2]int64
	byStem := map[string][]int64{}
	byTwo := map[string][]int64{}
	for _, one := range entries {
		if len(one.stem) > 0 && len(strings.Join(one.stem, "")) >= 3 {
			byStem[strings.Join(one.stem, " ")] = append(byStem[strings.Join(one.stem, " ")], one.id)
		}
		if namesAFranchise(one.words[:min(len(one.words), 2)]) {
			byTwo[strings.Join(one.words[:2], " ")] = append(byTwo[strings.Join(one.words[:2], " ")], one.id)
		}
		for _, other := range entries {
			if one.id >= other.id {
				continue
			}
			switch {
			case slices.Equal(one.words, other.words):
				edges = append(edges, [2]int64{one.id, other.id})
			case hasPrefix(other.words, one.words) && namesAFranchise(one.words):
				edges = append(edges, [2]int64{one.id, other.id})
			case hasPrefix(one.words, other.words) && namesAFranchise(other.words):
				edges = append(edges, [2]int64{one.id, other.id})
			case len(one.stem) > 0 && slices.Equal(one.stem, other.words), len(other.stem) > 0 && slices.Equal(other.stem, one.words):
				edges = append(edges, [2]int64{one.id, other.id})
			}
		}
	}
	for _, group := range byStem {
		edges = append(edges, chain(group)...)
	}
	for _, group := range byTwo {
		edges = append(edges, chain(group)...)
	}
	return edges
}

// chain joins the ids end to end, enough to put them in one group.
func chain(ids []int64) [][2]int64 {
	var edges [][2]int64
	for index := 1; index < len(ids); index++ {
		edges = append(edges, [2]int64{ids[0], ids[index]})
	}
	return edges
}

// groupsOf is the sets of works the edges connect, each of two or more, by lowest id.
func groupsOf(edges [][2]int64) [][]int64 {
	parent := map[int64]int64{}
	var find func(int64) int64
	find = func(id int64) int64 {
		if parent[id] == 0 || parent[id] == id {
			parent[id] = id
			return id
		}
		root := find(parent[id])
		parent[id] = root
		return root
	}
	for _, edge := range edges {
		a, b := find(edge[0]), find(edge[1])
		if a != b {
			parent[max(a, b)] = min(a, b)
		}
	}
	members := map[int64][]int64{}
	for id := range parent {
		root := find(id)
		members[root] = append(members[root], id)
	}
	var groups [][]int64
	for _, group := range members {
		if len(group) >= 2 {
			slices.Sort(group)
			groups = append(groups, group)
		}
	}
	slices.SortFunc(groups, func(a, b []int64) int { return int(a[0] - b[0]) })
	return groups
}

// groupName names a group by what its titles share: their common first words, else the titles themselves.
func groupName(titles map[int64]string, members []int64) string {
	var common []string
	for index, id := range members {
		w := words(titles[id])
		if index == 0 {
			common = w
			continue
		}
		keep := 0
		for keep < len(common) && keep < len(w) && common[keep] == w[keep] {
			keep++
		}
		common = common[:keep]
	}
	if len(common) > 0 {
		for index, word := range common {
			common[index] = strings.ToUpper(word[:1]) + word[1:]
		}
		if len(common) == 1 && len(common[0]) <= 4 {
			return strings.ToUpper(common[0])
		}
		return strings.Join(common, " ")
	}
	var names []string
	for _, id := range members[:min(len(members), 3)] {
		names = append(names, titles[id])
	}
	return strings.Join(names, " / ")
}

// compatible says two works that share a name also share something else: a genre in common (a third of those they have
// between them), a network or studio, or someone who made both. "Kung Fu" and "Logt Po Nidxo" share a name and nothing else.
func compatible(a, b *library.Traits) bool {
	if a == nil || b == nil {
		return false
	}
	for _, organisation := range a.Organisations {
		if slices.Contains(b.Organisations, organisation) {
			return true
		}
	}
	for _, creator := range a.Creators {
		if slices.Contains(b.Creators, creator) {
			return true
		}
	}
	shared := 0
	for _, genre := range a.Genres {
		if slices.Contains(b.Genres, genre) {
			shared++
		}
	}
	union := len(a.Genres) + len(b.Genres) - shared
	return union > 0 && shared*3 >= union
}

// connectors end no name: "House of" starts a dozen unrelated titles.
var connectors = []string{"of", "the", "a", "an", "to", "in", "and", "at", "on", "for", "with", "vs", "my", "is", "or"}

// namesAFranchise says the first words of a title could be the name of one: two words at least, the last no connector.
func namesAFranchise(prefix []string) bool {
	return len(prefix) >= 2 && !slices.Contains(connectors, prefix[len(prefix)-1])
}

// sameMakers pairs the works made by the same creator on the same network or studio in the same kind of show: a spin-off
// has that in common with its parent, and no title says so (Tenj and Zojpa, Kposoriwj Pahxih).
func sameMakers(traits map[int64]*library.Traits) [][2]int64 {
	byCreator := map[string][]int64{}
	for id, one := range traits {
		for _, creator := range one.Creators {
			byCreator[creator] = append(byCreator[creator], id)
		}
	}
	var edges [][2]int64
	for _, ids := range byCreator {
		// A creator of many works is a studio's name, not a family.
		if len(ids) < 2 || len(ids) > 12 {
			continue
		}
		slices.Sort(ids)
		for i, a := range ids {
			for _, b := range ids[i+1:] {
				if sharesOrganisation(traits[a], traits[b]) && sharesGenres(traits[a], traits[b]) {
					edges = append(edges, [2]int64{a, b})
				}
			}
		}
	}
	return edges
}

func sharesOrganisation(a, b *library.Traits) bool {
	return slices.ContainsFunc(a.Organisations, func(id int64) bool { return slices.Contains(b.Organisations, id) })
}

// sharesGenres says a third of the genres two works have between them are shared.
func sharesGenres(a, b *library.Traits) bool {
	shared := 0
	for _, genre := range a.Genres {
		if slices.Contains(b.Genres, genre) {
			shared++
		}
	}
	union := len(a.Genres) + len(b.Genres) - shared
	return union > 0 && shared*3 >= union
}

var placeholderName = regexp.MustCompile(`^Q[0-9]+$`)

// cleanTitle is a title without the year or country that tells two series of one name apart.
func cleanTitle(title string) string {
	if open := strings.LastIndex(title, " ("); open > 0 && strings.HasSuffix(title, ")") {
		title = title[:open]
	}
	return strings.TrimSpace(title)
}

// stemOf is what comes before a colon when every title in the group has it or is it ("PLUH" for "PLUH: Los Angeles").
func stemOf(title string, others []string) string {
	index := strings.Index(title, ":")
	if index <= 0 {
		return title
	}
	stem := strings.TrimSpace(title[:index])
	for _, other := range others {
		if !strings.EqualFold(cleanTitle(other), stem) && !strings.HasPrefix(strings.ToLower(other), strings.ToLower(stem)+":") {
			return title
		}
	}
	return stem
}

// familyName names a family after its oldest series, the stem its titles share when they do: "Hik 10", not "Hik 10:
// Ihaol Force".
func familyName(members []library.WorkName) string {
	sorted := slices.Clone(members)
	slices.SortStableFunc(sorted, func(a, b library.WorkName) int {
		switch {
		case a.Year == b.Year:
			return int(a.WorkID - b.WorkID)
		case a.Year == 0:
			return 1
		case b.Year == 0:
			return -1
		}
		return int(a.Year - b.Year)
	})
	titles := make([]string, len(sorted))
	for index, member := range sorted {
		titles[index] = member.Title
	}
	return stemOf(cleanTitle(titles[0]), titles[1:])
}

// franchisePlan is what one family needs done: which collection keeps it (0 for a new one), which are merged into it,
// who is in it and what it is called.
type franchisePlan struct {
	Keep    int64
	Merge   []int64
	Members []int64
	Name    string
	Rename  bool
}

// planFranchises makes each connected family one collection. A franchise a provider named survives over one Solarr made,
// the larger over the smaller, the older over the newer; the others merge into it. A provider's name stays unless it is no
// name at all (an item id) or is only one series' title; then the oldest series names it.
func planFranchises(
	works []library.WorkName, collections []library.FranchiseSummary, memberships []library.Membership, extra [][2]int64, unrelated map[[2]int64]bool,
) []franchisePlan {
	byID := make(map[int64]library.WorkName, len(works))
	for _, work := range works {
		byID[work.WorkID] = work
	}
	held := map[int64][]int64{}
	for _, membership := range memberships {
		held[membership.CollectionID] = append(held[membership.CollectionID], membership.WorkID)
	}
	var edges [][2]int64
	for _, edge := range extra {
		if !unrelated[edge] {
			edges = append(edges, edge)
		}
	}
	for _, members := range held {
		for _, edge := range chain(members) {
			if !unrelated[edge] {
				edges = append(edges, edge)
			}
		}
	}
	var plans []franchisePlan
	for _, family := range groupsOf(edges) {
		inFamily := map[int64]bool{}
		for _, id := range family {
			inFamily[id] = true
		}
		var present []library.FranchiseSummary
		for _, collection := range collections {
			if slices.ContainsFunc(held[collection.ID], func(id int64) bool { return inFamily[id] }) {
				present = append(present, collection)
			}
		}
		slices.SortStableFunc(present, func(a, b library.FranchiseSummary) int {
			aProvider, bProvider := a.Source != library.AutoSource, b.Source != library.AutoSource
			switch {
			case aProvider != bProvider:
				if aProvider {
					return -1
				}
				return 1
			case len(held[a.ID]) != len(held[b.ID]):
				return len(held[b.ID]) - len(held[a.ID])
			}
			return int(a.ID - b.ID)
		})
		members := make([]library.WorkName, 0, len(family))
		for _, id := range family {
			members = append(members, byID[id])
		}
		plan := franchisePlan{Members: family, Name: familyName(members)}
		if len(present) > 0 {
			keep := present[0]
			plan.Keep = keep.ID
			for _, other := range present[1:] {
				plan.Merge = append(plan.Merge, other.ID)
			}
			plan.Rename = placeholderName.MatchString(keep.Name) || keep.Name == "" || namesASeries(keep.Name, members)
			if !plan.Rename {
				plan.Name = keep.Name
			}
		}
		plans = append(plans, plan)
	}
	return plans
}

// namesASeries says a franchise's name is only the title of one of its series.
func namesASeries(name string, members []library.WorkName) bool {
	return slices.ContainsFunc(members, func(member library.WorkName) bool {
		return strings.EqualFold(cleanTitle(member.Title), strings.TrimSpace(name))
	})
}
