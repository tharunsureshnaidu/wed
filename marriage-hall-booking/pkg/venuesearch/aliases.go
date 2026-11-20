package venuesearch

import (
	"slices"
	"sort"
	"strings"
)

// cities maps an old or common spelling to the name venues are stored under,
// so "bangalore" finds venues in Bengaluru. A whole word only: renames score
// too low as typos (banglore -> Bengaluru is 0.11) for the trigram to catch.
var cities = map[string]string{
	"bangalore": "bengaluru", "banglore": "bengaluru", "bengalore": "bengaluru",
	"bangaluru": "bengaluru", "blr": "bengaluru",
	"mysore": "mysuru", "mangalore": "mangaluru", "belgaum": "belagavi",
	"hubli": "hubballi", "shimoga": "shivamogga", "tumkur": "tumakuru",
	"gulbarga": "kalaburagi", "bellary": "ballari", "bijapur": "vijayapura",
	"bombay": "mumbai", "madras": "chennai", "calcutta": "kolkata",
	"gurgaon": "gurugram", "cochin": "kochi", "trivandrum": "thiruvananthapuram",
	"poona": "pune", "baroda": "vadodara", "benares": "varanasi",
	"pondicherry": "puducherry", "vizag": "visakhapatnam",
}

// synonyms are sets of words that stand for one another in a search: a
// customer typing "mandapam" means the same venue an owner described as a
// banquet hall. Single words only - each becomes a tsquery prefix term.
var synonyms = [][]string{
	{"hall", "banquet", "mandapam", "choultry", "convention", "kalyana", "function"},
	{"ac", "conditioning", "conditioned", "airconditioned"},
}

// synonymOf finds a word's set.
var synonymOf = func() map[string][]string {
	m := map[string][]string{}
	for _, set := range synonyms {
		for _, w := range set {
			m[w] = set
		}
	}
	return m
}()

// Typed is the Clean text's words as the user typed them (city names already
// in their stored spelling), each without its synonyms.
func Typed(clean string) []string {
	var out []string
	for _, w := range strings.Fields(clean) {
		out = append(out, strings.SplitN(w, "|", 2)[0])
	}
	return out
}

// HasSynonyms reports whether a typed word stands for a synonym set, so it is
// a word the search knows even if no venue name contains it.
func HasSynonyms(word string) bool {
	_, ok := synonymOf[word]
	return ok
}

// CitiesStartingWith is every stored city name that word begins, directly or
// through an old name: "banga" gives bengaluru, as a type-ahead must while
// "bangalore" is still being typed.
func CitiesStartingWith(word string) []string {
	if len(word) < 3 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for alias, canon := range cities {
		if (strings.HasPrefix(alias, word) || strings.HasPrefix(canon, word)) && !seen[canon] {
			seen[canon] = true
			out = append(out, canon)
		}
	}
	sort.Strings(out)
	return out
}

// Synonym reports whether a and b are the same word or in one synonym set:
// Synonym("mandapam", "hall") is true.
func Synonym(a, b string) bool {
	if a == b {
		return true
	}
	set, ok := synonymOf[a]
	return ok && slices.Contains(set, b)
}
