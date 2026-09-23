package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSplitAddress(t *testing.T) {
	// Real address shapes from the scraper's own database.
	cases := []struct {
		addr, city, state, zip string
	}{
		{"101, Railway Parallel Rd, Kumarapark West, Seshadripuram, Bengaluru, Karnataka 560020",
			"Bengaluru", "Karnataka", "560020"},
		{"24, JW Marriott Bengaluru, 1, Vittal Mallya Rd, Ashok Nagar, Bengaluru, Karnataka 560001",
			"Bengaluru", "Karnataka", "560001"},
		// No PIN at the end.
		{"Some Rd, Mysuru, Karnataka", "Mysuru", "Karnataka", ""},
		// Nothing parseable: full_address still keeps the original.
		{"Bengaluru", "", "Bengaluru", ""},
		{"", "", "", ""},
	}
	for _, c := range cases {
		city, state, zip, country := splitAddress(c.addr)
		if city != c.city || state != c.state || zip != c.zip {
			t.Errorf("splitAddress(%q) = (%q,%q,%q), want (%q,%q,%q)",
				c.addr, city, state, zip, c.city, c.state, c.zip)
		}
		if c.addr != "" && country != "India" {
			t.Errorf("splitAddress(%q) country = %q", c.addr, country)
		}
	}
}

func TestNormalisePhoneAndVendorKey(t *testing.T) {
	// The same venue phone written three ways must collapse to one vendor.
	for _, s := range []string{"096118 49049", "+91 96118 49049", "9611849049"} {
		if got := normalisePhone(s); got != "9611849049" {
			t.Errorf("normalisePhone(%q) = %q, want 9611849049", s, got)
		}
	}
	if got := normalisePhone("12345"); got != "" {
		t.Errorf("short number should be rejected, got %q", got)
	}

	a := scrapedHall{Name: "Hall A", Phone: "096118 49049", MapsURL: "u1"}
	b := scrapedHall{Name: "Hall B", Phone: "+91 96118 49049", MapsURL: "u2"}
	if vendorKey(a) != vendorKey(b) {
		t.Error("same phone must map to the same vendor")
	}
	// No phone and no email: each venue gets its own vendor, never a shared one.
	c := scrapedHall{Name: "Hall C", MapsURL: "u3"}
	d := scrapedHall{Name: "Hall D", MapsURL: "u4"}
	if vendorKey(c) == vendorKey(d) {
		t.Error("contactless venues must not share a vendor")
	}
}

func TestParseCSV(t *testing.T) {
	dir := t.TempDir()
	leads := filepath.Join(dir, "leads.csv")
	// Row 2 has a quoted comma and an embedded newline; row 3 has no Maps URL
	// and must be skipped rather than imported as a keyless venue.
	os.WriteFile(leads, []byte(
		"Business Name,Rating,Reviews,Phone,Email,Address,Latitude,Longitude,Google Maps URL,All Photo URLs,Website Description\n"+
			"Sapthapadi Hall,4.4,1544,096118 49049,,\"Mysuru, Karnataka 570001\",12.33,76.61,https://maps/A,\"http://p/1,http://p/2\",A hall\n"+
			"Sindhoor Hall,4.2,1624.0,,x@y.com,\"Mysuru, Karnataka\",,,https://maps/B,,\"Big hall,\nwith space\"\n"+
			"No URL Hall,3.0,5,,,,,,,,\n"), 0o644)

	rows, err := readCSV(leads)
	if err != nil {
		t.Fatal(err)
	}
	halls := parseHalls(rows)
	if len(halls) != 2 {
		t.Fatalf("got %d halls, want 2 (the row without a Maps URL is skipped)", len(halls))
	}

	a := halls[0]
	if a.Name != "Sapthapadi Hall" || a.City != "Mysuru" || a.Zipcode != "570001" {
		t.Errorf("bad hall A: %+v", a)
	}
	if a.Rating == nil || *a.Rating != 4.4 {
		t.Errorf("rating = %v", a.Rating)
	}
	if a.ReviewCount == nil || *a.ReviewCount != 1544 {
		t.Errorf("review count = %v", a.ReviewCount)
	}
	if len(a.PhotoURLs) != 2 {
		t.Errorf("photos = %v", a.PhotoURLs)
	}
	if a.Lat == nil || *a.Lat != 12.33 {
		t.Errorf("lat = %v", a.Lat)
	}

	b := halls[1]
	// "1624.0" is how pandas writes an int column that contains blanks.
	if b.ReviewCount == nil || *b.ReviewCount != 1624 {
		t.Errorf("float-formatted count not parsed: %v", b.ReviewCount)
	}
	if b.Lat != nil || b.Lng != nil {
		t.Errorf("missing coords should stay nil, got %v %v", b.Lat, b.Lng)
	}
	if b.Email != "x@y.com" {
		t.Errorf("email = %q", b.Email)
	}

	// Reviews CSV.
	revs := filepath.Join(dir, "reviews.csv")
	os.WriteFile(revs, []byte(
		"Google Maps URL,Business Name,Review ID,Author,Review Rating,Review Text,Review Date,Author Photo\n"+
			"https://maps/A,Sapthapadi Hall,rid1,Gowri,5,\"Spacious, large hall\",2 months ago,http://av/1\n"+
			"https://maps/A,Sapthapadi Hall,rid2,Sana,9,Out of range,5 years ago,\n"), 0o644)
	rrows, err := readCSV(revs)
	if err != nil {
		t.Fatal(err)
	}
	rs := parseReviews(rrows)
	if len(rs) != 2 {
		t.Fatalf("got %d reviews, want 2", len(rs))
	}
	if rs[0].Rating == nil || *rs[0].Rating != 5 || rs[0].Text != "Spacious, large hall" {
		t.Errorf("bad review: %+v", rs[0])
	}
	// A rating outside 1-5 would violate the CHECK constraint; it is dropped,
	// not clamped, so the review still imports without its star count.
	if rs[1].Rating != nil {
		t.Errorf("out-of-range rating should be nil, got %v", *rs[1].Rating)
	}
}

