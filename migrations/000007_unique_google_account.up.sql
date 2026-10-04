BEGIN;

-- a Google account can be bound to one user only: otherwise new sign-ups
-- with the same Gmail would get a fresh trial. Duplicates keep the user who
-- connected first; the others lose the grant and must connect another Gmail.
DELETE FROM tokens t
USING tokens first
WHERE t.google_sub <> ''
  AND first.google_sub = t.google_sub
  AND first.id < t.id;

CREATE UNIQUE INDEX idx_tokens_google_sub ON tokens (google_sub) WHERE google_sub <> '';

COMMIT;
