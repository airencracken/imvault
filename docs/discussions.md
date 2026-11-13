# Album discussions in Witmoot

Operators: set `IMVAULT_WITMOOT_URL=https://board.example.org` and
`IMVAULT_BASE_URL=https://photos.example.org`, then restart. Imvault does not
contact Witmoot at startup. The two apps share no database or sign-in, and
Imvault never creates a thread on its own.

## Starting a discussion

When signed in, choose **Prepare a Witmoot discussion** on an album page.
Imvault sends only the album link: no title, cover, count, description or files.
In Witmoot, pick a board or an existing topic, check who can read it, and post
as usual. Sign in to Witmoot first, or reopen the draft after signing in.

People still need permission to see the album and each file in it.

## Discussion links

Album owners can paste an existing discussion address under **Album
discussions** and choose **Link discussion**, or choose **Remove link**. Only
people who can see the album see the links. Deleting an album deletes its links
but leaves the Witmoot threads and the files alone. A Witmoot outage never
affects the album.

## Album previews for Witmoot

Witmoot can fetch a preview of an album with
`GET /api/v1/albums/{ref}/preview`, using the album owner's API key. A public
album returns its title, slug, visibility, public image count and address. The
count leaves out private, members-only and expired files, and videos. Private
and members-only albums return 404 with no details. Witmoot checks who can read
the post before fetching, and discards anything that is not public.