func TestPlaceholderEmailIsStable(t *testing.T) {
	// A venue with no phone and no email has only its vendor key to match on at
	// re-import time. If this address varied per run, every run would create
	// another user and vendor for the same venue.
	h := scrapedHall{Name: "No Contact Hall", MapsURL: "https://maps/D"}
	first := placeholderEmail(vendorKey(h))
	if second := placeholderEmail(vendorKey(h)); first != second {
		t.Errorf("placeholder must be stable across runs: %q != %q", first, second)
	}
	other := scrapedHall{Name: "Other Hall", MapsURL: "https://maps/E"}
	if placeholderEmail(vendorKey(other)) == first {
		t.Error("different venues must not share a placeholder address")
	}
	if !strings.HasSuffix(first, "@invalid.local") {
		t.Errorf("placeholder must be unroutable, got %q", first)
	}
}

func TestOversizedFieldsFromRealData(t *testing.T) {
	// Both of these failed a real Gorakhpur import before the widths were
	// guarded: users.full_name is varchar(100) and facilities.website is
	// varchar(255), and Google supplies values longer than either.
	long := strings.Repeat("Royal Darbar Banquet & Marriage Lawn ", 10) // 370 chars
	if got := truncate(long, 100); len(got) != 100 {
		t.Errorf("truncate to users.full_name width: got %d", len(got))
	}
	if got := truncate(long, 200); len(got) != 200 {
		t.Errorf("truncate to vendors.business_name width: got %d", len(got))
	}
	// A name already inside the limit must come back untouched.
	if got := truncate("Maurya Palace", 100); got != "Maurya Palace" {
		t.Errorf("short name must not be altered, got %q", got)
	}

	// A venue with no site of its own: Maps hands back a Google search URL.
	search := "https://www.google.com/search?gs_ssp=" + strings.Repeat("eJzj4tVP1zc0", 30)
	if got := websiteOf(search); got != "" {
		t.Errorf("google search URL must be dropped, got %q", got)
	}
	if got := websiteOf("https://www.google.com/maps/place/Foo"); got != "" {
		t.Errorf("maps URL must be dropped, got %q", got)
	}
	if got := websiteOf("https://realvenue.example/"); got != "https://realvenue.example/" {
		t.Errorf("a real homepage must survive, got %q", got)
	}
	if got := websiteOf("https://x.example/" + strings.Repeat("a", 300)); got != "" {
		t.Errorf("over-long URL must be dropped rather than truncated to a broken link, got %q", got)
	}
}

