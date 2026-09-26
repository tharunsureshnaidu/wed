package repository

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/eventtypes"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("not found")
	ErrNotOwner = errors.New("not owner")
)

type Repo struct{ db *pgxpool.Pool }

func New(db *pgxpool.Pool) *Repo { return &Repo{db: db} }

func (r *Repo) Pool() *pgxpool.Pool { return r.db }

type Facility struct {
	ID   string `json:"id"`
	Type string `json:"type"`

	// Java's FacilityDTO exposes the owner's phone and the vendor profile id
	// alongside the owner id; clients show them on the listing page.
	OwnerID          int64   `json:"ownerId"`
	OwnerPhoneNumber *string `json:"ownerPhoneNumber"`
	VendorID         *string `json:"vendorId"`

	Name        string  `json:"name"`
	Description *string `json:"description"`
	City        *string `json:"city"`
	// fullAddress/zipcode, not street/zipCode: these names are the client
	// contract, and the column names follow Java's entity.
	FullAddress *string  `json:"fullAddress"`
	State       *string  `json:"state"`
	Zipcode     *string  `json:"zipcode"`
	Country     *string  `json:"country"`
	Lat         *float64 `json:"lat"`
	Lng         *float64 `json:"lng"`

	Status string `json:"status"`
	// Lombok renders boolean getters without the "is" prefix, so Java sends
	// verified/featured. Keep both spellings: the old ones shipped already.
	Verified    bool    `json:"verified"`
	Featured    bool    `json:"featured"`
	IsVerified  bool    `json:"isVerified"`
	IsFeatured  bool    `json:"isFeatured"`
	AvgRating   float64 `json:"avgRating"`
	ReviewCount int     `json:"reviewCount"`

	// Hall: base price per day. Hotel: cheapest room type. Computed, not stored.
	StartingPrice *float64 `json:"startingPrice"`

	// An advertised discount on the listing. DiscountPercent is nil when the
	// venue has none, or when the one it had has expired - an expired offer is
	// filtered out in SQL so it can never reach a client.
	//
	// DiscountedPrice is computed here rather than left to each client: four
	// apps rounding a percentage four ways is four different prices on the
	// same venue.
	DiscountPercent *float64 `json:"discountPercent"`
	DiscountLabel   *string  `json:"discountLabel"`
	DiscountedPrice *float64 `json:"discountedPrice"`
	HasDiscount     bool     `json:"hasDiscount"`

	StarRating   *int    `json:"starRating"`
	CheckInTime  *string `json:"checkInTime"`
	CheckOutTime *string `json:"checkOutTime"`

	CapacityPax      *int     `json:"capacityPax"`
	AreaSqft         *int     `json:"areaSqft"`
	BasePricePerDay  *float64 `json:"basePricePerDay"`
	SeatingCapacity  *int     `json:"seatingCapacity"`
	FloatingCapacity *int     `json:"floatingCapacity"`
	MinBookingSize   *int     `json:"minBookingSize"`

	// EventCodes are the events this venue hosts. Codes only - the handler
	// resolves display names from its catalogue, so a rename there does not
	// need a data migration here.
	EventCodes []string `json:"eventCodes"`

	// Faqs are returned with the detail read only. The list screen shows a
	// card, not an accordion, and loading them per row would be an N+1 on the
	// most-hit endpoint.
	Faqs []FAQ `json:"faqs,omitempty"`

	Amenities []Amenity `json:"amenities"`
	Images    []Image   `json:"images"`
	// Reviews are returned with the detail read only (never the list, which
	// would be one query per row): the detail page shows them, and avgRating /
	// reviewCount above are the summary the list needs.
	Reviews []Review `json:"reviews,omitempty"`
}

