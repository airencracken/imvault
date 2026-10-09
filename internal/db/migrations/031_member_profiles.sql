CREATE TABLE member_profiles (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 name TEXT NOT NULL DEFAULT '' CHECK(length(name)<=80),
 bio TEXT NOT NULL DEFAULT '' CHECK(length(bio)<=1000),
 links TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(links) AND json_type(links)='array' AND json_array_length(links)<=5)
);
