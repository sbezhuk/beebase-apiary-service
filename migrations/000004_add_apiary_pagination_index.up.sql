-- ListByUser now orders by (created_at, id) and paginates with
-- LIMIT/OFFSET; replace the single-column index with a composite one that
-- covers the WHERE clause, the ORDER BY, and the COUNT(*) query used for
-- pagination metadata.
DROP INDEX idx_apiaries_user_id;

CREATE INDEX idx_apiaries_user_id_created_at ON apiaries (user_id, created_at, id) WHERE deleted_at IS NULL;
