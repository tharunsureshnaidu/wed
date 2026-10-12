package venuetype

// LiveSQL is the one definition of "a customer may see and book this venue",
// as a predicate over the facilities row aliased a: not deleted, and APPROVED.
//
// A venue reaches customers only once an admin approves it - PENDING is
// unreviewed, and listing it bypassed moderation. It replaced four drifted
// copies: search said <> 'BLOCKED' (so rejected venues showed), hall booking
// the same, hotel booking checked nothing at all.
func LiveSQL(a string) string {
	return a + ".is_deleted = FALSE AND " + a + ".status = 'APPROVED'"
}

// Live is LiveSQL for a row already loaded (deleted rows never load).
func Live(status string) bool { return status == "APPROVED" }
