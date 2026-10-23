# A library's maximum rating

A TV or movie library may set a maximum rating (Settings -> Libraries -> Edit): the youngest viewer age a work may
admit, shown with the ratings of that age ("13 (PG-13)"). Plan 10-libraries L2.

## Ages

A certification is read as the youngest age it admits when it is written (`work_certification.age`, from
`screen.RatingAge`): the US film and TV ratings, the UK, Canadian, Australian, French, Spanish, Brazilian and other
codes providers fall back to, and any bare number ("12", "16+", "-16"). `NR` and anything not understood is stored as
-1, unrated. Ages for certifications stored before this were read once at startup. A media type without ratings (music)
offers no limit (`media.Rating`, `MediaTypeInfo.ratings`).

## What the limit does

A work its library does not admit (older than the limit, or unrated unless **Also allow unrated**) is **held**:

- it stays where it is, files and monitoring untouched; nothing is deleted or moved;
- the planner never searches it (`plan.Item.AboveRating`), and an automatic or RSS grab for it sends nothing
  (`dispatch.Automatic`); a person's own grab is not held;
- the library's list shows it under **Above rating**, its page says **Above this library's rating**, and a notice
  counts them per library.

Changing a library's limit, or a refresh changing a work's rating, has the planner look at the items again at once.
Raising or clearing the limit releases them.
