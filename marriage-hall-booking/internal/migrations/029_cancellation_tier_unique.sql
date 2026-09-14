-- One refund tier per cut-off. Without this, posting the same cut-off twice
-- created two contradictory rules and the refund amount depended on row order.
DELETE FROM cancellation_policies a USING cancellation_policies b
 WHERE a.ctid < b.ctid
   AND a.facility_id = b.facility_id
   AND a.days_before_checkin = b.days_before_checkin;

CREATE UNIQUE INDEX IF NOT EXISTS uq_cancellation_facility_days
    ON cancellation_policies (facility_id, days_before_checkin);
