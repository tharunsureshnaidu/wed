// Package venuesearch is the one definition of "this venue matches what the
// user typed". Shared by /search/venues, its type-ahead, and the /halls and
// /facilities lists, so a name the type-ahead offers is one the search finds.
//
// Built on Postgres, not Elasticsearch: full-text search (facilities.
// search_vector, migration 062) for words in the name, city, type, amenities
// and description, and pg_trgm for typos in the name and city. Both are indexed,
// and there is no second store to keep in sync. Every venue SQL expects the
// table aliased as f.
package venuesearch

import (
	"fmt"
	"strings"
	"unicode"
)

// threshold is the word_similarity a misspelt word needs to match. Measured
// on live data: real typos score 0.40-0.83 (grnd, palce, bengalru, mysur ->
// Mysuru) and the closest unrelated venue 0.33.
const threshold = "0.4"

// PrefilterGUC and PrefilterThreshold are set on every DB session (see
// pkg/database). The indexed `<%` operator reads its threshold from this
// setting, not from the query. It is looser than threshold because it scores
// the whole phrase, which can rank lower than each of its words; the exact
// per-word check runs after it.
//
// ponytail: a venue whose every word matches only as a typo, scattered far
// enough apart that the whole phrase scores under 0.3, misses the prefilter.
// Correcting each word against a dictionary of venue words would close that,
// and is more machinery than it is worth.
const (
	PrefilterGUC       = "pg_trgm.word_similarity_threshold"
	PrefilterThreshold = "0.3"
)

// haystack is what a typo is scored against. Name and city together, so
// "grand palace bengaluru" scores 1.00 where the name alone gives 0.57.
// Migration 062 indexes this exact expression; change both or neither. The
// parentheses matter: <% binds as tightly as ||, so without them
// `p <% f.name||...` parses as `(p <% f.name)||...`.
const haystack = "(f.name||' '||COALESCE(f.city,''))"

// maxWords caps the words in one search, each costing a check per candidate.
const maxWords = 8

// Clean is what callers bind as the search text: lower case, words of letters
// and digits only, single-spaced, deduplicated. It is spliced into a tsquery,
// so punctuation must not survive - "grand & palace" would be a syntax error.
//
// An old city name becomes the stored one (bangalore -> bengaluru), and a
// word with synonyms becomes all of them joined by | with the typed word
// first (mandapam -> mandapam|banquet|...|hall); any one of them matches.
func Clean(text string) string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r)
	})
	seen := map[string]bool{}
	var out []string
	for _, w := range words {
		if c, ok := cities[w]; ok {
			w = c
		}
		key := w
		if set, ok := synonymOf[w]; ok {
			key = set[0] // one key per set: "function hall" is one word, not two
			alts := []string{w}
			for _, o := range set {
				if o != w {
					alts = append(alts, o)
				}
			}
			w = strings.Join(alts, "|")
		}
		if !seen[key] && len(out) < maxWords {
			seen[key] = true
			out = append(out, w)
		}
	}
	return strings.Join(out, " ")
}

// City is the stored spelling of a city filter, lower case: "Bangalore" and
// "bengaluru" both give "bengaluru". Compare against LOWER(f.city).
func City(name string) string {
	c := strings.ToLower(strings.TrimSpace(name))
	if canon, ok := cities[c]; ok {
		return canon
	}
	return c
}

// base is the Clean text in p with each word's synonyms dropped - the words
// as typed, for scoring the phrase as a whole.
func base(p string) string {
	return fmt.Sprintf(`regexp_replace(%s, '\|\S*', '', 'g')`, p)
}

// anyWord is a tsquery matching any word or synonym of the text in p, each as
// a prefix, so "conven" finds a convention centre while it is still typed.
func anyWord(p string) string {
	return fmt.Sprintf("to_tsquery('english', regexp_replace(%[1]s, '[ |]', ':* | ', 'g')||':*')", p)
}

// MatchSQL is true when venue f matches every word of the Clean text in
// parameter p - by its full text (name, city, type, amenities, description;
// stemmed, prefix, any synonym) or as a typo of a word in its name or city.
// Stop words ("with", "near") are ignored. text is the value bound to p: when
// empty the predicate is a constant true, which keeps the indexed branch free
// of an "OR p is empty" that would stop a generic plan from using the index.
//
// Every word must match: matching the phrase as a whole let one word carry
// it, so "palace mysuru" returned a Bengaluru palace and "hall bengaluru"
// every Bengaluru venue. When this finds nothing, callers retry with LooseSQL.
func MatchSQL(p, text string) string {
	if text == "" {
		return "(" + p + " = '')"
	}
	word := "to_tsquery('english', replace(w, '|', ':* | ')||':*')"
	return fmt.Sprintf(`((f.search_vector @@ %[2]s OR %[6]s <%% %[3]s)
		AND NOT EXISTS (SELECT 1 FROM string_to_table(%[1]s, ' ') w
		     WHERE numnode(%[4]s) > 0
		       AND NOT (f.search_vector @@ %[4]s OR EXISTS (
		            SELECT 1 FROM string_to_table(w, '|') alt
		             WHERE word_similarity(alt, %[3]s) >= %[5]s))))`,
		p, anyWord(p), haystack, word, threshold, base(p))
}

// LooseSQL is the fallback when MatchSQL finds nothing: the phrase as a whole
// near the name or city, or inside the description. Not indexed, and it lets
// a match on one word carry the rest - which is why it is only ever a second
// try, never the first.
func LooseSQL(p string) string {
	b := base(p)
	return fmt.Sprintf("(%[1]s = '' OR %[2]s OR f.description ILIKE '%%'||%[1]s||'%%')",
		b, Like(b, haystack))
}

// Like is true when column col contains the text in parameter p, or nearly
// does. A NULL column never matches. Used by the type-ahead, over one column.
func Like(p, col string) string {
	return fmt.Sprintf("(%[2]s ILIKE '%%'||%[1]s||'%%' OR word_similarity(%[1]s, %[2]s) >= %[3]s)",
		p, col, threshold)
}

// ScoreSQL ranks matches best-first: the typo score over name and city, plus
// the full-text rank, which weighs a word in the name above one in the city,
// an amenity, then the description.
func ScoreSQL(p string) string {
	return fmt.Sprintf("(word_similarity(%[1]s, %[2]s) + ts_rank(f.search_vector, %[3]s))",
		base(p), haystack, anyWord(p))
}
