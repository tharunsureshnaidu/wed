package main

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// scrapedHall is one row of the leads CSV, already cleaned up.
type scrapedHall struct {
	Name        string
	Category    string
	Description string
	Rating      *float64
	ReviewCount *int
	Phone       string
	Email       string
	Website     string
	Address     string
	City        string
	State       string
	Zipcode     string
	Country     string
	Lat         *float64
	Lng         *float64
	MapsURL     string
	PhotoURLs   []string
}

// scrapedReview is one row of the reviews CSV.
type scrapedReview struct {
	MapsURL      string
	ExternalID   string
	Author       string
	AuthorPhoto  string
	Rating       *int
	Text         string
	RelativeDate string
}

// readCSV returns the rows keyed by header name, so a column added to the
// exporter does not shift every index here.
func readCSV(path string) ([]map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	// Review text contains commas, quotes and newlines; LazyQuotes keeps a stray
	// quote inside a review from aborting the whole import.
	r.LazyQuotes = true
	r.FieldsPerRecord = -1

	head, err := r.Read()
	if err == io.EOF {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for i := range head {
		head[i] = strings.TrimSpace(strings.TrimPrefix(head[i], "\ufeff"))
	}

	var out []map[string]string
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		m := make(map[string]string, len(head))
		for i, h := range head {
			if i < len(rec) {
				m[h] = strings.TrimSpace(rec[i])
			}
		}
		out = append(out, m)
	}
	return out, nil
}

// placeIDRe pulls Google's own place id out of a Maps URL's data blob.
var placeIDRe = regexp.MustCompile(`(?i)!1s(0x[0-9a-f]+:0x[0-9a-f]+)`)

// placeID identifies a venue independently of the URL shape it arrived in.
// Google serves the same place as both ".../Name/@lat,lng,767m/data=!3m2!..."
// and ".../Name/data=!4m7!3m6!...", sometimes with a landmark appended to the
// name in one of them, so the URL itself is not an identity.
func placeID(mapsURL string) string {
	if m := placeIDRe.FindStringSubmatch(mapsURL); m != nil {
		return strings.ToLower(m[1])
	}
	return strings.ToLower(strings.TrimSpace(mapsURL))
}

func parseHalls(rows []map[string]string) []scrapedHall {
	halls := make([]scrapedHall, 0, len(rows))
	// Google returns the same venue under two URL shapes across query passes;
	// without this the same hall is imported twice under slightly different
	// names. Keep the first, which carries the richer scrape.
	seen := make(map[string]bool, len(rows))
	for _, r := range rows {
		name := r["Business Name"]
		maps := r["Google Maps URL"]
		// Without a name there is nothing to list, and without the Maps URL there
		// is no stable key to re-import against. Skip rather than insert junk.
		if name == "" || maps == "" {
			continue
		}
		id := placeID(maps)
		if seen[id] {
			continue
		}
		seen[id] = true

		// A tile search over a rural district also returns settlements and
		// landmarks ("Partawal Bazar") that carry no address and no contact of
		// any kind. Imported venues go live as APPROVED, so these would show up
		// publicly as empty listings. A real venue has at least one of the two.
		if r["Address"] == "" && firstOf(r["Phone"], r["All Extracted Phones"]) == "" &&
			r["Website"] == "" {
			continue
		}
		addr := r["Address"]
		city, state, zip, country := splitAddress(addr)
		h := scrapedHall{
			Name:        name,
			Category:    r["Category"],
			Description: r["Website Description"],
			Rating:      parseFloat(r["Rating"]),
			ReviewCount: parseInt(r["Reviews"]),
			Phone:       firstOf(r["Phone"], r["All Extracted Phones"]),
			Email:       firstOf(r["Email"], r["All Extracted Emails"]),
			Website:     websiteOf(r["Website"]),
			Address:     addr,
			City:        city,
			State:       state,
			Zipcode:     zip,
			Country:     country,
			Lat:         parseFloat(r["Latitude"]),
			Lng:         parseFloat(r["Longitude"]),
			MapsURL:     maps,
			PhotoURLs:   splitList(firstNonEmpty(r["All Photo URLs"], r["Photo URL"])),
		}
		halls = append(halls, h)
	}
	return halls
}

func parseReviews(rows []map[string]string) []scrapedReview {
	out := make([]scrapedReview, 0, len(rows))
	for _, r := range rows {
		id, maps := r["Review ID"], r["Google Maps URL"]
		if id == "" || maps == "" {
			continue
		}
		var rating *int
		if v := parseInt(r["Review Rating"]); v != nil && *v >= 1 && *v <= 5 {
			rating = v
		}
		out = append(out, scrapedReview{
			MapsURL:      maps,
			ExternalID:   id,
			Author:       r["Author"],
			AuthorPhoto:  r["Author Photo"],
			Rating:       rating,
			Text:         r["Review Text"],
			RelativeDate: r["Review Date"],
		})
	}
	return out
}

