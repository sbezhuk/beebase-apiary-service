DROP INDEX idx_apiaries_user_id_created_at;

CREATE INDEX idx_apiaries_user_id ON apiaries (user_id) WHERE deleted_at IS NULL;
