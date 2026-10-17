package tv

import (
	"cmp"
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

// notACharacter are the parts that name no one: hosts, narrators and walk-ons recur across unrelated shows.
var notACharacter = map[string]bool{
	"narrator": true, "host": true, "presenter": true, "various": true, "voice": true, "additional voices": true, "guest": true,
	"judge": true, "contestant": true, "unknown": true, "mom": true, "dad": true, "mother": true, "father": true, "doctor": true,
	"nurse": true, "reporter": true, "announcer": true, "various characters": true, "various voices": true, "news anchor": true,
	"interviewee": true, "panelist": true, "co host": true, "guest host": true, "man": true, "woman": true, "boy": true, "girl": true,
	"kid": true, "waiter": true, "waitress": true, "cop": true, "officer": true, "police officer": true, "guard": true, "driver": true,
	"bartender": true, "receptionist": true, "old man": true, "old woman": true, "teacher": true, "student": true, "computer": true,
	"crowd": true, "singer": true, "dancer": true, "soldier": true, "pilot": true, "scientist": true, "baby": true, "dog": true, "cat": true,
}

var creditNote = regexp.MustCompile(`\((voice|uncredited|archive footage|singing voice)\)`)

// characterNames are the characters a credit names, as compared: "Nightjar / Ada Vale (voice)" is two. A part named after
// the one who plays it is a cameo as themselves, not a character.
func characterNames(character, person string) []string {
	self := strings.Join(words(person), " ")
	var names []string
	for _, piece := range strings.FieldsFunc(creditNote.ReplaceAllString(strings.ToLower(character), ""), func(r rune) bool { return r == '/' || r == ';' }) {
		name := strings.Join(strings.FieldsFunc(piece, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }), " ")
		switch {
		case len(name) < 3, notACharacter[name], strings.HasPrefix(name, "self"), strings.HasPrefix(name, "himself"), strings.HasPrefix(name, "herself"):
		case self != "" && (strings.Contains(self, name) || strings.Contains(name, self)):
		default:
			names = append(names, name)
		}
	}
	return names
}