func TestDuplicatePlaceCollapsed(t *testing.T) {
	// Real Gorakhpur data: Google returned this venue under two URL shapes
	// across query passes, appending a landmark to the name in one of them.
	// Keyed on the URL, it imported twice as two near-identical listings.
	a := "https://www.google.com/maps/place/Julian+Club/@26.7237064,83.3744096,767m/" +
		"data=!3m2!1e3!4b1!4m6!3m5!1s0x399143003b20b40d:0x17e2574665f3e07!8m2!3d26.7237064!4d83.3744096?entry=ttu"
	b := "https://www.google.com/maps/place/Julian+Club/data=!4m7!3m6!" +
		"1s0x399143003b20b40d:0x17e2574665f3e07!8m2!3d26.7237064!4d83.3744096!3m1!1e3"
	if placeID(a) != placeID(b) {
		t.Fatalf("same venue must share a place id: %q vs %q", placeID(a), placeID(b))
	}

	rows := []map[string]string{
		{"Business Name": "Julian Club and Resort", "Google Maps URL": a, "Rating": "4.5",
			"Address": "Gorakhpur, Uttar Pradesh 273001"},
		{"Business Name": "Julian Club and Resort (near Bagaha Baba Mandir)",
			"Google Maps URL": b, "Rating": "4.5",
			"Address": "Gorakhpur, Uttar Pradesh 273001"},
		{"Business Name": "Ambey Palace", "Rating": "4.0",
			"Address":         "Indira Nagar, Gorakhpur, Uttar Pradesh 273001",
			"Google Maps URL": "https://maps/x/data=!1s0x3991437f658afc6f:0x331367468b5a08ad!3m1"},
	}
	halls := parseHalls(rows)
	if len(halls) != 2 {
		t.Fatalf("got %d halls, want 2 (the duplicate collapses)", len(halls))
	}
	// The first occurrence wins, so the name without Google's landmark suffix.
	if halls[0].Name != "Julian Club and Resort" {
		t.Errorf("first occurrence should win, got %q", halls[0].Name)
	}

	// A URL with no data blob still keys on itself rather than colliding.
	if placeID("https://maps/a") == placeID("https://maps/b") {
		t.Error("URLs without a place id must not collide")
	}
}

func TestUpscale(t *testing.T) {
	// Scraped URLs carry the thumbnail size the place panel rendered; fetching
	// them verbatim stored 160x120 images. =s1600 returns the full-size photo.
	const base = "https://lh3.googleusercontent.com/gps-cs-s/AHRPTWn2u3mU"
	if got := upscale(base + "=w408-h306-k-no"); got != base+"=s1600" {
		t.Errorf("upscale = %q", got)
	}
	// No size suffix: leave it alone rather than corrupting the path.
	if got := upscale(base); got != base {
		t.Errorf("suffixless URL must be unchanged, got %q", got)
	}
	// An '=' in an earlier path segment must not be mistaken for the suffix.
	q := "https://example.com/a=b/photo.jpg"
	if got := upscale(q); got != q {
		t.Errorf("'=' before the last slash must be ignored, got %q", got)
	}
}

func TestJunkRowsSkipped(t *testing.T) {
	// A tile search over a rural district returns settlements and landmarks
	// alongside venues. With no address and no contact there is nothing to
	// show, and imported rows go live as APPROVED.
	rows := []map[string]string{
		{"Business Name": "Partawal Bazar", "Google Maps URL": "https://maps/a/!1s0x1:0x1"},
		{"Business Name": "Real Hall", "Google Maps URL": "https://maps/b/!1s0x2:0x2",
			"Address": "Hebbal, Mysuru, Karnataka 570016"},
		{"Business Name": "Phone Only Hall", "Google Maps URL": "https://maps/c/!1s0x3:0x3",
			"Phone": "096118 49049"},
		{"Business Name": "Site Only Hall", "Google Maps URL": "https://maps/d/!1s0x4:0x4",
			"Website": "https://hall.example"},
	}
	halls := parseHalls(rows)
	if len(halls) != 3 {
		t.Fatalf("got %d halls, want 3 (only the contactless landmark is dropped)", len(halls))
	}
	for _, h := range halls {
		if h.Name == "Partawal Bazar" {
			t.Error("a row with no address and no contact must be skipped")
		}
	}
}

func TestTruncateIsRuneSafe(t *testing.T) {
	// Real Siddharthnagar venue: mathematical-bold Unicode, 4 bytes per letter.
	// A byte slice cut one of those in half and Postgres rejected the row with
	// "invalid byte sequence for encoding UTF8".
	name := "𝖫𝖮𝖨𝖨𝖸 𝖲𝖳𝖴𝖣𝖨𝖮 (𝐏𝐫𝐞𝐦𝐢𝐮𝐦 𝐖𝐞𝐝𝐝𝐢𝐧𝐠 𝐅𝐢𝐥𝐦𝐦𝐚𝐤𝐞𝐫, 𝐁𝐞𝐚𝐮𝐭𝐲)"
	for _, lim := range []int{20, 100, 200, 255} {
		got := truncate(name, lim)
		if !utf8.ValidString(got) {
			t.Errorf("truncate(name, %d) produced invalid UTF-8", lim)
		}
		if n := utf8.RuneCountInString(got); n > lim {
			t.Errorf("truncate(name, %d) kept %d characters", lim, n)
		}
	}
	// varchar(n) counts characters, so a name inside the limit must survive whole
	// even when its byte length is far larger.
	if got := truncate(name, 255); got != name {
		t.Errorf("name of %d chars must survive a 255 limit intact",
			utf8.RuneCountInString(name))
	}
	// ASCII is unaffected.
	if got := truncate("Maurya Palace Banquet", 13); got != "Maurya Palace" {
		t.Errorf("ascii truncate = %q", got)
	}
}
