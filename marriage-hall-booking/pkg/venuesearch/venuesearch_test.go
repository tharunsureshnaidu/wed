package venuesearch

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// Read-only: matches against an inline VALUES list, never the facilities table.
// search_vector is built inline the way migration 062 builds it.
func TestMatchAndRank(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run")
	}
	ctx := context.Background()
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams[PrefilterGUC] = PrefilterThreshold
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	run := func(match, q string) string {
		t.Helper()
		// As in the callers: rank only when there is text to rank by.
		order := "f.name"
		if q != "" {
			order = ScoreSQL("$1") + " DESC, f.name"
		}
		rows, err := conn.Query(ctx, `
			SELECT f.name FROM (
			  SELECT v.*, setweight(to_tsvector('english', name), 'A')
			           || setweight(to_tsvector('english', city), 'B')
			           || setweight(to_tsvector('english', type||' venue'), 'C')
			           || setweight(to_tsvector('english', amenities), 'C')
			           || setweight(to_tsvector('english', COALESCE(description, '')), 'D') AS search_vector
			  FROM (VALUES
			    ('Grand Palace Hall', 'Bengaluru', 'marriage hall', 'Banquet for 800', 'Parking Catering'),
			    ('Lake View Resort', 'Mysuru', 'hotel', NULL, 'Swimming Pool'),
			    ('Sunrise Convention', 'Bengaluru', 'marriage hall', 'Poolside lawn; hall for 300', '')
			  ) v(name, city, type, description, amenities)
			) f
			WHERE `+match+`
			ORDER BY `+order, q)
		if err != nil {
			t.Fatal(err)
		}
		got, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(got, ",")
	}

	strict := map[string]string{
		"":       "Grand Palace Hall,Lake View Resort,Sunrise Convention",
		"palace": "Grand Palace Hall",
		"palce":  "Grand Palace Hall", // typo
		"mysur":  "Lake View Resort",  // city typo
		// Every word must match: the other Bengaluru venue has no "grand".
		"grand palace bengaluru":  "Grand Palace Hall",
		"grnd palce":              "Grand Palace Hall", // a typo in every word
		"palace mysuru":           "",                  // one word carried the rest before
		"poolside":                "Sunrise Convention",
		"conven":                  "Sunrise Convention",                   // prefix, still typing
		"hall with parking":       "Grand Palace Hall",                    // amenity; "with" ignored
		"pool":                    "Lake View Resort,Sunrise Convention",  // amenity, description prefix
		"hall":                    "Grand Palace Hall,Sunrise Convention", // exact name first
		"hotel mysuru":            "Lake View Resort",                     // by type, not name
		"wedding venue bengaluru": "",                                     // "wedding" is in no row
		"Grand & Palace!":         "Grand Palace Hall",                    // punctuation is not tsquery syntax
		"xyzzy":                   "",
		"bangalore palace":        "Grand Palace Hall", // old city name
		"mysore":                  "Lake View Resort",
		"mandapam bengaluru":      "Sunrise Convention,Grand Palace Hall", // synonym of hall; "convention" is in its name
		"kalyana mandapam":        "Sunrise Convention,Grand Palace Hall",
		"banquet mysuru":          "", // a synonym is still a word to match
	}
	for q, want := range strict {
		c := Clean(q)
		if g := run(MatchSQL("$1", c), c); g != want {
			t.Errorf("strict q=%q: got [%s], want [%s]", q, g, want)
		}
	}

	// The fallback a caller tries when nothing matched every word: each venue
	// matching part of the phrase - the palace, and the venue in Mysuru.
	if g := run(LooseSQL("$1"), Clean("palace mysuru")); g != "Grand Palace Hall,Lake View Resort" {
		t.Errorf("loose: got [%s]", g)
	}
}

func TestClean(t *testing.T) {
	for in, want := range map[string]string{
		"  Grand   PALACE ":        "grand palace",
		"palace, palace & palace!": "palace",
		"o'neil's":                 "o neil s",
		"a b c d e f g h i j":      "a b c d e f g h",
		"ಗ್ರ್ಯಾಂಡ್ ಪ್ಯಾಲೇಸ್": "ಗ್ರ್ಯಾಂಡ್ ಪ್ಯಾಲೇಸ್",
		"&|!:*()":   "",
		"Bangalore": "bengaluru",
		// One synonym set is one word, so "function hall" is not asked twice.
		"function hall": "function|hall|banquet|mandapam|choultry|convention|kalyana",
		"AC hall":       "ac|conditioning|conditioned|airconditioned hall|banquet|mandapam|choultry|convention|kalyana|function",
	} {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
	if got := City(" Bangalore "); got != "bengaluru" {
		t.Errorf("City(Bangalore) = %q", got)
	}
	if got := City("Hyderabad"); got != "hyderabad" {
		t.Errorf("City(Hyderabad) = %q", got)
	}
}
