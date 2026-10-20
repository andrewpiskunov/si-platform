-- +goose Up
CREATE TABLE engine_owner (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    epoch bigint NOT NULL
);
INSERT INTO engine_owner (epoch) VALUES (0);

-- +goose Down
DROP TABLE engine_owner;
