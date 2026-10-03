package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/audit"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
)

// decisionAnalytics is GET /api/v1/admin/analytics/decisions - what the
// platform is approving and rejecting.
//
// Reads audit_logs rather than each entity's current status, deliberately:
// status tells you where a thing ended up, not how it got there. A listing
// rejected and later approved looks identical to one approved first time if
// you only count statuses, and a booking cancelled then deleted vanishes
// entirely. The audit row survives both.
func (h *Handler) decisionAnalytics(w http.ResponseWriter, r *http.Request) {
	from, until, ok := decisionRange(w, r)
	if !ok {
		return
	}
	// An unknown entity is rejected rather than silently matching nothing: a
	// typo would otherwise read as "zero decisions", which looks like a quiet
	// month instead of a bad request.
	entity := strings.TrimSpace(r.URL.Query().Get("entity"))
	if entity != "" && !validEntity(entity) {
		response.Error(w, http.StatusBadRequest,
			"entity must be one of: "+entityNames, "VALIDATION_ERROR")
		return
	}

	rows, err := h.db.Query(r.Context(), `
		SELECT entity_name,
		       COALESCE(new_values->>'status', 'UNKNOWN') AS status,
		       count(*),
		       count(*) FILTER (WHERE COALESCE(new_values->>'reason','') <> '')
		  FROM audit_logs
		 WHERE created_at >= $1 AND created_at < $2
		   AND ($3 = '' OR entity_name = $3)
		 GROUP BY 1, 2
		 ORDER BY 1, 3 DESC`, from, until, entity)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()

	type statusCount struct {
		Status     string `json:"status"`
		Count      int64  `json:"count"`
		WithReason int64  `json:"withReason"`
	}
	byEntity := map[string][]statusCount{}
	var total, rejected int64
	for rows.Next() {
		var ent, status string
		var n, withReason int64
		if err := rows.Scan(&ent, &status, &n, &withReason); err != nil {
			httpx.Fail(w, err)
			return
		}
		byEntity[ent] = append(byEntity[ent], statusCount{status, n, withReason})
		total += n
		if isRejection(status) {
			rejected += n
		}
	}
	if err := rows.Err(); err != nil {
		httpx.Fail(w, err)
		return
	}

	// Rejections broken down by the occasion they were for. Only bookings
	// carry one, so this is empty until a booking is cancelled.
	events, err := h.rejectionsByEvent(r, from, until)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	reasons, err := h.topReasons(r, from, until, entity)
	if err != nil {
		httpx.Fail(w, err)
		return
	}

	// rejectionRate is null rather than 0 when nothing was decided: 0% reads
	// as "we rejected nobody", which is not the same as "nothing happened".
	var rate *float64
	if total > 0 {
		v := float64(rejected) / float64(total)
		rate = &v
	}

	response.OK(w, "Decision analytics retrieved successfully", map[string]any{
		"from": from, "until": until,
		"totalDecisions": total,
		"rejected":       rejected,
		"rejectionRate":  rate,
		"byEntity":       byEntity,
		"byEventType":    events,
		"topReasons":     reasons,
	})
}

// isRejection names the statuses that mean "turned down". Kept in one place so
// the rate and the breakdown cannot disagree about what counts.
func isRejection(status string) bool {
	switch status {
	case "REJECTED", "BLOCKED", "CANCELLED":
		return true
	}
	return false
}

// decisionRange reads ?from=&until=, defaulting to the last 30 days.
func decisionRange(w http.ResponseWriter, r *http.Request) (time.Time, time.Time, bool) {
	q := r.URL.Query()
	until := time.Now()
	from := until.AddDate(0, 0, -30)
	parse := func(raw string) (time.Time, bool) {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			return t, true
		}
		t, err := time.Parse("2006-01-02", raw)
		return t, err == nil
	}
	if raw := q.Get("from"); raw != "" {
		t, ok := parse(raw)
		if !ok {
			response.Error(w, http.StatusBadRequest,
				"from must be YYYY-MM-DD or RFC3339", "VALIDATION_ERROR")
			return time.Time{}, time.Time{}, false
		}
		from = t
	}
	if raw := q.Get("until"); raw != "" {
		t, ok := parse(raw)
		if !ok {
			response.Error(w, http.StatusBadRequest,
				"until must be YYYY-MM-DD or RFC3339", "VALIDATION_ERROR")
			return time.Time{}, time.Time{}, false
		}
		// A bare date means the whole of that day, not midnight at its start -
		// otherwise ?until=today returns nothing that happened today.
		if len(raw) == len("2006-01-02") {
			t = t.AddDate(0, 0, 1)
		}
		until = t
	}
	if !from.Before(until) {
		response.Error(w, http.StatusBadRequest,
			"from must be before until", "VALIDATION_ERROR")
		return time.Time{}, time.Time{}, false
	}
	return from, until, true
}

type eventBreakdown struct {
	EventType string `json:"eventType"`
	Total     int64  `json:"total"`
	Rejected  int64  `json:"rejected"`
}

func (h *Handler) rejectionsByEvent(r *http.Request, from, until time.Time) ([]eventBreakdown, error) {
	rows, err := h.db.Query(r.Context(), `
		SELECT new_values->>'eventType',
		       count(*),
		       count(*) FILTER (WHERE new_values->>'status' IN ('REJECTED','BLOCKED','CANCELLED'))
		  FROM audit_logs
		 WHERE created_at >= $1 AND created_at < $2
		   AND COALESCE(new_values->>'eventType','') <> ''
		 GROUP BY 1
		 ORDER BY 2 DESC`, from, until)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []eventBreakdown{}
	for rows.Next() {
		var e eventBreakdown
		if err := rows.Scan(&e.EventType, &e.Total, &e.Rejected); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

type reasonCount struct {
	Reason string `json:"reason"`
	Count  int64  `json:"count"`
}

// topReasons is why things were turned down, commonest first. Capped: this is
// free text an admin typed, so the long tail is mostly one-offs.
func (h *Handler) topReasons(r *http.Request, from, until time.Time, entity string) ([]reasonCount, error) {
	rows, err := h.db.Query(r.Context(), `
		SELECT new_values->>'reason', count(*)
		  FROM audit_logs
		 WHERE created_at >= $1 AND created_at < $2
		   AND ($3 = '' OR entity_name = $3)
		   AND COALESCE(new_values->>'reason','') <> ''
		   AND new_values->>'status' IN ('REJECTED','BLOCKED','CANCELLED')
		 GROUP BY 1
		 ORDER BY 2 DESC
		 LIMIT 20`, from, until, entity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []reasonCount{}
	for rows.Next() {
		var c reasonCount
		if err := rows.Scan(&c.Reason, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// knownEntities is what ?entity= accepts.
var knownEntities = []string{
	audit.EntityFacility, audit.EntityVendor, audit.EntityBooking,
	audit.EntityQuote, audit.EntityReview, audit.EntityUser,
}

var entityNames = strings.Join(knownEntities, ", ")

func validEntity(name string) bool {
	for _, k := range knownEntities {
		if k == name {
			return true
		}
	}
	return false
}