// MarshalJSON adds the grouped blocks the venue detail screen reads -
// location, rating, price, coordinates - on top of the flat fields, which stay
// exactly as they were so existing callers keep working.
//
// ponytail: a marshaller, not a second DTO and no extra queries; every value
// below is already loaded.
func (f Facility) MarshalJSON() ([]byte, error) {
	type raw Facility // avoids recursing into this method
	return json.Marshal(struct {
		raw
		Location    location    `json:"location"`
		Rating      rating      `json:"rating"`
		Price       *price      `json:"price"`
		Coordinates *coordinate `json:"coordinates"`
		// Resolved alongside the raw codes so a client renders "Sangeet"
		// without shipping its own copy of the catalogue. eventCodes stays for
		// the clients already reading it.
		Events []eventtypes.EventType `json:"events"`
	}{
		raw:         raw(f),
		Location:    location{f.City, f.State, f.Country, f.FullAddress, f.Lat, f.Lng},
		Rating:      rating{f.AvgRating, f.ReviewCount},
		Price:       f.price(),
		Coordinates: f.coordinates(),
		Events:      eventtypes.Views(f.EventCodes),
	})
}

type location struct {
	City        *string  `json:"city"`
	State       *string  `json:"state"`
	Country     *string  `json:"country"`
	FullAddress *string  `json:"fullAddress"`
	Latitude    *float64 `json:"latitude"`
	Longitude   *float64 `json:"longitude"`
}

type rating struct {
	Value       float64 `json:"value"`
	ReviewCount int     `json:"reviewCount"`
}

type price struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
	Period   string  `json:"period"`
}

type coordinate struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// price reports the headline figure. A hotel prices per night (its cheapest
// room type) and a hall per day, which is what StartingPrice already resolves
// to. Null when there is no price yet - a hotel with no room types - rather
// than a misleading zero.
func (f Facility) price() *price {
	if f.StartingPrice == nil {
		return nil
	}
	period := "DAY"
	if f.Type == "HOTEL" {
		period = "NIGHT"
	}
	// ponytail: every amount in this system is rupees; add a currency column
	// if a venue ever prices in something else.
	return &price{Amount: *f.StartingPrice, Currency: "INR", Period: period}
}

// coordinates is null unless the venue has both, so a client never plots a
// pin at (0,0) off the coast of Africa for a venue that was never geocoded.
func (f Facility) coordinates() *coordinate {
	if f.Lat == nil || f.Lng == nil {
		return nil
	}
	return &coordinate{Latitude: *f.Lat, Longitude: *f.Lng}
}

// Review is the subset of a review the facility detail page shows.
type Review struct {
	ID        string    `json:"id"`
	UserID    int64     `json:"userId"`
	UserName  string    `json:"userName"`
	Rating    int       `json:"rating"`
	Title     *string   `json:"title"`
	Comment   *string   `json:"comment"`
	CreatedAt time.Time `json:"createdAt"`
}

type Amenity struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Code           *string `json:"code"`
	ApplicableType string  `json:"applicableType"`
}

// MarshalJSON adds `icon`, derived from the code rather than stored: the
// amenities.icon column exists but is NULL for every row, so reading it would
// send null to clients that need an icon name for all 40 amenities.
//
// ponytail: derived, not a column. If a specific amenity ever needs an icon
// that is not its code (front-desk for 24_HOUR_FRONT_DESK), populate
// amenities.icon and prefer it here.
func (a Amenity) MarshalJSON() ([]byte, error) {
	type raw Amenity // avoids recursing into this method
	return json.Marshal(struct {
		raw
		Icon string `json:"icon"`
	}{raw(a), a.icon()})
}

func (a Amenity) icon() string {
	if a.Code == nil || *a.Code == "" {
		// Fall back to the name, so an amenity added without a code still
		// gets an icon rather than an empty string.
		return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(a.Name), " ", "-"))
	}
	return strings.ToLower(strings.ReplaceAll(*a.Code, "_", "-"))
}

type Image struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	IsCover   bool   `json:"isCover"`
	SortOrder int    `json:"sortOrder"`
}

