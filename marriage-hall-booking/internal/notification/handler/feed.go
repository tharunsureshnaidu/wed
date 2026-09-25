package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tripfcatory/marriage-hall-booking/pkg/httpx"
	"github.com/tripfcatory/marriage-hall-booking/pkg/logger"
	"github.com/tripfcatory/marriage-hall-booking/pkg/middleware"
	"github.com/tripfcatory/marriage-hall-booking/pkg/response"
)

// feedLimit caps a page. The app shows a scrolling list, so the cap exists to
// bound the query rather than to match any screen.
const feedLimit = 20

const maxFeedLimit = 100

// list is the notification feed: GET /api/v1/notifications.
//
// Returns the caller's own notifications only. Admin ops rows carry a NULL
// recipient_user_id by design (one shared ops inbox, migration 034), so they
// never appear in anyone's personal feed - an admin sees what was addressed to
// them as a person, not the platform queue.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "Authentication required", "UNAUTHORIZED")
		return
	}

	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = feedLimit
	}
	if limit > maxFeedLimit {
		limit = maxFeedLimit
	}

	// Keyset cursor, not an offset: notifications arrive while the user is
	// scrolling, and an OFFSET page would shift under them and repeat rows.
	var before *time.Time
	if v := q.Get("before"); v != "" {
		t, err := time.Parse(time.RFC3339, repairCursor(v))
		if err != nil {
			response.Error(w, http.StatusBadRequest,
				"before must be an RFC3339 timestamp", "VALIDATION_ERROR")
			return
		}
		before = &t
	}

	items, err := h.svc.Feed(r.Context(), userID, q.Get("unreadOnly") == "true", before, limit)
	if err != nil {
		logger.Error("notify: feed", logger.Err(err))
		response.Error(w, http.StatusInternalServerError, "Could not load notifications", "INTERNAL_ERROR")
		return
	}
	unread, err := h.svc.UnreadCount(r.Context(), userID)
	if err != nil {
		logger.Error("notify: unread count", logger.Err(err))
		response.Error(w, http.StatusInternalServerError, "Could not load notifications", "INTERNAL_ERROR")
		return
	}

	// nextBefore is returned only on a full page. Sending it on a short page
	// would make the client fetch once more to discover the end.
	var nextBefore *time.Time
	if len(items) == limit {
		nextBefore = &items[len(items)-1].CreatedAt
	}

	response.OK(w, "Notifications retrieved successfully", map[string]any{
		"items":       items,
		"unreadCount": unread,
		"nextBefore":  nextBefore,
	})
}

// unreadCount is the badge on the bell icon: GET /api/v1/notifications/unread-count.
// Separate from the list because the app polls this far more often than it
// opens the screen, and it must not pay for a page of rows to draw a number.
func (h *Handler) unreadCount(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "Authentication required", "UNAUTHORIZED")
		return
	}
	n, err := h.svc.UnreadCount(r.Context(), userID)
	if err != nil {
		logger.Error("notify: unread count", logger.Err(err))
		response.Error(w, http.StatusInternalServerError, "Could not load unread count", "INTERNAL_ERROR")
		return
	}
	response.OK(w, "Unread count retrieved successfully", map[string]any{"unreadCount": n})
}

// markRead marks one feed item read: PUT /api/v1/notifications/{id}/read.
//
// Idempotent: marking an already-read notification is a 200 with updated 0, not
// an error. The app fires this on scroll and must not have to track what it has
// already sent.
func (h *Handler) markRead(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "Authentication required", "UNAUTHORIZED")
		return
	}
	// Validated as a UUID, not just length-checked: the column is uuid, so a
	// non-UUID string fails the cast inside Postgres and surfaces as a 500.
	// Bad input from a client is a 400.
	id := r.PathValue("id")
	if !httpx.ValidUUID(id) {
		response.Error(w, http.StatusBadRequest, "Invalid notification id", "VALIDATION_ERROR")
		return
	}
	n, err := h.svc.MarkRead(r.Context(), userID, id)
	if err != nil {
		logger.Error("notify: mark read", logger.Err(err))
		response.Error(w, http.StatusInternalServerError, "Could not update notification", "INTERNAL_ERROR")
		return
	}
	unread, err := h.svc.UnreadCount(r.Context(), userID)
	if err != nil {
		logger.Error("notify: unread count", logger.Err(err))
		response.Error(w, http.StatusInternalServerError, "Could not update notification", "INTERNAL_ERROR")
		return
	}
	// The fresh count comes back so the badge updates without a second call.
	response.OK(w, "Notification marked as read", map[string]any{
		"updated": n, "unreadCount": unread,
	})
}

// markAllRead is the "Read All" button: PUT /api/v1/notifications/read-all.
func (h *Handler) markAllRead(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "Authentication required", "UNAUTHORIZED")
		return
	}
	n, err := h.svc.MarkAllRead(r.Context(), userID)
	if err != nil {
		logger.Error("notify: mark all read", logger.Err(err))
		response.Error(w, http.StatusInternalServerError, "Could not update notifications", "INTERNAL_ERROR")
		return
	}
	response.OK(w, "All notifications marked as read", map[string]any{
		"updated": n, "unreadCount": 0,
	})
}

// repairCursor undoes the one mangling a client reliably inflicts on the
// keyset cursor: "+" in a query string decodes to a space, so the "+05:30"
// offset arrives as " 05:30". The cursor is our own value handed back
// verbatim, so repairing it is kinder than a 400 the app author has to debug.
//
// Anything else is left alone and fails the parse, which is what a genuinely
// bad cursor should do.
func repairCursor(v string) string {
	i := strings.LastIndex(v, " ")
	if i <= 0 || i == len(v)-1 || !strings.Contains(v[i+1:], ":") {
		return v
	}
	return v[:i] + "+" + v[i+1:]
}
