CREATE TABLE IF NOT EXISTS blocks (
    rule_id INTEGER NOT NULL,
    date    TEXT NOT NULL,        -- YYYY-MM-DD
    blocked INTEGER NOT NULL DEFAULT 1,
    reason  TEXT,
    PRIMARY KEY (rule_id, date)
);

CREATE INDEX IF NOT EXISTS idx_blocks_date ON blocks(date);