const facilityCols = `f.id, f.owner_id, u.phone_number, f.vendor_id, f.name, f.description,
	f.type, f.city, f.full_address, f.state, f.zipcode, f.country, f.lat, f.lng,
	f.status, f.is_verified, f.is_featured, COALESCE(f.avg_rating,0), COALESCE(f.review_count,0),
	f.star_rating, f.check_in_time, f.check_out_time, f.capacity_pax, f.area_sqft,
	f.base_price_per_day, f.seating_capacity, f.floating_capacity, f.min_booking_size,
	CASE WHEN f.type = 'MARRIAGE_HALL' THEN f.base_price_per_day
	     ELSE (SELECT MIN(rt.base_price_per_night) FROM room_types rt
	            WHERE rt.facility_id = f.id AND rt.is_deleted = FALSE)
	END,
	CASE WHEN f.discount_valid_until IS NULL OR f.discount_valid_until > CURRENT_TIMESTAMP
	     THEN f.discount_percent END,
	CASE WHEN f.discount_valid_until IS NULL OR f.discount_valid_until > CURRENT_TIMESTAMP
	     THEN f.discount_label END`

// applyDiscount derives the struck-through price.
//
// A discount with no starting price to apply it to is still reported - the card
// can show "15% off" without a number - but there is nothing to compute.
// Rounded to whole currency units: a listing price of 84999.9999 is not a price
// anyone would print.
func (f *Facility) applyDiscount() {
	if f.DiscountPercent == nil || *f.DiscountPercent <= 0 {
		return
	}
	f.HasDiscount = true
	if f.StartingPrice == nil {
		return
	}
	d := math.Round(*f.StartingPrice * (100 - *f.DiscountPercent) / 100)
	f.DiscountedPrice = &d
}

// facilityFrom joins the owner so ownerPhoneNumber comes back in the same read.
const facilityFrom = ` FROM facilities f JOIN users u ON u.id = f.owner_id`

func scanFacility(row pgx.Row) (*Facility, error) {
	var f Facility
	err := row.Scan(&f.ID, &f.OwnerID, &f.OwnerPhoneNumber, &f.VendorID, &f.Name, &f.Description,
		&f.Type, &f.City, &f.FullAddress, &f.State, &f.Zipcode, &f.Country, &f.Lat, &f.Lng,
		&f.Status, &f.IsVerified, &f.IsFeatured,
		&f.AvgRating, &f.ReviewCount, &f.StarRating, &f.CheckInTime, &f.CheckOutTime,
		&f.CapacityPax, &f.AreaSqft, &f.BasePricePerDay, &f.SeatingCapacity,
		&f.FloatingCapacity, &f.MinBookingSize, &f.StartingPrice,
		&f.DiscountPercent, &f.DiscountLabel)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	f.applyDiscount()
	f.Verified, f.Featured = f.IsVerified, f.IsFeatured
	f.Amenities, f.Images = []Amenity{}, []Image{}
	return &f, err
}

type CreateInput struct {
	OwnerID     int64
	Name        string
	Description *string
	Type        string
	City        *string
	FullAddress *string
	State       *string
	Zipcode     *string
	Country     *string
	Lat         *float64
	Lng         *float64

	StarRating   *int
	CheckInTime  *string
	CheckOutTime *string

	CapacityPax      *int
	AreaSqft         *int
	BasePricePerDay  *float64
	SeatingCapacity  *int
	FloatingCapacity *int
	MinBookingSize   *int
}

