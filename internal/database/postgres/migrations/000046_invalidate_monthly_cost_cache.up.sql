-- Invalidate the recommendations cache so pre-PR-#254 rows (which stored
-- monthly_cost as a literal 0 from the old float64 struct field) are
-- flushed. After this migration runs, the next call to
-- ListRecommendations triggers a synchronous cold-start collect that
-- re-populates the table with correct monthly_cost values:
--   - nil  (JSON null)  for providers/services that don't expose it
--   - 0    (JSON 0)     for all-upfront commitments (no recurring charge)
--   - >0               for AWS RI/SP recs (actual recurring monthly cost)
--
-- TRUNCATE is safe because this table is a cache: the scheduler
-- re-fetches all data from cloud APIs on the next collect. No user-
-- created or non-reproducible data lives here.
TRUNCATE TABLE recommendations;

UPDATE recommendations_state
   SET last_collected_at = NULL
 WHERE id = 1;
