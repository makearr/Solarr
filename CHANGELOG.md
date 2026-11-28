# Changelog

Version is `YYYY.MM.R.BBBB` (year.month.release.build) from 2026-10-31 (`YYYY.MM.RRR.BBB` before); see `documentation/versioning.md`.
Earlier entries use older schemes; those at and below `0.0.x` predate both and are left as history, unconverted.

## Unreleased
- Built-in reader: a Read button on comic (cbz, cbr, cb7, cbt), EPUB and PDF files opens them in the browser; comics have right-to-left reading for manga, two pages side by side and page turning by keys or the page edges; each user's place in each file is kept and resumed. Schema: new `reading_position` table in library.db (migration 0147, edit in place; recreate a dev library database).
- Book and audiobook series: the collection lists the whole series (Wikidata, completed by Hardcover when its key is set), shows how many books it has in all and which are missing, and Monitor adds them even when Wikidata has no Open Library id for them.
- Settings > Database > Pictures: one image format (WebP by default, JPEG or PNG) for item pictures and video previews; changing it converts the stored ones in the background and new ones are written in it.
- Video hashing and previews read each folder with more files at once only while that folder's disk answers as fast as it normally does, and back off when it slows; broken-file detection still runs before previews and hashes.
- Comics: a series with no provider poster takes its cover from the first page of its first issue (cbz, cbr, cb7, cbt), a page named cover first; a provider poster still wins.
- Series, films and books whose library uses Wikidata keep the Wikidata item, and their page links the English Wikipedia article.
- Lists and posters show the age chip (All ages, 6+, 12+, 16+, 18+) beside the year, and a calendar entry for an episode opens that episode.
- Import: a folder's units are all listed as "Identifying" before the first file is read, so a big import shows its whole batch at once and the identified units replace them as each folder is read. Every media type names its folders from their names alone first: a match of 90% or more shows its work on the "Identifying" unit at once, and the files then confirm it (commit as usual) or disagree (held in Review).
- Users: a maximum age per account (All ages only, up to 6+, 12+, 16+) hides works rated above it from lists, search, detail pages, artwork, people credits and playback; unrated works stay hidden unless a Kids library holds them. Schema: `app_user.max_age` (edit in place; recreate a dev settings database).
- Manage > Statistics: charts, per minute for two days and per hour for 90 days, of Solarr's own CPU, memory and IO, scans and metadata jobs finished per minute, each folder's disk latency and throughput, internet bandwidth and latency per provider, and each library's items, files and storage. Schema: new `metric_point` table in statistics.db (migration 0010).
- Solarr measures its own disk calls per folder and its requests per provider, with no probe traffic: Settings > Disk shows how each folder answers against its usual speed, and scans and file checks start fewer jobs at once while a disk runs well below its normal.
- A work shows one age chip on a single scale (All ages, 6+, 12+, 16+, 18+) whatever rating system its source uses; adult content is 18+.
- Music libraries have kinds (General, Classical, Soundtracks, Kids, Live and DJ mixes), audiobook libraries gain Audio drama, Lectures and courses, Non-fiction and Podcasts, book libraries gain Light novels, Non-fiction and reference, Magazines and Adult fiction (hidden from restricted users); Discover keeps an item no library rule claims out of a specialised library (documentaries, anime) when a general library of the type exists; Users is its own entry under Manage, linked from User settings.
- Storage > Folders: a folder also reserves a percentage of its size (default 5%), the larger of that and the absolute reserve blocks writes, and the bytes the library holds on the folder show left of the usage bar. Schema: two columns on `root_folder` (edit in place; recreate a dev settings database). Connections: an *Arr connection offers only the libraries of its own media type.
- Crossovers: an episode in an event opens that episode in its series (the season opens and the row is marked), shows its picture when the file is present, and episodes whose videos look alike are linked as the same broadcast.
- Crossovers of a library list only events with a part in that library; a new root folder starts read only on a read-only filesystem and with renaming allowed where Solarr may write; the Disk page tells active watches that are not yet trusted from none; Needs attention no longer lists paused metadata providers nor "automatic searching is off" in observe and *Arr companion modes; absent NVENC reads "not present on this host"; the API key name field no longer suggests a name.
- Setup assistant: testing a Radarr or Sonarr v3 connection reads the languages its profiles ask for; they start as the chosen languages when none were chosen yet, otherwise the Languages step offers them in a banner.
- *Arr import: the naming scheme of Sonarr, Radarr, Readarr, Whisparr and Lidarr can be taken over per library (ticked in the preview and in the setup assistant's review) with the translated templates and an example name; a token Solarr has no counterpart for is dropped and named instead of refusing the whole template.
- Discover: Add automatically is one switch per library; it takes the best rated of all the library's enabled lists up to the daily limit, and the preview shows the same order. Schema: the per-list switch is gone.
- Libraries: a Status filter in the toolbar (Any, Present, Wanted, Downloading, Not yet released) next to the Status sort; the Watched filter moved into the Filter menu.
- Setup assistant: the step names in the stepper jump to that step, imports bring unmonitored items by default, a new app gets its own name (numbered when taken), a choice to run beside the *Arr apps (chosen) or replace them, and an Extras step to switch on Discover, duplicate-video hashing, collections, broken-file detection and transcoding with what each costs.
- Fresh installs: the Nord colour scheme, RSS sync on, "Search for what is added" in Discover on, import history kept 30 days, and the optional features (duplicate-video hashing, broken-file detection, collections) off until you switch them on.
- Music: Wikidata is a metadata provider of music libraries and asked first; music prefers AAC over Vorbis. Video profiles aimed at 1080p now rank 2160p above 720p. New quality targets prefer "over" and ignore everything below the target.
- Content profiles for ebooks, comics and manga; Jellyfin servers can be linked to audiobook libraries.
- Dashboard: "Needs attention" can be hidden with its X and returns when something new needs attention.
- Settings, Transcode: the lanes follow the hardware until you set limits of your own (shown as "Auto (n)"): one Video job, Video HW as many as encode engines, Audio and Books half the cores, fewer while someone watches; the hardware card shows the CPU cores the container may use.
- Download clients, Indexers, Arr apps and Media servers are one "Connections" entry under Manage (People leaves the sidebar, its pages stay); Quality, Metadata and Transcode settings are shown at the Expert level.
- The setup assistant is rebuilt: a stepper with Back, Next and Skip in the same place, a start choice (moving from an *Arr or starting fresh), app discovery and a Test button that names what is wrong, several apps each with their own import choices and folder mapping with a folder browser, language presets, a starting quality per library, a Review of everything before anything runs, and a Finish screen with live import progress and a checklist (indexer keys, imported download clients, health). Where you stopped is remembered on the server; Settings -> General -> Run setup assistant opens it again and only adds.
- New setup-assistant endpoints: a connection test that names the app and version or says what to fix (key, port, address), a look around for apps on their usual addresses (Lidarr, Sonarr, Radarr, Readarr, Whisparr, Prowlarr, SABnzbd, qBittorrent), and import previews that count the indexers, download clients and lists an import would add.
- *Arr import: indexers, download clients and the new Import lists option are ticked by default and copied before the library walk, so a quick test on a big library sees them at once; the setup assistant shows each started import with its progress, errors and notes (for example which indexers still need a key).
- Command palette (Ctrl+K or /) replaces the header search box: every page and setting by name, actions (add to a library, rescan, switch theme), and a search of your items and people; it only lists what your account may open.
- The notification bell opens a list of what needs attention and what failed lately, with "See all".
- Keyboard shortcuts: g d, g l and g s go to the Dashboard, the first library and Settings, j / k / Enter walk the rows of a list, and ? lists them all.
- Every settings card keeps its help behind a "?" next to the title (hover, focus or tap), and a setting that saves when you change it confirms with a "Saved" toast.
- Bulk Monitor / Unmonitor: Undo now restores only the works that actually changed, so works that were already monitored are not unmonitored by it. A change to more than 5000 works asks first and offers no Undo.
- Web UI preferences: Comfortable or Compact density for lists and poster grids.
- One page header for the settings and configuration pages, with breadcrumbs (Library, artist, album) on an item's page; empty lists show an icon, a sentence and the next step.
- Every page now opens with the same header (lists, jobs, logs, calendar and people included), and Compact density also tightens table rows.
- Settings pages save by themselves a moment after you stop typing, with a "Saved" tick; bulk Monitor / Unmonitor applies at once with an Undo; irreversible actions ask in one shared dialog.
- Dashboard: "Needs attention" and the system health checks are for administrators only (the server refuses everyone else), an administrator gets a "Finish setting up" checklist (library with root folder, indexer, download client, metadata key) that disappears when done or when hidden, each person can show, hide and reorder the dashboard's sections under Web UI, and the libraries are one slim row each.
- Discover: every recommendation card names the library it would be added to, and its Add and Not interested buttons are always visible (touch works); Not interested shows an Undo toast. The Discover page is one feed with All, per-library and Hidden chips, and the dashboard row is exactly two rows of cards with no sideways scrolling.
- Failed actions and actions that say what they did now show a toast with the server's detail instead of only a line in the status bar.
- Navigation: the sidebar is grouped into Browse, Libraries and Manage; each library lists only its levels, and its Recommended, Wanted, Insights, Files, Watched and Settings are a tab row above its pages. Connections and Storage moved into Settings, which has four collapsible groups (Media, Connections, Storage, System). User profile, Web UI preferences and the theme are in the user menu. The sidebar badges show only what needs action (downloads, unidentified files, health issues), and on a narrow screen it is a drawer opened from the header.
- Books, audiobooks and comics: when a title exists in several language editions (one shared rule), the one the file says it is in (an epub's language, a language tag in its name, a comic's ComicInfo) is chosen, then the one in your preferred language. For comics: when a title exists in several language editions, the one the file says it is in (its ComicInfo language or a language in its names) is chosen, then the one in your preferred language, before the provider's first hit; a title that language settles is no longer held back as ambiguous.
- Settings, Transcode: the lanes are called Video, Video HW and Audio, and comics and ebooks share one Books lane; a comic or ebook target no longer warns that it "names no bitrate".
- Comic and epub files are written with maximum Deflate compression.
- Import lists: book and audiobook libraries can add from one of your own Hardcover lists (read only, with the Hardcover token), music libraries from a public MusicBrainz collection by id or link (through the same MusicBrainz rate limit as the rest of Solarr); the help points to MDBList for Letterboxd lists. A list that needs a key now says "a key or token" instead of naming TMDB.
- Settings, Discover: the classic age, auto-add's minimum rating and votes, its daily ceiling and the search-after-add switch are one setting for the whole instance (in the first card) instead of one per library.
- Collections folded into one keep the name of the one with the most works, then the one with more provider ids, the lowest id only last.
- The old per-target transcode setup page is gone; the setup lives in the quality target's editor.
- Transcode converts ebooks: a book library whose target names epub gets its text and html files written as epub in pure Go, and its other formats through pandoc when that is installed (the `-full` image variant, docker build --build-arg WITH_PANDOC=1, carries it); the result replaces the original through the recycle bin like any conversion. Schema note: the transcode kind lists gained `ebook` in the library and statistics databases.
- Comics: the Grand Comics Database gets two minutes per request, since a title search there can take almost a minute.
- Comics: a series is found whatever the accents (a folder without them matches the catalogue's accented title), so a folder of comics whose name lacks the accent is identified instead of waiting in Review.
- The web interface opens one live-update stream per browser, not one per tab: with many tabs of the same instance open, the first tab reads the stream and passes it to the others, so the page no longer freezes behind the browser's six-connection limit.
- Transcode: the parallel limits are ceilings down the lanes CPU, GPU, audio, comics, ebooks (CPU 1, GPU 2, audio 4 allows one CPU and one GPU job beside two audio ones), comics and ebooks have a lane of their own, and a limit under an earlier lane's is refused.
- Transcode: the way files are converted to a quality target is set in that target's editor (Quality profiles, Target qualities); Settings, Transcode keeps the libraries, hardware, lanes and originals. The hardware card counts the video engines of Intel and AMD GPUs from the kernel instead of saying "engine count unknown".
- Discover: manga libraries are recommended from AniList again (the English-only original-language default rejected every title), and Western comic libraries apply no original-language rule.
- *Arr companion: a film added from a collection (or any list that names it as a TMDB film) now reaches Radarr like one added from Discover, a series is found by the TheTVDB id the work holds, and a Whisparr scene is sent whichever stash-box it came from (taken by its exact title when Whisparr has no id for it).
- Import lists now exist for movies (TMDB lists, MDBList lists with their IMDb imports), music (a ListenBrainz user's top albums) and books and audiobooks (a public Open Library list).
- Backups: Backup now, Download backup (the newest bundle) and Delete old backups (after checking the newest) are buttons at the top of the page.
- Discover: the list loads more by itself as you scroll, reading one page ahead; Settings, Discover is shorter: short labels with the explanation on hover, the auto-add settings appear only when a list adds automatically, and the "last read" lines are gone (failures still show).
- Person pages: the titles under "Also known for" open the item's page, as collection cards do.
- People: an adult performer is never joined to a same-named music artist or actor any more, and persons already joined wrongly (an artist carrying a performer's id, picture and credits) are split apart by the "Match people" background task.
- Collections: collections two providers made of one franchise (nearly the same members) are folded into one by the collection linking task, keeping every provider id on it.
- Comics and manga: the keyless providers (Grand Comics Database, MangaUpdates, MangaDex) are on from the start, so a new comic library identifies its files without any setup; ComicVine and Metron stay off until you give a key.
- Discover, Collections: unfinished collections now recommend the parts a library lacks altogether (as the collection page lists them), not only works already known.
- Settings, General: Run now buttons for collection matching, recommendations and broken-file detection (the "Files" card is now "Detect broken files"); Hash videos is greyed out, ticked and explained while preview pictures are on (also on the Duplicates page).
- Dashboard shows Discover above the newly added items (five instead of ten); each library lists Discover (was Recommended) above Collections.
- Discover: the first time Discover is switched on (Settings, General or Settings, Discover) it is enabled for every library at once; a library you switch off afterwards stays off, and later switch-ons do not turn it back on.
- Ignore: every Ignore button now shows Ignored with Stop ignoring after a reload, and stopping the ignore of a work you hold puts its monitoring back as it was before; the contents list on an unadded item's page scrolls in place instead of offering Show more.
- Comics and manga: a cbz, cbr, cb7 or cbt archive that cannot be read, or holds no page image, is marked defective when it is scanned and listed under Files, Defective beside the other broken files.
- Ignore works for things you already hold too: an Ignore button on library cards and rows, the item page, the person page and crossover rows leaves the item out of every list that offers things as missing (wanted, collections, crossovers, search, Discover); a work is left unmonitored, nothing is deleted, and an administrator may ignore for everyone. Crossover series the library lacks now link to their provider page.
- The page of an item the library lacks (from Discover, search, collections, crossovers) now shows what its providers know, for every media type: facts, genres, ratings, cast and crew, and the parts in the type's grouping (a series' episodes by season, an album's tracks by disc, a comic's issues), beside Add and Ignore.
- Items the library lacks (Discover cards, search results, collection members) open a page of their own with the provider's description, Add and Ignore.
- Ignore: an Ignore button on search results, collection members and crossover parts (administrators may ignore for everyone) keeps an item you do not want out of Discover, collections and crossovers; it is the same list as Discover's "not interested", and the library is never touched.
- Duplicates (music): an empty report now says how many audio files still wait for their fingerprint, so you can tell "no duplicates" from "not looked yet".
- Files, Defective: the scan now finds empty, unreadable and cut-short files (the end of each video and audio file is decoded; switch under Settings, General), lists them with the reason, and offers bulk Re-request (recycle bin, wanted again, searched) and Delete.
- Settings, Transcode: the hardware card names the GPU and shows how many encode and decode engines it has where they are known (NVIDIA cards in the shipped table), otherwise says "engine count unknown".
- Comics and manga now use Wikidata as a metadata source (switch it on under Settings, Metadata): summary, genres, original language, creators and start year fill what the comic providers leave empty, found by the series' Comic Vine or MyAnimeList id.
- Organisation pictures: publishers (books, audiobooks, comics), studios, networks and labels without a logo now get one from Wikidata by name, in a daily background task ("Find organisation logos" under Settings, Tasks); music labels with a MusicBrainz id keep using their own task.
- Choose image: the picture in use is always shown first in the dialog, and the empty message now says no provider offers *another* picture.
- Collections without a picture of their own (music, and any type whose provider has none) show a mosaic of their members' covers instead of an empty tile.
- Libraries with Discover on get a "Recommended" entry under their name in the sidebar, opening Discover on that library; it disappears when Discover or that library is switched off.
- Dashboard: when Discover is on but no library uses it, or nothing was fetched yet, the Discover row says so instead of staying empty.
- Adding an item now reaches the connected *Arr (Radarr, Sonarr, Lidarr, Readarr, Whisparr) whenever that connection sends monitoring back, not only in companion mode; in safe mode the add dialog says nothing is sent.
- Settings, General: a switch to hide collections from the navigation and item pages; they are still collected in the background.
- Settings, Providers: a search box over every provider tab (name, host, media type) and a note that a listed provider does nothing until it is switched on.
- Collections: the sort order, favourites filter and search are kept when you leave the list and come back, like every other list.
- Adding an item from search, a person page, Discover, a collection or a crossover now flips its Add button to the in-library state at once, on every screen that lists it.
- Settings, Metadata: a provider that is switched off now says so the same way everywhere, with a link to the switch, instead of "switched off under Providers" next to a button that turns it on in a type order.
- Duplicates: a music (or book, comic) library no longer shows a "Similar videos" block; it appears only in libraries of video types.
- Discover reads every switched-on source of a library's media type instead of one: anime libraries get AniList and TMDB's Japanese animation (before, only AniList while the settings named TMDB), music gets ListenBrainz (weekly charts, upcoming albums) and Deezer (charts) next to Last.fm, ebooks and audiobooks get Hardcover (with your token) and Open Library, adult libraries every stash-box server with a key. A list's size is shared between its sources, a title two sources name is shown once, popularity is scaled per source so the sources interleave, a provider switched off under Settings, Metadata is not read, and Settings, Discover shows each list's state per source.
- Documentation: a shorter README, one page per feature (what it does, why it helps, how it works) with fresh screenshots, a Docker image page, an architecture page, a security policy with private vulnerability reporting, a contributing guide, and briefs that let an AI agent test recognition, benchmark a library or review the code and write a report you can post.
- Readarr: connect a Readarr and import its books and audiobooks (by Goodreads id, with its monitoring, library and quality-profile mapping, download clients and indexers), send monitoring back, and add a book named by its Goodreads id to Readarr when running as its companion.
- Adult: a scene waiting in Review or Unidentified shows each suggested hit with its date and studio, as the Import screen does.
- Conversion: a file that was moved or renamed no longer makes its detail show the old path or Restore answer "folder not reachable"; Restore failures are now shown on the page; a blocked file shows why; the "saved" size in the status bar reads like the file lists (5.9 MB).
- Comics: pages that are only downscaled keep their own format (a PNG stays a real PNG instead of WebP bytes under a .png name).
- Comics: a series imported from Kapowarr or Mylar3 that no provider lists issues of arrives monitored when the choice says so; picking a series for a file by hand makes the issue the file names when the series has no issue list; a Grand Comics Database series is found by a year it was publishing in, not only the year it began.
- Comics: a series name search at the Grand Comics Database reads up to three result pages, so a short name whose series is not on the first page is found, both when a series is identified and in the manual search.
- Freeleech counts as a small score bonus: a release the indexer reports as free (or half free) edges out an otherwise equal one. The weight is a "Freeleech bonus" field on each quality profile (default 1, 0 turns it off); it never outranks a better quality tier.
- Comics: a comics library can convert its files like audio and video do (Settings > Transcode): cbr, cb7 and cbt archives become cbz in natural page order with ComicInfo kept, pages are re-encoded to the target's image format and quality and brought down to its page size (never enlarged), and a repack that would save under 10 percent is skipped; the original goes to the recycle bin and can be restored.
- Adult: a hit whose own season and episode numbering (S41:E2) differs from the file's (S41E3) is no longer taken, and a hit that agrees wins a tie between same-titled scenes.
- Adult: a scene file named "Title - S36E29" (a studio's own season and episode numbering) is searched by the bare title, so providers find it; the numbers are kept apart from the title.
- Adult: a library title with a quality word ("UHD", "4K", "HD", "VR", ...) is searched without it, so the exact scene is found; each hit in Review shows its date and studio, and a debug log line ("adult pick") says why a file stayed open.
- A music release whose name states no format is downloaded and probed at import: if its files meet none of the library's quality profiles (MP3 in a lossless-only library), the release is blocklisted with that reason and the next one is grabbed.
- Settings: "Videos hashed at once" and "Duplicate video tolerance" can now be set next to the video-hashing switch (Advanced level); Settings, Libraries, Priority says which level it needs instead of showing blank; a job that safe mode skips writes one "skipped: safe mode" line to the log.
- The quality-change preview now covers the Automatic target: it counts what would fall under or over the cutoff with the target the first profile gives.
- An interactive search in observe or companion mode now says searches are off because of the mode, instead of "no indexer is configured".
- Removing a title from the library now also takes its running downloads out of the download client, so one that finishes later cannot bring the title back.
- Importing a season pack fingerprints its files several at a time (the "Videos hashed at once" setting) and reads the series' held hashes once; the video hash backfill reads three moments of a file with one ffmpeg process instead of three.
- Internal: the outbound limiter lost its unused token bucket, daily cap and global gate; pacing and per-host concurrency are unchanged.
- A series refresh is skipped and retried later while TMDB, AniList or TVmaze fails, if the stored record was built from it, instead of replacing its data with the other sources' answers.
- Video hashing can read several files at once (Settings, Advanced: "Videos hashed at once", default 1, up to 8); a local disk hashes about twice as fast with three.
- A film refresh while TMDB does not answer is skipped and retried later, instead of replacing the stored credits, keywords and pictures with a fallback source's shorter lists.
- Safe mode: `safe_mode: true` in config.yml (or SOLARR_SAFE_MODE) keeps transcoding, the automatic search, the RSS sync, the cutoff check and sending monitoring to an *Arr off without touching any setting; Settings pages for them say so in a banner, and GET /settings/general reports it.
- Discover: a sidebar page (poster wall per library with list, genre and title filters, Add and Not interested) and a dashboard row of recommendations.
- Discover (settings): Settings > General has the switch; Settings > Discover sets what is never recommended and, per library, list sizes, language, rating and vote floors and automatic adding.
- Discover (sources): audiobook libraries recommend from Audible (best sellers, highest rated, upcoming) and adult libraries from StashDB (trending, upcoming) once its key is set.
- Discover (settings): a library with more than one kind of item (artists and albums) can switch each kind off, and Refresh now reads the lists again without saving.
- Discover (fixes): the sidebar entry sits below Calendar, the dashboard row no longer clips its list badges, a plain ?library=2 or ?hidden=1 link opens that library or the Hidden view, and a card without a poster shows its initial.
- Discover (API): the recommendations of each library, a dashboard row, genres, per-person dismissal and undo, and Load more reading the next page from the source.
- Transcode (settings, not yet active): per-target conversion setups, per-library switches and lane limits can be read and saved through the API and are part of the configuration export; nothing is converted yet. The hardware page data (which encoders this machine has, per codec) is read from ffmpeg at start. A file-by-file analysis (what a conversion would do, and why a file needs none) is in place but nothing runs it yet.
- Transcode (settings page): Settings > Transcode has the switch, where each library's files stand (with Retry failed), the space saved, per-library priority and order, lane limits, the originals kept and the detected hardware.
- Transcode (setup editor): each library's target opens a form for how its files are converted (engine with unavailable ones greyed out and why, quality or bitrate, skip rules, streams, filters, result checks, second pass), with the warning block, Reset to defaults and Copy from another target.
- Transcode (file list): Settings > Transcode > File list shows every judged file with its state and the reasons in words, filters by state, library and path, retries a selection (with other quality, bitrate, preset or engine, and the count first), restores an original, and opens a file's plan; the pipeline numbers link into it.
- Transcode (API): summary, per-file list and detail, retry (with other settings, selected or by filter, with a preview count), restore of the original and "try now" for a paused kind. Administrators only; no screen yet.
- Transcode (fixes): a converted file stays finished and restorable when its library is analysed again; a Run now on the task starts the conversions it finds; the file list shows every file's size, tries after a second pass and a plain Retry; errors and the hardware table read as sentences; the status bar names a conversion; the log records each conversion's start, pass outcome and failure; a file's details show a plan read from the file now.
- Transcode (details): the Transcode page warns when a setup's bitrate or its finished files land outside the target's range, shows how many originals are kept and when the first expires, and says when library folders are held back for want of space; the setup editor has the extra skip codecs and the copy-up-to percentages; a file converted by Solarr says so on its work page (expert level) with Restore original and the date it is kept until.
- Transcode (activity): Scheduler shows the file a job is working on, and History lists every finished conversion.
- Discover (page): the poster wall is virtualised, Not interested offers Undo, a Hidden view lists what you hid with Restore, a work you hold but do not monitor gets Monitor, request-only accounts see Request, and Settings > Discover has word chips for exclusions and shows when each list was last read and why it failed.
- Discover replaces the TMDB trending and popular import lists, and the unused discovery jobs are gone. On upgrade a library that had such a list gets Discover switched on with Trending adding automatically when the list was enabled (popular becomes trending; the rating and vote floors now apply), the lists are deleted, and this cannot be undone.
- Discover: music libraries recommend artists and albums from Last.fm (needs your Last.fm key): what is trending, and what is alike the artists and genres the library holds most of; Last.fm has no ratings (that gate is skipped) and gives albums no listener count, so automatic adding of albums needs the vote floor at 0.
- Discover: ebook libraries recommend from Open Library (trending this week, best rated, classics); comic and manga libraries read AniList's manga charts; audiobooks and adult libraries have no feeds yet.
- Discover: anime libraries read AniList charts (trending, top rated, upcoming, classics) instead of TMDB.
- Discover: automatic adding. After each refresh a library adds recommendations that pass its rating and vote floors (most popular first, up to the daily limit, never one it added before); switching it on for a list first shows what it would add now and why it holds the rest back.
- Discover: an instance switch (Settings > General) and per-library settings (list sizes, rating and vote floors, exclusions, daily auto-add) for recommendations, saved from the API and carried in the configuration export and import; the recommendations themselves are refreshed daily.
- Comics: an issue that arrives as a folder of page images (jpg, png, webp, avif, jxl, gif; at least three, cover first, natural order) is packed into one cbz and imported as that file; a download's folder is left as it was, a folder you imported by hand goes to the recycle bin once the cbz is in the library.
- Metadata language: one setting under Metadata providers (default English) makes TMDB, Skyhook and Wikipedia answer in your language; an overview comes from that language's Wikipedia article (found through Wikidata) where the library's order lets Wikidata supply it, and English is used where a provider has nothing in your language.
- TV: the anime id map (Fribb) is now on by default; switch it off under Metadata providers.
- Refresh now works on a series a scan added that has no episodes yet (it answered "not found"), and a provider definition that answers a JSON list fills genres and tags from its elements.
- Work page: a series, film, book, comic or scene whose metadata so far came only from your own media server shows a "From your media server" chip; it goes once another metadata provider of its library answers a refresh (music never carries it: albums always come from MusicBrainz).
- Comics: a creator (writer, artist, ...) can be followed; their series at ComicVine are added monitored, new ones from the year of the follow on (needs a ComicVine key; not yet tried against the live service).
- Import: Kapowarr and Mylar3 can be connected like an *Arr and their comic series imported by ComicVine id (monitored or paused/ended, placed by the folder they live in) -- built from their public API, not yet tried against a real instance. Schema note: the connection table is rebuilt in the unreleased settings migration.
- Comics and books: Kavita can be a local metadata source (series summary, genres, credits, publisher and the provider ids in its links), ranked after every online provider and only filling what they lack.
- Books: BookBrainz is offered as a metadata provider (works, authors, description, language; keyless, not in the default order).
- Music: an optional per-library Lyrics setting writes synced (.lrc) or plain (.txt) lyrics from LRCLIB beside tracks that have none, once a day, never over a file you put there.
- Webhooks: a webhook with an invalid address or event now answers a clear 400 naming the problem instead of an internal error.
- TV: an anime id map (Fribb, one file read once a day) finds an anime's AniDB, MyAnimeList and AniList ids from its TheTVDB id.
- Music: the cover picker also offers Apple Music's large cover for an album with a barcode (keyless; a switch under Metadata providers).
- Media servers: Stash is told of new files (a metadata scan of their folders); Emby and Komga can be connected (scan push; Emby also metadata and watch state) -- both built from their public API, not yet tried against a real server.
- Your own provider definition files can now enrich music artists (photo and biography where none) and fill adult-library scenes (title, studio, tags, pictures, credits).
- Comics: a series known only to the Grand Comics Database now gets its issue list (variants left out, up to 60 issues a refresh, on the provider's own pace).
- Your own provider definition files can now fill a TV library's series (title, overview, genres, rating, pictures, cast; never episodes) when ordered in its sources.
- TV: each episode row shows its own IMDb score (read from OMDb's season lists when an OMDb key is set; one request per season on a refresh). Library database gains `rating` and `votes` on episodes.
- TV and movie pickers offer your Jellyfin server's posters and backdrops (never as the default over an online source); they are fetched with the connection's own sign-in and cached.
- Configuration export and import carry your webhooks (outgoing and incoming) and your own provider definition files, never an address, a secret or a token; an imported webhook starts switched off until its address is set.
- Manga: a volume release is no longer turned away because its edition year (a reprint) differs from the series' first year.
- Comics: a release named "Complete" or "Collection" counts for every part still wanted; history and queue rows say what a comic release holds (#12, v03, c120, Complete).
- Books and audiobooks: a scan names a book by the ISBN or ASIN your Audiobookshelf holds for its folder when the file name and tags give none.
- New library and the Also holds list: media types are listed by their own name (TV and Comics were both "Series").
- Indexers: the add list marks an indexer defined by your own spec file ("Your own file") and a shipped one your file replaced.
- A part wanted on its own (one volume, one track) now starts with the file the library already holds for it, and starts with its media type's quality profile when the library has none of its own (a manga volume got the music profile).
- Comics: a volume or issue found by a scan now also counts for the item that wants that volume on its own (it stayed missing, so a cutoff never compared it); the old bytes of a cbz that gets ComicInfo.xml written go to the recycle bin first; an import triggers the Kavita and Komga files pass for its series.
- TV: a series page shows every provider's score as its own chip (OMDb's IMDb rating included) instead of only the first source's.
- Download client definitions can sign in with a login request (the session cookie is kept) and read XML and HTML answers, not only JSON.
- Comics: an edition page has a Covers dialog to say which issues a collected edition holds (a series and a range), the library list filters by Format (the Trades view), and the Kavita and Komga files setting counts how many files it would write before it is switched on.
- Watched: the library tree shows how many works are ready to clean up beside the Watched entry (refreshed by the watch-state sync); a non-administrator who opens the page is told it is administrator-only instead of an error.
- Webhooks: `file_recycled` now fires when a file goes to the recycle bin (it never did for the bin itself).
- Comics: an issue held in a collected edition shows "In the collected edition" with a link to it on the series page.
- Webhooks: the form shows the body of the chosen format for an example event, and `GET /webhooks/{id}/pending` lists what still waits for a webhook (event, attempts, next try).
- Music scan: an album folder whose files carry no MusicBrainz tags is named by the ids your Jellyfin server holds for it (before any title search); the identification detail shows the pass.
- Download clients as files: a definition file (`engine: http`, usenet) describes how to talk to a client's HTTP API; once approved in Settings > Providers it is offered beside the built-in clients. Solarr still implements no client itself.
- Webhooks out (API): an administrator adds webhooks with a preset (generic JSON, Discord, Slack, ntfy, Gotify, Apprise), the events they take and the libraries; deliveries go through an outbox with backing-off retries and an HMAC signature, and the address and secret are sealed. Schema note: new settings and library tables (dev databases need recreating).
- Metadata providers as files: e-Hentai, ExHentai (your own session cookies) and nhentai (experimental, through your own FlareSolverr) ship switched off as definition files and are offered to Hentai libraries only; two ports of compiled clients prove the engine against the Go code.
- Your own metadata providers (definition files) now also work for books, audiobooks and movies; adult-only definitions are offered to adult libraries only; their answers are cached as the file says. A custom descriptor can narrow a provider's media types, name indexer mirrors and set a request delay.
- Fixed: the library Metadata page failed to load once a custom provider without a feature list was offered; a custom provider now shows its Custom chip in the unused list too, and a provider switched off by a custom descriptor no longer appears in Settings > Metadata.
- Connections > Webhooks: add, edit, test and remove outgoing webhooks (events as grouped checkboxes, libraries, file paths, adult libraries) and incoming webhooks (a token shown once, curl lines and the Jellyfin Webhook plugin fields for scan, import, refresh, watch state and search). Outgoing webhooks now also tell of grabs, failed downloads and imports, renames, additions and removals, requests and health changes. Schema note: new settings table (dev databases need recreating).
- Settings > Providers: list every provider definition (metadata, indexers, download clients, webhook presets) with its origin and state, edit a custom file with its problems listed by line, copy a built-in one to custom, approve the hosts of a custom provider before it runs, test a metadata provider, and revert or remove a file (it moves to custom/.removed).
- Provider definitions: an active metadata definition file now joins a comic library's provider order (picker marked Custom); a definition that is gone or broken shows as no longer available and is skipped.
- Provider definitions: the engine behind a metadata provider that is only a YAML file (JSON, HTML or XML answers read into one generic record, at most eight requests an operation, only the declared hosts). It is not offered to libraries yet.
- Provider descriptors: every built-in metadata provider, indexer kind and download client is written out as a YAML descriptor under `providers/`; a custom file with the same key can switch it off for new use (hides it from the add lists and a library's provider list), lower its request pace and narrow its media types.
- Indexers: the built-in indexer specs are written out as YAML under `providers/indexers/builtin/` in the config folder, and a custom spec in `providers/indexers/custom/` (same key replaces a built-in) is used once an administrator has approved its hosts.
- Picture pickers: a series or movie also offers its fanart.tv logos and banners; an album also offers its Deezer cover (found by barcode when a release has one). Books: an Open Library match that names an Internet Archive item reads that item directly.
- Music: the album and artist image pickers also offer fanart.tv covers, disc art, artist photos, backgrounds, logos and banners (when fanart.tv is on and has a key).
- Fixed: Deezer ratings missed albums whose plain edition is not among the first search hits; a local media server's score now reads as the server's name on a work.

- Calendar: the Pull list (and the calendar's dated parts in general) now lists issues and volumes of comic libraries; it showed only TV episodes before. A release whose size cannot be its named parts (a stub, a bundle) is now rejected with its reason instead of silently dropped.
- Comics: a file holding a range of volumes is named for both ends (v04-05), and number padding pads both.
- Comics: a manga or hentai library says volumes where a comics library says issues, and a comic file shows its page count, median page size and image format beside its size.
- Comics: a library can write what Kavita and Komga read (File Management > Library options > Kavita and Komga files): series.json beside each series and ComicInfo.xml inside each cbz, adding what is missing (fill) or replacing it. A file is rewritten only when the result differs. Off by default.
- Comics: a library chooses its date type (store date or cover date) and whether a collected edition it holds satisfies the issues it collects (those are then not searched for and show as held in the edition); a ComicRack reading list (.cbl) can be imported on the Story arcs page: matched issues are kept in order as one list. Libraries gained a generic "Library options" section for settings a media type declares.
- Comics: a download of cbz/cbr files is no longer unpacked into its pages before import; a Manga library names its series folders without a year and starts on the Manga default target.
- Calendar: a Pull list view (comic libraries only, weeks starting on Wednesday).
- Comics: four shipped CBZ quality targets (any page size, SD, HD, manga HD) with a page size the median page of a file must reach (a file whose pages were not measured is never held against it), comic quality profiles (CBZ preferred, Any comic, High resolution), a Pages section in the quality target editor, and Rename now names comic files by the library's naming scheme. The "also the specials" box shows only for TV.
- Comics: wanted issues, annuals, volumes and chapters are searched (Newznab comic and book categories) with the series title and the number as releases print it, in the hunt, the RSS sync and interactive search; a release must name the series and a wanted part, alone or in a range; the cutoff check keeps the better file of a part.
- Comics: downloads of comics and manga are imported (issues, volumes and chapters named by file, folder or ComicInfo, ranges, placed below the series' folder and named by the library's naming scheme), files waiting in Review can be assigned to a series, and a series you removed with "do not add it back" is no longer re-added by the next scan.
- Metadata: MyAnimeList and AniDB can add an anime's score once you store a client id or client name under Settings > Metadata (both off by default).
- Metadata: Last.fm, Discogs and Spotify can add an album's popularity or rating once you store your own key under Settings > Metadata (all off by default). Jellyfin's people become cast and crew of a series or film when no other provider has any, and a series keeps the TheTVDB id its folder names.
- Metadata: ListenBrainz and Deezer add how popular an album is (listeners and fans, shown as a rating beside MusicBrainz's) when a music library's albums are enriched; both need no key and can be switched off under Settings > Metadata.
- Metadata: IMDb ratings from IMDb's own daily download (switched off; no key, no quota, personal and non-commercial use) fill the rating of films and series, and outrank the IMDb number MDBList and OMDb give; switch it on under Settings > Metadata and Run now under Scheduler.
- Media servers: a connection a library's metadata order still names can no longer be deleted by mistake, and connecting a media server as a metadata source takes effect at once.
- Comics: a new media type with four library kinds (Comics, Manga, Adult comics, Hentai) that can be created and are empty for now; the two adult kinds ask for a confirmation before the library is added. Under the hood: the file-name parser, ComicInfo.xml / MetronInfo.xml / series.json readers, the page probe and clients for ComicVine, Metron, the Grand Comics Database, MangaUpdates, MangaDex and AniList manga are built but not yet connected.
- Comics: ComicVine, Metron, the Grand Comics Database, MangaUpdates and MangaDex appear under Settings > Metadata (switched off until you turn them on; ComicVine takes your key, Metron your account as user:password); AniList also serves comic libraries. Comics libraries default to ComicVine, Metron, GCD; Manga and Hentai to AniList, MangaUpdates, MangaDex.
- Comics: Add series in a Comics, Manga, Adult comics or Hentai library: search the providers of the library, pick Sonarr-style monitor options (all, future, missing, first, latest, none), series and their issues or volumes are stored, and a daily refresh keeps ongoing series current. Manga volumes come from MangaDex's chapter map or the volume count AniList and MangaUpdates give; chapters outside any volume are listed but not wanted. A result an adult-flagged provider marks is offered only to the adult kinds.
- Comics: a scan of a Comics, Manga, Adult comics or Hentai library now reads each archive (cbz, cbr, cb7, cbt, pdf: page count, page size, image format, embedded ComicInfo.xml / MetronInfo.xml), finds the series (an id in the file first, then its name and folders, then the providers; added unmonitored when sure, else queued in Review with candidates), and records which issues, volumes or chapters each file covers, including ranges such as 001-006 and v01-v03. A chapter file counts toward the volume MangaDex lists it in; a chapter no volume holds stands alone.
- Comics: a series page groups its parts as Issues, Annuals, Specials, Volumes and "Chapters not yet in a volume", names untitled parts ("Volume 3", "Issue #1"), and a manga whose volume count no provider states gains the volumes its files name. See documentation/comics.md.
- Comics: adding a manga or comic whose parts no provider lists yet still puts it in its library; MangaDex's "erotica" rating (given to much mainstream manga) no longer counts as adult, only "pornographic" does; the add dialog shows the type's monitor options.
- Development moves to branch `dev`; `main` on GitHub stays at the last release.
- Metadata: OMDb (IMDb rating, English plot) and fanart.tv (clear logos, banners, posters) now also serve TV libraries, last in the default order; media servers used as metadata will rank after every other provider.
- Metadata: a Jellyfin connection used as metadata fills a TV or film library's missing fields (overview, genres, cast, age rating, ids) and names files and folders it already knows during a scan; it ranks after every other provider and never replaces an id.
- Metadata: an Audiobookshelf, Calibre or Stash connection used as metadata fills a book, audiobook or adult library's missing fields (found by ASIN, ISBN, Calibre identifier or stash-box id, else by exact title); ranked after every other provider, never replacing an id.
- Settings > Metadata lists the media servers used as a source of metadata (kind, libraries, state, link to the connection); a library's Metadata page marks such a provider and says it is asked first and replaced by the others where they know better.
- Metadata: the Internet Archive fills a book's missing description, subjects and cover by ISBN, last in the book order (no key).
- Metadata: Kitsu (no key) adds anime synopsis, age rating, average rating, poster and the MyAnimeList and AniDB ids, after the other providers in TV libraries.
- Connections > Media servers: connect Jellyfin, Navidrome, Audiobookshelf, Kavita, a Calibre content server or Stash with a tested address and credential, and map Solarr libraries and paths to the server's. Settings schema 0078 adds the connection tables. Telling servers about new files, local metadata and watch state follow.
- Media servers: Jellyfin, Kavita and Stash sign in with a user name and password as well as an API key (Jellyfin may have an empty password); the session is kept and renewed when the server refuses it.

- Media servers: after Solarr places, moves, renames or recycles files, connected Jellyfin, Audiobookshelf, Kavita and Navidrome servers are told (targeted where the server can, one library scan otherwise); Jellyfin can also show "Now available" to people watching. Library schema 0147 adds the queue.
- Media servers: watch state is read from Jellyfin, Audiobookshelf and Navidrome every 30 minutes (or on demand) and kept by work and part. Work tiles and rows show a check or an eye, the work list filters by watched, in progress or not watched, and Libraries > Watched lists what everyone has played so its files can be moved to the recycle bin (optionally unmonitoring the works).

## 2026.11.2.1311 — 2026-11-22

**Upgrading:** each database is one squashed baseline again. Only databases from build 1298 or later (schema
library 0146, settings 0077, jobs 0010, statistics 0008) are upgraded. Anything older, **2026.11.1.0882 included**, is
refused at start-up: use the *Arr import or start with a fresh config folder. The automatic move of the old
`data/{db,cache,backups}` layout and the old browser theme setting are gone too.

- **Indexers without Prowlarr.** Our own indexer spec engine with a **581-site catalog**, a proxy list with
  FlareSolverr, indexer priority and per-indexer download client binding, categories read from the indexer itself,
  and stated API allowances respected (back-off on a limit answer, one sign-in shared by parallel searches).
  Prowlarr can still be followed: its indexers, proxies and clients sync hourly.
- **Disk-aware I/O.** Settings → Disk shows the disk topology and latency; probing, tag write-back, moves, linking and
  imports wait their turn per disk, idle disks are left alone, and inotify folder watching lets scans trust it.
- **Scheduler.** Pause and resume any task (or all), a *Running now* list with time left and estimated load, a
  heatmap of real run time, a hung-job watchdog, and a task that missed its time while Solarr was down runs once.
- **Imports.** Blu-ray/DVD folders are one unit held for review; Calibre `metadata.opf` and EPUB metadata name a book;
  audiobook tags lead the folder name; disc subfolders, subtitle folders and hidden pregap tracks are handled;
  every failing step takes back what it placed; the Import screen pages, has a *Needs attention* tab and dismisses
  all finished sessions in one click. How long import history is kept is a setting.
- **Music.** Bootleg-only release groups per library, optional tracks, release detail (edition names, disc titles,
  track artists, ISRCs), duplicates by **audio fingerprint**, community ratings, vote-weighted genres, Lidarr import
  takes over naming formats and metadata profiles.
- **TV.** Daily series searched by air date, a season missing a few episodes searched episode by episode, several
  downloads of one season in parallel when they do not overlap, specials leave the missing count, and seasons that two
  sources number differently are shown on the work page.
- **Downloads.** A title can be wanted by two libraries; a dead `.torrent` falls back to its magnet; BitTorrent v2
  hashes; a full client disk keeps downloads waiting instead of blocklisting them; a release an indexer stopped
  serving is blocklisted as *gone at indexer*; renamed and blackhole downloads are still recognised; a removed
  download is only declared gone after ten minutes.
- **Similar videos** are compared by a nightly job and stored, so the report opens at once; a pair is compared only
  when their lengths agree.
- **Safety.** Deleting anything in use is refused or offers to move its users first; a fresh install claimed from a
  public address needs the setup code from the log; access levels per action (add, manage) in one table; files of a
  blocked work are a 404 everywhere; passwords of 8+ characters asked twice; credentials never follow a redirect to
  another host; API writes trigger a database copy to disk when running from RAM.
- **Faster and lighter.** A metadata refresh that finds nothing changed writes nothing; library covers sit in 256
  shard folders; one event stream per tab; JSON answers gzipped; provider outages serve the stored answer;
  backups and vacuum no longer hold the database writer; the job dispatcher no longer polls.
- **Health** names unreadable files (with ffprobe's message), missing media tools, failing tasks, stale backups,
  disabled clients still holding downloads and a missing AcoustID key.
- **The whole interface is translatable**: every sentence comes from one i18n catalogue (English only for now).


## 2026.11.1.0882 — 2026-11-08

**Upgrading:** databases created by 2026.10.001.515 or earlier are refused at start-up (one baseline migration per database now); import from the *Arrs or start a fresh config folder.

- **Books and audiobooks are real libraries.** Metadata from Open Library, Google Books, Hardcover, FantLab, iTunes and
  Audible (with Audnexus), covers, one collection per series and the books a series still lacks, **Monitor** on a
  whole series, **follow an author**, and a **Narrators** level, filter and column for audiobooks.
- **An adult library type** (optional) over the stash-box providers, ThePornDB and Whisparr's metadata: scenes and
  films, performers, and a StashDB favourites import list. See [adult](documentation/adult.md).
- **Import lists for TV.** An AniList season or user list, a TMDB list or TMDB's trending and popular shows add series
  for you, **previewed before you enable them**. See [import lists](documentation/import-lists.md).
- **Import from the \*Arrs by choice.** A preview and mapping step, then pick what to bring across; Whisparr is a
  source too, and monitoring can be **sent back** to the \*Arr.
- **Merge one library into another** of the same media type, previewed like a move.
- **Favourites.** A per-user heart on works, parts, people, organisations and collections, with a Favourites filter.
- **A preview player in the browser**: bar, corner window or full screen, source bitrate, quality menu and **GPU
  transcoding**. See [media player](documentation/media-player.md).
- **Similar videos report** across one library or all: perceptual hashes of the frames plus duration, keep one and
  link or recycle the others. Hashing is **off by default**. See [duplicate videos](documentation/duplicate-videos.md).
- **More download clients and indexers**: every client Lidarr and Sonarr offer, **Soulseek through slskd**, and
  **Prowlarr's catalog** to choose indexers from.
- **Download queue insight**: the dashboard says when the queue will be empty, a deferred release says what it
  waits for, each client's average speed is shown, a search-budget view shows what the last hour's searches were spent
  on, and the Add form shows where a request ranks against the automatic searches.
- **Libraries choose whether existing files are renamed**: never, on request, or automatic. Replacements are
  recorded and shown side by side. Move jobs can be paused, resumed and cancelled.
- **TV**: searches and imports by TheXEM's scene numbers, episode titles in the library's profile languages, air moments on the
  calendar, subtitles that follow the file that replaces an episode, a richer add form (root folder, series type,
  season folders, tags) and a franchise watch task.
- **Per-library music provider order**, label, network and organisation logos, and **people and collections editable
  by hand**; one human is one person across every source.
- **Cache and memory page** (*Settings → Cache*) with one database cache ceiling, a Shrink button, and an optional
  **cache and databases in RAM** (tmpfs); **statistics** per indexer and files below each library's cutoff,
  and health that names a paused indexer or download client.
- **Settings by environment variable**: config folder, port, root folders and API keys; `_FILE` variants for Docker
  secrets. See [environment variables](documentation/environment-variables.md).
- **Deduplication.** Similar videos are found at five levels across all libraries (hash version 2: time-based sample
  windows, black-bar crop, scene cuts), with delete on every file, a frame and 3x3 preview sheet per file, and
  hashing switched off by default.
- **Requests.** Users with request permission file requests an administrator approves (or an *Add* grant that is
  approved at once); tags can be hidden per account and **blocked** library-wide.
- **Target quality.** Libraries aim at a named target with shipped presets, a per-work target, closeness judged
  against it, and a preview of what a change makes upgradable.
- **One verified backup bundle** (`tar.zst` of the four databases and `config.yml`) and a Settings → Database page
  with disk use, a keep rule and maintenance by hand.
- **Held releases** have a size setting (default 200), and torrents can wait for both ratio and time with a hard stop.
- **Documentation** split into beginner, advanced and expert levels, with a getting-started page.
- **Hardening.** The first-run claim race is closed, cross-site writes are refused, a session cookie is `Secure`
  behind a trusted TLS proxy, 6to4 and IPv4-compatible addresses are judged by the IPv4 they carry, symlinks in a
  download are never followed, a torrent whose name is a path never becomes an import path, and a whole download
  folder is never imported as one download.
- **Faster.** Scans read directories ahead of the walk, bulk edit writes only what changed, album decisions are about
  3x faster, release-name parses are remembered, RSS sync skips a feed that answers 304, and the queue asks all
  download clients together.


## 2026.10.001.515 — 2026-10-25

**First public release — early alpha.** Use at your own risk, and please report problems as issues.

- Music, TV, anime and movies in one engine: a successor to Lidarr, Sonarr and Radarr, with any number of libraries, mixed libraries, rating limits and library suggestions
- Hunting within a budget, RSS, upgrade hunting, held releases and free-space gating; SABnzbd and qBittorrent; Newznab/Torznab indexers searched in parallel
- Evidence-based recognition of any folder at any depth, packs and archives, previewed and revertible in one click
- Quality judged from the measured file; ties keep the incumbent; remove-and-block by content hash
- Several metadata providers per library type, in a per-library order, tied together by Wikidata; collections and franchises
- Import from Lidarr, Sonarr, Radarr and Prowlarr (read-only)
- Privacy: no telemetry; the optional public-IP check is now off by default
- Images for linux/amd64 and linux/arm64 on Docker Hub (`makearr/solarr`) and ghcr.io; version scheme `YYYY.MM.RRR.BBB`


## 0.0.22 — 2026-09-04

- Add root folder placement policy and section 11.7's Resolve engine


## 0.0.21 — 2026-09-04

- Add the naming template engine, live-verified against the real library (todo 4.5)


## 0.0.20 — 2026-09-04

- Generalize quality-profile targets beyond bitrate (ADR-40 amendment, todo 4.3)


## 0.0.19 — 2026-09-04

- Wire the measured half of ADR-39 attributes (todo 4.2)


## 0.0.18 — 2026-09-04

- Port Lidarr's release-name parser and quality vocabulary (ADR-18, todo 4.1)
- Bump build to 0.0.17 (internal)
- Add an artist column and configurable, sortable library columns
- Add person list/detail pages and redesign the work detail view
- Bump build to 0.0.16 (internal)
- Add the person read/subscribe API (ADR-32 phase 5)
- Expose credits, genres, description and on-disk status on GET /works/{id}
- Add work.description, sourced from the release group's own Wikidata QID
- Add internal/wikidata and wire real biographies into EnrichJob
- Wire artist credits and genre tags into EnrichJob
- Add artist/credit/genre data and a library.People resolver (ADR-32/33)
- Serve and render cover art (section 21.6, closing the loop)
- Bump build to 0.0.15 (internal)
- Fix internal/parse and SearchIdentifier bugs found by live verification
- Bump build to 0.0.14 (internal)
- Add MusicBrainz text search and wire ADR-24's parse strategy (section 10.2)
- Bump build to 0.0.13 (internal)
- Wire the artwork pipeline into a job handler (section 21.6)
- Bump build to 0.0.12 (internal)
- Add POST /api/v1/score/simulate (F6, section 13.2)
- Validate a quality profile's real shape on write, not just its JSON syntax
- Bump build to 0.0.11 (internal)
- Bump build to 0.0.10 (internal)
- Implement internal/score: ADR-40 quality/language interpreter
- Add internal/artwork resize-and-encode pipeline and Cover Art Archive client
- Bump build to 0.0.9 (internal)
- Add internal/match: section 10.2's weighted-distance identity matcher
- Bump build to 0.0.8 (internal)
- Round-trip quality and language profiles through config export/import
- Declare internal/artwork in the import allowlist
- Declare internal/score and internal/match in the import allowlist
- Bump build to 0.0.7 (internal)
- Add make binfmt: register qemu emulators before a multi-arch image build
- Add quality and language profile schema (ADR-40, section 19.8)
- Bump build to 0.0.6 (internal)
- Add internal/parse: stage 4's ADR-18 music release-name parser + golden corpus
- Bump build to 0.0.5 (internal)
- Collapse the sidebar to icons below 1100px (section 21.8)
- Bump build to 0.0.4 (internal)
- Trim comments to CLAUDE.md's one-line rule before merge
- Add GET /api/v1/works/{id}: read-only work detail page
- Bump build to 0.0.3 (internal)
- Log a startup warning when ADR-30's external tools are missing
- Bump build to 0.0.2 (internal)
- Add GET /api/v1/library/unidentified: F29's manual identification queue
- Add category counts: the sidebar's other, deliberately-deferred half
- Add GET /api/v1/search: ranked, category-filtered title search
- Make the MusicBrainz metadata cache staleness-aware (ADR-29)
- Mark vanished library paths MISSING after a full sweep (section 10.4)
- Add GET /api/v1/categories and wire it into the sidebar and library filter
- Bundle fpcalc and ffprobe in the runtime image (ADR-30)
- Replace the Library placeholder with the real browse page
- Add GET /api/v1/works: the library list endpoint
- Add work_root_folder: a temporary bridge for category filtering
- Wire ADR-29's provider into real edition/work_part enrichment
- Add internal/musicbrainz: ADR-29's provider, both sources
- Add cache/metadata.db: a generic, disposable metadata cache
- Fix env overrides misreading YAML-special leading characters
- Populate a placeholder edition from the release MBID
- Add ADR-15's backup rotation, wired in for real
- Benchmark the scan batch write path at ADR-22 scale
- Add the scan.Consumer: files on disk become identified works
- Persist scan progress and add the first job handler (section 10.4)
- Add internal/scan: the section 10.4 mtime sweep, resumable
- Add the embedded-MBID identifier: identity with no network call (ADR-29)
- Add internal/mediatool: ADR-30's tools, and settle what stage 3 needs
- Recover from a corrupt database at startup, and reconcile after (ADR-15)
- Add backup retention (ADR-15)
- Add discovery_job (ADR-38) and wire it into Invariant 41
- Add the request table's query surface (ADR-14, stage 12)
- Add counter reconciliation logic
- Add internal/metaresolve: field-level provider priority (ADR-8, Inv. 13)
- Add internal/model: ADR-23 extension types and the ADR-24 Identifier chain
- Update benchmarks.md now that FTS5 search exists
- Add FTS5 search over titles and aliases (migration 0010)
- Add event_log and retention_state to jobs.db, decision_rollup to library.db
- Verify the image can supply ADR-30's tools, and qualify the 0.2 milestone
- Give the list's view state to the router, and a DOM to test it with
- Compose the list query instead of writing its filters as no-ops
- Size the page cache per database, and stop pretending the driver is a setting
- Checkpoint the log on purpose instead of by accident
- Measure the ADR-28 targets against a 500k corpus, and settle ADR-5
- Add user management to the interface
- Require an account at setup; open access stays a settings decision
- Unstick the create-account button, and make the login requirement a policy
- Loosen passwords, add themes and icons, and report the egress address
- Fix the API base URL, and build the shell around a dashboard
- Drop history.db: three databases, not four
- Stop interning what is unique per file
- Give parts an identity, and fix the three defects the validation found
- Cut SQL comments to one line, and move the prose to documentation
- Complete library.db: collections, ready queue, artwork, statistics, reputation
- Add people, organisations, tags and subscriptions
- Add candidates, decisions, provenance and recycle bin
- Add desired state, the work-to-file link, the hunt queue and counters
- Plan eleven media types, and add classification and image attributes
- Add the physical layer: objects, paths and interned attributes
- Close five open stage 1 points
- Create library.db and the identity schema
- Tidy doubled references left by the comment pass
- Cut every remaining comment block to one line
- Cut comments back to one line, and add TZ
- Make PUID and PGID authoritative, and say so when they cannot be
- Add F9 config export/import, and stop enforcing host permissions
- Add useLibraryList(), the headless core both library views render
- Add the first-run wizard: claim the install, add a library folder
- Run as `media` 1000:1000, overridable with PUID and PGID
- Make the image work on a first `docker run`, and a fresh config directory
- Add the web interface shell, embedded in the binary
- Implement the generated ServerInterface, and test routing against the spec
- Apply environment overrides on the first run, not just later ones
- Give 405 its own problem type instead of reusing bad-request
- Generate the Go types and TS client from api/openapi.yaml
- Correct the versioning rules and tighten the build context
- Create credential files 0600 instead of world-readable
- Refuse to commit local-only working documents
- Record that database placement is the operator's responsibility
- Add a codebase map to the wiki
- Add the event bus and the SSE stream
- Add cursor pagination and the jobs listing endpoint
- Make multi-arch image builds work
- Add the Dockerfile, healthcheck subcommand and compose example
- Add authentication: passwords, API keys and trusted proxy headers
- Add the HTTP server and a working serve command
- Add job workers, the executor and the dispatch loop
- Add the durable job queue
- Add the authz access context
- Add settings.db, the sqlc stack and the in-memory config cache
- Add secrets: encryption at rest and a type that cannot leak
- Add first-run config.yml creation and key generation
- Add startup directory checks and `config check`
- Add environment overrides for config.yml
- Add config.yml loading and validation
- Add migrations and the pre-migration backup
- Add the priority write queue
- Add the store connection layer
- Add the httpx circuit breaker and Client
- Add the httpx rate limiter
- Add the httpx SSRF guard
- Use '_' when a name contains a path separator
- Add the fsx filesystem seam and path-safety choke point
- Add project skeleton and the clock seam
- Publish to GitHub on every build
- Version by milestone and build, not by commit
- Record deployment and toolchain decisions
- Add Makefile wrapping Go and Node toolchains in Docker
- Support Servarr metadata proxies as preferred, never required
- Adopt generated-code stack: sqlc, OpenAPI spec-first, chi, slog
- Adopt AGPL-3.0-or-later
- Add documentation wiki with coding standards
- Add versioned git hooks and CI workflow
- Rewrite README around functionality
- Ignore local-only CLAUDE.md working context

