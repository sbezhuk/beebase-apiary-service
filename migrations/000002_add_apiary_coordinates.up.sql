ALTER TABLE apiaries
    ADD COLUMN lat DOUBLE PRECISION,
    ADD COLUMN lon DOUBLE PRECISION;

ALTER TABLE apiaries
    ADD CONSTRAINT apiaries_lat_range CHECK (lat IS NULL OR (lat >= -90 AND lat <= 90)),
    ADD CONSTRAINT apiaries_lon_range CHECK (lon IS NULL OR (lon >= -180 AND lon <= 180));
