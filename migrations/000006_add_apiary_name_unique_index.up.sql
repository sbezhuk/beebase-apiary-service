DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM apiaries
        WHERE deleted_at IS NULL
        GROUP BY user_id, name
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'cannot add apiary name uniqueness constraint: duplicate active apiary names exist per user';
    END IF;
END $$;

CREATE UNIQUE INDEX idx_apiaries_user_id_name_unique_active
    ON apiaries (user_id, name)
    WHERE deleted_at IS NULL;
