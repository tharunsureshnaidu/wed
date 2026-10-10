package venuesearch

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// Read-only: matches against an inline VALUES list, never the facilities table.
func TestMatchAndRank(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	cases := map[string]string{
		"":       "Grand Palace Hall,Lake View Resort,Sunrise Convention",
		"palace": "Grand Palace Hall",
		"palce":  "Grand Palace Hall", // typo
		"mysur":  "Lake View Resort",  // city typo
		// Name + city ranks first; another venue in the same city follows, and
		// the Mysuru one does not match.
		"grand palace bengaluru": "Grand Palace Hall,Sunrise Convention",
		"poolside":               "Sunrise Convention",                   // description only
		"xyzzy":                  "",                                     // nothing
		"hall":                   "Grand Palace Hall,Sunrise Convention", // exact name first
	}
	for q, want := range cases {
		rows, err := conn.Query(ctx, `
			SELECT f.name FROM (VALUES
			  ('Grand Palace Hall', 'Bengaluru', 'Banquet for 800'),
			  ('Lake View Resort', 'Mysuru', NULL),
			  ('Sunrise Convention', 'Bengaluru', 'Poolside lawn; hall for 300')
			) f(name, city, description)
			WHERE `+MatchSQL("$1")+`
			ORDER BY `+ScoreSQL("$1")+` DESC, f.name`, q)
		if err != nil {
			t.Fatal(err)
		}
		got, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatal(err)
		}
		if g := strings.Join(got, ","); g != want {
			t.Errorf("q=%q: got [%s], want [%s]", q, g, want)
		}
	}
}
