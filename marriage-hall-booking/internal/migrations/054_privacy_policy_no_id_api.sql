-- Remove id exposure: PUT and DELETE no longer use an {id} path param.
-- The id column is kept internally in the database for integrity,
-- but is not exposed in the API response.
-- No schema changes needed; this migration is a no-op placeholder that
-- documents the API contract change.
SELECT 1;
