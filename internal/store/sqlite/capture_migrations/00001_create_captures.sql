-- +goose Up
-- db is the database id of the activity database whose activity row this
-- capture belongs to: ids restart at 1 in every activity database, so the pair
-- (db, id), not the id alone, names a capture. created is the write time, so
-- the budget prunes oldest-first across every activity database sharing the
-- file. data holds the encoded capture blob; size is denormalized for the
-- budget's SUM.
CREATE TABLE captures (
    db      TEXT    NOT NULL,
    id      INTEGER NOT NULL,
    created INTEGER NOT NULL,
    data    BLOB    NOT NULL,
    size    INTEGER NOT NULL,
    PRIMARY KEY (db, id)
);

CREATE INDEX captures_created ON captures (created);

-- +goose Down
DROP TABLE captures;
