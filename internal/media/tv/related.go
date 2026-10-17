package tv

import (
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
