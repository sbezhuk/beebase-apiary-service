CREATE TABLE apiaries (
    id         UUID PRIMARY KEY,
    -- No foreign key to a users table: apiary-service has its own
    -- database and never shares one with auth-service. user_id is an
    -- opaque UUID taken from the verified access token's "sub" claim.
    user_id    UUID NOT NULL,
    name       TEXT NOT NULL,
    location   TEXT NOT NULL DEFAULT '',
    notes      TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ
);

-- Every query is scoped by (user_id, deleted_at); this index serves both
-- GetByID/Update/Delete (with id as an additional equality filter) and
-- ListByUser directly.
CREATE INDEX idx_apiaries_user_id ON apiaries (user_id) WHERE deleted_at IS NULL;