// zipRe matches the 6-digit Indian PIN that ends a Google Maps address.
var zipRe = regexp.MustCompile(`\b(\d{6})\b`)

// splitAddress pulls city/state/zip out of the single address blob Maps renders,
// e.g. "101, Railway Parallel Rd, Kumarapark West, Bengaluru, Karnataka 560020".
//
// The tail is "<city>, <state> <zip>", so it is read from the right. Anything
// that does not fit that shape leaves the fields empty rather than guessing -
// full_address always keeps the original string either way.
func splitAddress(addr string) (city, state, zip, country string) {
	if addr == "" {
		return "", "", "", ""
	}
	country = "India"
	parts := strings.Split(addr, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	last := parts[len(parts)-1]
	if m := zipRe.FindStringSubmatch(last); m != nil {
		zip = m[1]
		state = strings.TrimSpace(strings.Replace(last, m[1], "", 1))
	} else {
		state = last
	}
	if len(parts) >= 2 {
		city = parts[len(parts)-2]
	}
	// "Bengaluru, Karnataka 560020" leaves state empty when the zip was the whole
	// last field; then the city slot actually held the state.
	if state == "" && city != "" {
		state, city = city, ""
		if len(parts) >= 3 {
			city = parts[len(parts)-3]
		}
	}
	return city, state, zip, country
}

// websiteOf drops the value when Google gave a search URL instead of the
// venue's own site. Maps renders one of these for a venue with no website, and
// they run to several hundred characters of tracking parameters - far past the
// column width, and useless to anyone who clicks it.
func websiteOf(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.Contains(s, "google.com/search") ||
		strings.Contains(s, "google.com/maps") {
		return ""
	}
	if len(s) > 255 {
		return "" // a URL this long is tracking junk, not a homepage
	}
	return s
}

func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] || !strings.HasPrefix(p, "http") {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// firstOf prefers the primary value, falling back to the first of a list.
func firstOf(primary, list string) string {
	if strings.TrimSpace(primary) != "" {
		return strings.TrimSpace(primary)
	}
	for _, p := range strings.Split(list, ",") {
		if p = strings.TrimSpace(p); p != "" {
			return p
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func parseFloat(s string) *float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &v
}

func parseInt(s string) *int {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
	if s == "" {
		return nil
	}
	// Pandas writes an integer column containing blanks as "1544.0".
	if i := strings.IndexByte(s, '.'); i >= 0 {
		s = s[:i]
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return nil
	}
	return &v
}

// normalisePhone reduces a scraped phone to digits so "096118 49049" and
// "+91 96118 49049" are recognised as the same vendor. Returns "" when there is
// nothing usable, which the caller treats as "no phone".
func normalisePhone(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	d := b.String()
	// Indian numbers arrive as 10 digits, 11 with a leading 0, or 12 with 91.
	switch {
	case len(d) == 12 && strings.HasPrefix(d, "91"):
		d = d[2:]
	case len(d) == 11 && strings.HasPrefix(d, "0"):
		d = d[1:]
	}
	if len(d) < 10 {
		return ""
	}
	return d
}

// vendorKey decides which halls belong to the same vendor. Phone first (a venue
// operator answers one number across their halls), then email, then the venue
// name - so a hall with neither still gets its own vendor rather than being
// merged with every other contactless hall.
func vendorKey(h scrapedHall) string {
	if p := normalisePhone(h.Phone); p != "" {
		return "phone:" + p
	}
	if e := strings.ToLower(strings.TrimSpace(h.Email)); e != "" {
		return "email:" + e
	}
	return "name:" + strings.ToLower(h.Name) + "|" + h.MapsURL
}

// placeholderEmail builds a stable, obviously-fake address for a vendor with no
// scraped email. users.email is UNIQUE and the login flow keys on it, so a real
// looking address would collide with, or shadow, a genuine signup.
//
// It must be derived only from the vendor key: a venue with neither phone nor
// email has nothing else for the re-import lookup to match on, so anything
// run-dependent here (a counter, a timestamp) creates a second account for the
// same venue on every run. The .invalid TLD is reserved by RFC 2606 and can
// never be registered or receive mail.
func placeholderEmail(key string) string {
	sum := sha256.Sum256([]byte(key))
	return fmt.Sprintf("imported-%s@invalid.local", hex.EncodeToString(sum[:8]))
}
