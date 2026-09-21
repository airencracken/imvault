-- The descriptive fields read out of a file's metadata, kept so a page can show
-- them without re-reading the original.
--
-- They live on the blob rather than on the file because they are a property of
-- the bytes: two files with the same content have the same camera, the same
-- date, and the same coordinates, and the second one to be uploaded should not
-- pay to read them again.
--
-- Storing them is what lets the two halves of this feature be independent. The
-- bytes served to a public audience carry no metadata at all; the page can
-- still say when a photograph was taken and with what, and mark the location as
-- withheld rather than pretending there is nothing to withhold. The date is the
-- reason a family archive exists, and the coordinates are the reason it is
-- private, and a single setting should not have to choose between losing one
-- and leaking the other.

ALTER TABLE blobs ADD COLUMN details_json TEXT NOT NULL DEFAULT '';
