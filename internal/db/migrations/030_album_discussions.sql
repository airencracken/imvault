CREATE TABLE album_discussions (
 album_id INTEGER NOT NULL REFERENCES albums(id) ON DELETE CASCADE,
 url TEXT NOT NULL CHECK(length(url) BETWEEN 1 AND 4096),
 PRIMARY KEY(album_id,url)
);
