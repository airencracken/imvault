-- Pictures and viewer preferences are optional and independent of uploaded files.
CREATE TABLE user_avatars (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 still BLOB NOT NULL CHECK(length(still) BETWEEN 8 AND 4194304 AND substr(still,1,8)=X'89504e470d0a1a0a'),
 animation BLOB NOT NULL DEFAULT X'' CHECK(length(animation)=0 OR (length(animation) BETWEEN 6 AND 4194304 AND substr(animation,1,6) IN (X'474946383761',X'474946383961')))
);
CREATE TABLE avatar_preferences (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 animate INTEGER NOT NULL CHECK(animate IN (0,1))
);
