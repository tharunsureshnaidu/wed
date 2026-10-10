package venuetype

// LiveSQL is the one definition of "a customer may see and book this venue",
// as a predicate over the facilities row aliased a: not deleted, and not
// BLOCKED or REJECTED. PENDING stays live - most of the catalogue was imported
// unreviewed, and hiding it would empty the app.
//
// It replaces four drifted copies: search said <> 'BLOCKED' (so rejected venues
// showed), hall booking the same, hotel booking checked nothing at all.
func LiveSQL(a string) string {
	return a + ".is_deleted = FALSE AND COALESCE(" + a + ".status, 'APPROVED') NOT IN ('BLOCKED', 'REJECTED')"
}
