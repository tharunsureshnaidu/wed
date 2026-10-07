-- Store rejection reason directly on the booking so customer dashboard and booking
-- endpoints can display owner-provided rejection reasons without subqueries.
ALTER TABLE bookings ADD COLUMN IF NOT EXISTS rejection_reason TEXT;

-- Backfill existing rejected bookings from booking_status_history if any.
UPDATE bookings b
   SET rejection_reason = h.reason
  FROM (
      SELECT DISTINCT ON (booking_id) booking_id, reason
        FROM booking_status_history
       WHERE to_status = 'REJECTED'
       ORDER BY booking_id, id DESC
  ) h
 WHERE b.id = h.booking_id AND b.status = 'REJECTED' AND b.rejection_reason IS NULL;
