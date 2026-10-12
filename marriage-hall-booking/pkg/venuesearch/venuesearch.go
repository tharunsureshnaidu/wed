// Package venuesearch is the one definition of "this venue matches what the
// user typed". Shared by /search/venues, its type-ahead, and the /halls and
// /facilities lists, so a name the type-ahead offers is one the search finds.
//
// Built on pg_trgm, not Elasticsearch: at this data size a trigram score over
// the facility row gives typo tolerance and relevance ranking with no second
// store to keep in sync. Every venue SQL expects the table aliased as f.
package venuesearch

import "fmt"

// threshold is the word_similarity a misspelling needs to match. Measured on
// live data: real typos score 0.43-0.83 (palce, bengalru, mysur, mysore ->
// Mysuru) and the closest unrelated venue 0.33.
const threshold = "0.4"

// haystack is what a fuzzy query is scored against. Name and city together,
// so "grand palace bengaluru" scores 1.00 where the name alone gives 0.57.
const haystack = "f.name||' '||COALESCE(f.city,'')"

// Like is true when column col contains the text in parameter p, or nearly
// does. A NULL column never matches.
//
// ponytail: word_similarity is computed per row, no index. Fine at hundreds
// of venues; past ~50k switch to `p <% col` with a GIN gin_trgm_ops index on
// the same expression, and set pg_trgm.word_similarity_threshold to match.
func Like(p, col string) string {
	return fmt.Sprintf("(%[2]s ILIKE '%%'||%[1]s||'%%' OR word_similarity(%[1]s, %[2]s) >= %[3]s)",
		p, col, threshold)
}

// MatchSQL is true when venue f matches the search text in parameter p, or p
// is empty. Name or city, exactly or misspelt; the description by substring
// only, since a trigram score over a paragraph matches nearly anything.
func MatchSQL(p string) string {
	return fmt.Sprintf("(%[1]s = '' OR %[2]s OR f.description ILIKE '%%'||%[1]s||'%%')",
		p, Like(p, haystack))
}

// ScoreSQL ranks matches best-first: 1 for an exact name or city, lower for a
// misspelling, near 0 for a venue matched only by its description.
func ScoreSQL(p string) string {
	return fmt.Sprintf("word_similarity(%s, %s)", p, haystack)
}
