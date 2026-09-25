package handler

import (
	"context"
	"fmt"
	"strings"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
)

// Attaching amenities while creating the facility.
//
// Java's CreateFacilityRequest takes amenityIds as a repeated field of amenity
// CODES (PARKING, AIR_CONDITIONING...), not database UUIDs - the listing form
// sends checkbox values, and a person filling one in does not know row ids.
// Codes are accepted here for that reason; a UUID is accepted too, because the
// existing /facilities/{id}/amenities/{amenityId} route uses one and clients
// already send them.
//
// An amenity scoped to the other facility type is refused rather than silently
// dropped: "Bridal Room" on a hotel is a mistake worth reporting, and silently
// discarding it leaves the owner believing the listing has it.

type amenityRow struct {
	ID             string
	Name           string
	Code           *string
	ApplicableType string
}

// resolveAmenities turns the requested codes or ids into amenity rows, checking
// each one exists and applies to this facility type.
func (h *Handler) resolveAmenities(ctx context.Context, refs []string, facilityType string) ([]amenityRow, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	// Deduplicate: a form can repeat the same checkbox, and inserting the same
	// amenity twice would violate the primary key.
	seen := map[string]bool{}
	unique := make([]string, 0, len(refs))
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" || seen[strings.ToUpper(ref)] {
			continue
		}
		seen[strings.ToUpper(ref)] = true
		unique = append(unique, ref)
	}

	// Codes are compared upper-cased, ids lower-cased: a UUID renders as
	// lowercase hex, so matching it against the upper-cased list found nothing
	// and every attach-by-id failed with AMENITY_NOT_FOUND.
	rows, err := h.repo.Pool().Query(ctx,
		`SELECT id, name, code, applicable_type FROM amenities
		  WHERE upper(code) = ANY($1) OR id::text = ANY($2)`,
		upperAll(unique), lowerAll(unique))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []amenityRow{}
	found := map[string]bool{}
	for rows.Next() {
		var a amenityRow
		if err := rows.Scan(&a.ID, &a.Name, &a.Code, &a.ApplicableType); err != nil {
			return nil, err
		}
		found[strings.ToUpper(a.ID)] = true
		if a.Code != nil {
			found[strings.ToUpper(*a.Code)] = true
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// A code that matched nothing is an error, not something to drop quietly.
	var missing []string
	for _, ref := range unique {
		if !found[strings.ToUpper(ref)] {
			missing = append(missing, ref)
		}
	}
	if len(missing) > 0 {
		return nil, &amenityError{
			code: "AMENITY_NOT_FOUND",
			msg:  "Amenity not found: " + strings.Join(missing, ", "),
		}
	}

	for _, a := range out {
		if a.ApplicableType != "BOTH" && a.ApplicableType != facilityType {
			return nil, &amenityError{
				code: "AMENITY_TYPE_MISMATCH",
				msg:  fmt.Sprintf("Amenity '%s' does not apply to %s", a.Name, facilityType),
			}
		}
	}
	return out, nil
}

// amenityError carries the error code the response should use, so the handler
// does not have to guess which failure it is looking at.
type amenityError struct {
	code string
	msg  string
}

func (e *amenityError) Error() string { return e.msg }

func upperAll(in []string) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = strings.ToUpper(v)
	}
	return out
}

func lowerAll(in []string) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = strings.ToLower(v)
	}
	return out
}

// attachAmenities links amenities to a facility and reports which were
// genuinely new.
//
// The added list matters because re-adding an amenity a venue already has is a
// no-op, and announcing "now offers WiFi" to every nearby customer for a
// no-op is spam.
func (h *Handler) attachAmenities(ctx context.Context, facilityID string, list []amenityRow) ([]string, error) {
	var added []string
	for _, a := range list {
		tag, err := h.repo.Pool().Exec(ctx,
			`INSERT INTO facility_amenities (facility_id, amenity_id) VALUES ($1,$2)
			 ON CONFLICT DO NOTHING`, facilityID, a.ID)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() > 0 {
			added = append(added, a.Name)
		}
	}
	return added, nil
}

// amenityRefs pulls amenityIds out of a multipart form, where the field is
// repeated once per checkbox rather than sent as a JSON array.
func amenityRefs(values []string) []string {
	out := []string{}
	for _, v := range values {
		// A single field may also carry a comma-separated list.
		for _, part := range strings.Split(v, ",") {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

var _ = httpx.ValidUUID // keep the import when the helper above is unused