func (r *Repo) Create(ctx context.Context, in CreateInput) (*Facility, error) {
	var id string
	err := r.db.QueryRow(ctx,
		`INSERT INTO facilities (owner_id, name, description, type, city, full_address, state,
		    zipcode, country, lat, lng, star_rating, check_in_time, check_out_time, capacity_pax,
		    area_sqft, base_price_per_day, seating_capacity, floating_capacity, min_booking_size)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
		 RETURNING id`,
		in.OwnerID, in.Name, in.Description, in.Type, in.City, in.FullAddress, in.State,
		in.Zipcode, in.Country, in.Lat, in.Lng, in.StarRating, in.CheckInTime, in.CheckOutTime,
		in.CapacityPax, in.AreaSqft, in.BasePricePerDay, in.SeatingCapacity,
		in.FloatingCapacity, in.MinBookingSize).Scan(&id)
	if err != nil {
		return nil, err
	}
	// Link the listing to the owner's vendor business, as Java does on create.
	// A vendor row is guaranteed for non-admin callers (see VendorOf); an admin
	// creating a listing has none, and vendor_id stays NULL - a valid state.
	if _, err := r.db.Exec(ctx,
		`UPDATE facilities SET vendor_id = v.id FROM vendors v
		 WHERE facilities.id = $1 AND v.user_id = $2`, id, in.OwnerID); err != nil {
		return nil, err
	}
	return r.Get(ctx, id)
}

func (r *Repo) Get(ctx context.Context, id string) (*Facility, error) {
	f, err := scanFacility(r.db.QueryRow(ctx,
		`SELECT `+facilityCols+facilityFrom+` WHERE f.id = $1 AND f.is_deleted = FALSE`, id))
	if err != nil {
		return nil, err
	}
	if f.Amenities, err = r.amenitiesOf(ctx, id); err != nil {
		return nil, err
	}
	if f.EventCodes, err = r.EventsOf(ctx, id); err != nil {
		return nil, err
	}
	if f.Faqs, err = r.FaqsOf(ctx, id); err != nil {
		return nil, err
	}
	if f.Images, err = r.imagesOf(ctx, id); err != nil {
		return nil, err
	}
	if f.Reviews, err = r.reviewsOf(ctx, id); err != nil {
		return nil, err
	}
	return f, nil
}

