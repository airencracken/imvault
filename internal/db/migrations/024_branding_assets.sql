CREATE TABLE branding_assets (
    name    TEXT PRIMARY KEY CHECK (name IN ('favicon', 'mascot')),
    content BLOB NOT NULL
);