// characterEdges pairs the series that share characters played by the same people, as a spin-off does and a crossover does
// not; the rules are in documentation/metadata-providers.md ("Shared characters").
func characterEdges(credits []library.CharacterCredit, works []library.WorkName) [][2]int64 {
	byID := make(map[int64]library.WorkName, len(works))
	for _, work := range works {
		byID[work.WorkID] = work
	}
	type role struct {
		person int64
		name   string
	}
	playedIn := map[role]map[int64]library.CharacterCredit{}
	for _, credit := range credits {
		for _, name := range characterNames(credit.Character, credit.Person) {
			key := role{credit.PersonID, name}
			if playedIn[key] == nil {
				playedIn[key] = map[int64]library.CharacterCredit{}
			}
			seen, again := playedIn[key][credit.WorkID]
			if !again || credit.Billing >= 0 && (seen.Billing < 0 || credit.Billing < seen.Billing) {
				seen.Billing = credit.Billing
			}
			seen.Regular = seen.Regular || credit.Regular
			seen.Episodes += credit.Episodes
			playedIn[key][credit.WorkID] = seen
		}
	}
	type tally struct{ both, named int }
	pairs := map[[2]int64]*tally{}
	for key, in := range playedIn {
		// A part in many series is a stock one, not a family's.
		if len(in) < 2 || len(in) > 6 {
			continue
		}
		ids := make([]int64, 0, len(in))
		for id := range in {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for i, a := range ids {
			for _, b := range ids[i+1:] {
				count := pairs[[2]int64{a, b}]
				if count == nil {
					count = &tally{}
					pairs[[2]int64{a, b}] = count
				}
				if in[a].Regular && in[b].Regular {
					count.both++
				}
				if namedAfter(byID[a], byID[b], key.name, in[b]) || namedAfter(byID[b], byID[a], key.name, in[a]) {
					count.named++
				}
			}
		}
	}
	var edges [][2]int64
	for pair, count := range pairs {
		if count.both >= 3 || count.named > 0 {
			edges = append(edges, pair)
		}
	}
	// What share of a work's characters other series have too: nearly all of a crossover show's.
	borrowed := map[int64]float64{}
	characters := map[int64]int{}
	for _, in := range playedIn {
		for id := range in {
			characters[id]++
			if len(in) > 1 {
				borrowed[id]++
			}
		}
	}
	for id, count := range characters {
		borrowed[id] /= float64(count)
	}
	for {
		neighbours := map[int64][]int64{}
		for _, edge := range edges {
			neighbours[edge[0]] = append(neighbours[edge[0]], edge[1])
			neighbours[edge[1]] = append(neighbours[edge[1]], edge[0])
		}
		hubs := map[int64]bool{}
		for id, around := range neighbours {
			if families := familiesWithout(id, around, edges); families >= 3 || families == 2 && borrowed[id] >= 0.5 {
				hubs[id] = true
			}
		}
		if len(hubs) == 0 {
			break
		}
		edges = slices.DeleteFunc(edges, func(edge [2]int64) bool { return hubs[edge[0]] || hubs[edge[1]] })
	}
	slices.SortFunc(edges, func(a, b [2]int64) int { return cmp.Or(cmp.Compare(a[0], b[0]), cmp.Compare(a[1], b[1])) })
	return edges
}

// namedAfter says the newer work is named after a lead of the older one (billed in its first ten) or a character in three
// of its episodes at least: "Young Brannock", "Governor Tandry".
func namedAfter(newer, older library.WorkName, character string, inOlder library.CharacterCredit) bool {
	lead := inOlder.Billing >= 0 && inOlder.Billing < 10
	if !lead && inOlder.Episodes < 3 || newer.Year == 0 || older.Year == 0 || newer.Year <= older.Year {
		return false
	}
	first := strings.Fields(character)[0]
	return slices.Contains(words(newer.Title), first) && !slices.Contains(words(older.Title), first)
}

// familiesWithout counts the groups a work's neighbours fall into when the work itself is taken away.
func familiesWithout(id int64, around []int64, edges [][2]int64) int {
	var rest [][2]int64
	for _, edge := range edges {
		if edge[0] != id && edge[1] != id {
			rest = append(rest, edge)
		}
	}
	groupOf := map[int64]int64{}
	for _, group := range groupsOf(rest) {
		for _, member := range group {
			groupOf[member] = group[0]
		}
	}
	families := map[int64]bool{}
	for _, neighbour := range around {
		if root, grouped := groupOf[neighbour]; grouped {
			families[root] = true
		} else {
			families[neighbour] = true
		}
	}
	return len(families)
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

// olderFirst orders works by first year, those without one last, ties by id.
func olderFirst(a, b library.WorkName) int {
	switch {
	case a.Year == b.Year:
		return cmp.Compare(a.WorkID, b.WorkID)
	case a.Year == 0:
		return 1
	case b.Year == 0:
		return -1
	}
	return cmp.Compare(a.Year, b.Year)
}

// familyName names a family after its oldest series, the stem its titles share when they do: "Hik 10", not "Hik 10:
// Ihaol Force".
func familyName(members []library.WorkName) string {
	sorted := slices.Clone(members)
	slices.SortStableFunc(sorted, olderFirst)
	titles := make([]string, len(sorted))
	for index, member := range sorted {
		titles[index] = member.Title
	}
	return stemOf(cleanTitle(titles[0]), titles[1:])
}

// franchisePlan is what one family needs done: which collection keeps it (0 for a new one), which are merged into it,
// who is in it, who is no longer, and what it is called.
type franchisePlan struct {
	Keep    int64
	Merge   []int64
	Members []int64
	Remove  []int64
	Name    string
	Rename  bool
}

// planFranchises makes each connected family one collection and answers the ones Solarr made whose family is gone; how
// they are kept and named is in documentation/metadata-providers.md ("Every pass works the families out again").
func planFranchises(
	works []library.WorkName, collections []library.FranchiseSummary, memberships []library.Membership, extra [][2]int64, unrelated map[[2]int64]bool,
) ([]franchisePlan, []int64) {
	byID := make(map[int64]library.WorkName, len(works))
	for _, work := range works {
		byID[work.WorkID] = work
	}
	source := make(map[int64]string, len(collections))
	for _, collection := range collections {
		source[collection.ID] = collection.Source
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
	for id, members := range held {
		if source[id] == library.AutoSource {
			continue
		}
		for _, edge := range chain(members) {
			if !unrelated[edge] {
				edges = append(edges, edge)
			}
		}
	}
	families := groupsOf(edges)
	slices.SortStableFunc(families, func(a, b []int64) int { return len(b) - len(a) })
	used := map[int64]bool{}
	var plans []franchisePlan
	for _, family := range families {
		inFamily := map[int64]bool{}
		for _, id := range family {
			inFamily[id] = true
		}
		count := func(id int64) int {
			n := 0
			for _, member := range held[id] {
				if inFamily[member] {
					n++
				}
			}
			return n
		}
		var present []library.FranchiseSummary
		for _, collection := range collections {
			if !used[collection.ID] && count(collection.ID) > 0 {
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
			case count(a.ID) != count(b.ID):
				return count(b.ID) - count(a.ID)
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
			plan.Keep, used[keep.ID] = keep.ID, true
			for _, other := range present[1:] {
				if other.Source != library.AutoSource {
					plan.Merge, used[other.ID] = append(plan.Merge, other.ID), true
				}
			}
			if keep.Source == library.AutoSource {
				for _, member := range held[keep.ID] {
					if !inFamily[member] {
						plan.Remove = append(plan.Remove, member)
					}
				}
			} else if stem, shared := sharedStem(members); shared {
				plan.Name = stem
			} else if !placeholderName.MatchString(keep.Name) && keep.Name != "" && !namesASeries(keep.Name, members) {
				plan.Name = keep.Name
			}
			plan.Rename = plan.Name != keep.Name
		}
		plans = append(plans, plan)
	}
	var drop []int64
	for _, collection := range collections {
		if collection.Source == library.AutoSource && !used[collection.ID] {
			drop = append(drop, collection.ID)
		}
	}
	return plans, drop
}

// sharedStem is the name every title of a family starts with, the oldest series' up to its colon: "Vulcano Park" for
// "Vulcano Park: Camp Lava" and "Vulcano Park: Ember Theory".
func sharedStem(members []library.WorkName) (string, bool) {
	if len(members) < 2 {
		return "", false
	}
	stem := cleanTitle(slices.MinFunc(members, olderFirst).Title)
	if index := strings.Index(stem, ":"); index > 0 {
		stem = strings.TrimSpace(stem[:index])
	}
	want := words(stem)
	for _, member := range members {
		if have := words(member.Title); len(want) == 0 || !slices.Equal(have, want) && !hasPrefix(have, want) {
			return "", false
		}
	}
	return stem, true
}

// namesASeries says a franchise's name is only the title of one of its series.
func namesASeries(name string, members []library.WorkName) bool {
	return slices.ContainsFunc(members, func(member library.WorkName) bool {
		return strings.EqualFold(cleanTitle(member.Title), strings.TrimSpace(name))
	})
}