// reviewsOf returns the most recent reviews for the detail page. Capped rather
// than unbounded: a popular venue with thousands of reviews would otherwise
// make its own detail response enormous. Full history is paged at
// GET /api/v1/reviews/facility/{id}.
func (r *Repo) reviewsOf(ctx context.Context, id string) ([]Review, error) {
	rows, err := r.db.Query(ctx,
		`SELECT rv.id, rv.user_id, u.full_name, rv.rating, rv.title, rv.comment, rv.created_at
		   FROM reviews rv JOIN users u ON u.id = rv.user_id
		  WHERE rv.facility_id = $1 AND rv.is_deleted = FALSE
		  ORDER BY rv.created_at DESC LIMIT 20`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Review{}
	for rows.Next() {
		var x Review
		if err := rows.Scan(&x.ID, &x.UserID, &x.UserName, &x.Rating,
			&x.Title, &x.Comment, &x.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// HasVendor reports whether the user has created their vendor business.
// Java refuses facility creation without one (VENDOR_REQUIRED), so the check
// lives next to Create rather than in each caller.
func (r *Repo) HasVendor(ctx context.Context, userID int64) (bool, error) {
	var ok bool
	err := r.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM vendors WHERE user_id = $1)`, userID).Scan(&ok)
	return ok, err
}

// OwnerOf returns the owner id without loading the whole row, for auth checks.
func (r *Repo) OwnerOf(ctx context.Context, id string) (int64, error) {
	var owner int64
	err := r.db.QueryRow(ctx,
		`SELECT owner_id FROM facilities WHERE id = $1 AND is_deleted = FALSE`, id).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return owner, err
}

func (r *Repo) Update(ctx context.Context, id string, in CreateInput) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE facilities SET name = $2, description = $3, city = $4, full_address = $5,
		    state = $6, zipcode = $7, country = $8, lat = $9, lng = $10,
		    star_rating = $11, check_in_time = $12,
		    check_out_time = $13, capacity_pax = $14, area_sqft = $15, base_price_per_day = $16,
		    seating_capacity = $17, floating_capacity = $18, min_booking_size = $19,
		    updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1 AND is_deleted = FALSE`,
		id, in.Name, in.Description, in.City, in.FullAddress, in.State, in.Zipcode, in.Country,
		in.Lat, in.Lng,
		in.StarRating, in.CheckInTime, in.CheckOutTime, in.CapacityPax, in.AreaSqft,
		in.BasePricePerDay, in.SeatingCapacity, in.FloatingCapacity, in.MinBookingSize)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repo) SoftDelete(ctx context.Context, id string) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE facilities SET is_deleted = TRUE, updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1 AND is_deleted = FALSE`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

type ListFilter struct {
	Type    string
	Search  string
	City    string
	OwnerID int64 // 0 means any owner
	Page    int
	Size    int
}

func (r *Repo) List(ctx context.Context, f ListFilter) ([]Facility, int64, error) {
	// Fixed parameter positions with a sentinel for "not filtering", rather than
	// building the WHERE clause dynamically - every value stays a bound parameter,
	// so no caller input can reach the SQL text.
	args := []any{f.Type, f.City, f.OwnerID, f.Search}
	// Columns are qualified with f. because facilityFrom joins users.
	clause := ` WHERE f.is_deleted = FALSE
		AND ($1 = '' OR f.type = $1)
		AND ($2 = '' OR LOWER(f.city) = LOWER($2))
		AND ($3 = 0 OR f.owner_id = $3)
		AND ($4 = '' OR f.name ILIKE '%' || $4 || '%' OR f.description ILIKE '%' || $4 || '%')`

	var total int64
	if err := r.db.QueryRow(ctx, `SELECT count(*)`+facilityFrom+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, f.Size, f.Page*f.Size)
	rows, err := r.db.Query(ctx,
		`SELECT `+facilityCols+facilityFrom+clause+
			` ORDER BY f.is_featured DESC, f.created_at DESC LIMIT $5 OFFSET $6`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []Facility{}
	ids := make([]string, 0, f.Size)
	for rows.Next() {
		fac, err := scanFacility(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *fac)
		ids = append(ids, fac.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	// One query for the whole page, not one per row: the list is the most-hit
	// screen and 20 venues would be 20 round trips.
	events, err := r.EventsOfMany(ctx, ids)
	if err != nil {
		return nil, 0, err
	}
	for i := range out {
		out[i].EventCodes = events[out[i].ID]
	}
	return out, total, nil
}

func (r *Repo) amenitiesOf(ctx context.Context, id string) ([]Amenity, error) {
	rows, err := r.db.Query(ctx,
		`SELECT a.id, a.name, a.code, a.applicable_type FROM facility_amenities fa
		 JOIN amenities a ON a.id = fa.amenity_id WHERE fa.facility_id = $1 ORDER BY a.name`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Amenity{}
	for rows.Next() {
		var a Amenity
		if err := rows.Scan(&a.ID, &a.Name, &a.Code, &a.ApplicableType); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *Repo) imagesOf(ctx context.Context, id string) ([]Image, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, url, is_cover, sort_order FROM facility_images
		 WHERE facility_id = $1 ORDER BY sort_order, created_at`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Image{}
	for rows.Next() {
		var i Image
		if err := rows.Scan(&i.ID, &i.URL, &i.IsCover, &i.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (r *Repo) ListAmenities(ctx context.Context, typeFilter string) ([]Amenity, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, name, code, applicable_type FROM amenities
		 WHERE $1 = '' OR applicable_type = $1 OR applicable_type = 'BOTH'
		 ORDER BY applicable_type, name`, typeFilter)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Amenity{}
	for rows.Next() {
		var a Amenity
		if err := rows.Scan(&a.ID, &a.Name, &a.Code, &a.ApplicableType); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *Repo) AddAmenity(ctx context.Context, facilityID, amenityID string) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO facility_amenities (facility_id, amenity_id) VALUES ($1, $2)
		 ON CONFLICT DO NOTHING`, facilityID, amenityID)
	return err
}

func (r *Repo) RemoveAmenity(ctx context.Context, facilityID, amenityID string) error {
	_, err := r.db.Exec(ctx,
		`DELETE FROM facility_amenities WHERE facility_id = $1 AND amenity_id = $2`,
		facilityID, amenityID)
	return err
}

// ByIDs loads several facilities in one round trip, each with its amenities and
// cover image. It exists for the compare screen: calling Get once per venue
// would be three queries plus three more for amenities and three for images.
//
// Order follows the ids argument, not the database's, so the comparison columns
// stay in the order the user picked them. A missing or deleted id is simply
// absent from the result - the handler decides whether that is an error.
func (r *Repo) ByIDs(ctx context.Context, ids []string) ([]Facility, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+facilityCols+facilityFrom+
			` WHERE f.id::text = ANY($1) AND f.is_deleted = FALSE`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byID := map[string]*Facility{}
	for rows.Next() {
		f, err := scanFacility(rows)
		if err != nil {
			return nil, err
		}
		byID[f.ID] = f
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(byID) == 0 {
		return nil, nil
	}

	// Amenities for every venue in one query, then distributed - the N+1 this
	// method exists to avoid.
	arows, err := r.db.Query(ctx,
		`SELECT fa.facility_id::text, a.id, a.name, a.code, a.applicable_type
		   FROM facility_amenities fa
		   JOIN amenities a ON a.id = fa.amenity_id
		  WHERE fa.facility_id::text = ANY($1)
		  ORDER BY a.name`, ids)
	if err != nil {
		return nil, err
	}
	defer arows.Close()
	for arows.Next() {
		var fid string
		var a Amenity
		if err := arows.Scan(&fid, &a.ID, &a.Name, &a.Code, &a.ApplicableType); err != nil {
			return nil, err
		}
		if f, ok := byID[fid]; ok {
			f.Amenities = append(f.Amenities, a)
		}
	}
	if err := arows.Err(); err != nil {
		return nil, err
	}

	// Cover image only: the compare card shows one thumbnail per venue, and
	// pulling every gallery image would be the same N+1 by another name.
	irows, err := r.db.Query(ctx, `
		SELECT DISTINCT ON (i.facility_id)
		       i.facility_id::text, i.id, i.url, i.is_cover, i.sort_order
		  FROM facility_images i
		 WHERE i.facility_id::text = ANY($1)
		 ORDER BY i.facility_id, i.is_cover DESC, i.sort_order`, ids)
	if err != nil {
		return nil, err
	}
	defer irows.Close()
	for irows.Next() {
		var fid string
		var im Image
		if err := irows.Scan(&fid, &im.ID, &im.URL, &im.IsCover, &im.SortOrder); err != nil {
			return nil, err
		}
		if f, ok := byID[fid]; ok {
			f.Images = append(f.Images, im)
		}
	}
	if err := irows.Err(); err != nil {
		return nil, err
	}

	out := make([]Facility, 0, len(byID))
	for _, id := range ids {
		if f, ok := byID[id]; ok {
			out = append(out, *f)
		}
	}
	return out, nil
}

// EventsOf returns the event codes a venue hosts.
func (r *Repo) EventsOf(ctx context.Context, facilityID string) ([]string, error) {
	rows, err := r.db.Query(ctx,
		`SELECT event_code FROM facility_events
		  WHERE facility_id = $1 ORDER BY event_code`, facilityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, err
		}
		out = append(out, code)
	}
	return out, rows.Err()
}

// SetEvents replaces a venue's event list.
//
// Delete-then-insert inside one transaction, rather than diffing: the payload
// is the complete list from a checkbox screen, and computing a diff would be
// more code for the same result. The transaction is what stops a failed insert
// leaving the venue hosting nothing.
func (r *Repo) SetEvents(ctx context.Context, facilityID string, codes []string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`DELETE FROM facility_events WHERE facility_id = $1`, facilityID); err != nil {
		return err
	}
	for _, code := range codes {
		if _, err := tx.Exec(ctx,
			`INSERT INTO facility_events (facility_id, event_code) VALUES ($1, $2)
			 ON CONFLICT DO NOTHING`, facilityID, code); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// HostsEvent reports whether a venue has declared it hosts an event type.
//
// A venue with NO declared events returns true for anything: 43 halls predate
// this feature, and refusing their bookings would be a regression caused by a
// feature the owner has not seen yet. Silence means "not stated", never "no".
func (r *Repo) HostsEvent(ctx context.Context, facilityID, code string) (bool, error) {
	var declared, hosts bool
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM facility_events WHERE facility_id = $1),
		       EXISTS (SELECT 1 FROM facility_events
		                WHERE facility_id = $1 AND event_code = $2)`,
		facilityID, code).Scan(&declared, &hosts)
	if err != nil {
		return false, err
	}
	return !declared || hosts, nil
}

// FAQ is one question and answer on a venue's detail page.
type FAQ struct {
	ID        string `json:"id"`
	Question  string `json:"question"`
	Answer    string `json:"answer"`
	SortOrder int    `json:"sortOrder"`
}

// FaqsOf returns a venue's live FAQs in display order.
func (r *Repo) FaqsOf(ctx context.Context, facilityID string) ([]FAQ, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id::text, question, answer, sort_order
		  FROM facility_faqs
		 WHERE facility_id = $1 AND is_deleted = FALSE
		 ORDER BY sort_order, created_at`, facilityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []FAQ{}
	for rows.Next() {
		var f FAQ
		if err := rows.Scan(&f.ID, &f.Question, &f.Answer, &f.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (r *Repo) AddFaq(ctx context.Context, facilityID, question, answer string, sortOrder int) (string, error) {
	var id string
	err := r.db.QueryRow(ctx, `
		INSERT INTO facility_faqs (facility_id, question, answer, sort_order)
		VALUES ($1, $2, $3, $4) RETURNING id::text`,
		facilityID, question, answer, sortOrder).Scan(&id)
	return id, err
}

// UpdateFaq applies only the fields the caller sent; a nil stays untouched.
// Scoped to the facility as well as the id, so an id from another venue
// matches nothing rather than editing someone else's FAQ.
func (r *Repo) UpdateFaq(ctx context.Context, facilityID, id string, question, answer *string, sortOrder *int) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE facility_faqs SET
		    question   = COALESCE($3, question),
		    answer     = COALESCE($4, answer),
		    sort_order = COALESCE($5, sort_order),
		    updated_at = CURRENT_TIMESTAMP
		 WHERE id = $2 AND facility_id = $1 AND is_deleted = FALSE`,
		facilityID, id, question, answer, sortOrder)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// DeleteFaq soft-deletes, matching the rest of this schema.
func (r *Repo) DeleteFaq(ctx context.Context, facilityID, id string) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE facility_faqs SET is_deleted = TRUE, updated_at = CURRENT_TIMESTAMP
		 WHERE id = $2 AND facility_id = $1 AND is_deleted = FALSE`, facilityID, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// EventsOfMany loads event codes for a whole page of facilities in ONE query.
//
// The list endpoint returns 20 venues; calling EventsOf per row would be 20
// round trips on the most-hit screen in the app.
func (r *Repo) EventsOfMany(ctx context.Context, ids []string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.db.Query(ctx, `
		SELECT facility_id::text, event_code
		  FROM facility_events
		 WHERE facility_id::text = ANY($1)
		 ORDER BY facility_id, event_code`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var fid, code string
		if err := rows.Scan(&fid, &code); err != nil {
			return nil, err
		}
		out[fid] = append(out[fid], code)
	}
	return out, rows.Err()
}
