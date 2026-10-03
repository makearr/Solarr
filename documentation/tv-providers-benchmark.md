# TV metadata providers, measured (2026-10-03)

Forty random series of the real library, asked from this host (`scripts/bench-tv-providers.py`). Latency is per request, sequential;
throughput is the whole sample at the given number of requests in flight.

| Provider | Median | p90 | 1 in flight | 4 | 8 | 16 | Errors |
|---|---|---|---|---|---|---|---|
| Skyhook (Sonarr's TheTVDB proxy) | 0.23 s | 1.27 s | 3.3 req/s | 17.4 | 24.4 | 29.1 | none |
| TMDB | 0.40 s | 0.51 s | 3.1 | 15.1 | 25.6 | 36.6 | none |
| TVmaze | 0.46 s | 0.65 s | 2.3 | 7.6 | 13.4 | 17.8 | 404 where TVmaze has no show |
| TheTVDB v4 direct | 0.52 s | 0.63 s | 2.0 | 7.1 | 16.0 | 24.4 | none |

No provider refused at 16 in flight, so the spacing between request starts is the limit: Skyhook 100 ms, TMDB 25 ms, TheTVDB 50 ms; TVmaze
stays at 500 ms (its published limit is 20 calls per 10 s).

What each answered (share of the sample with the field):

| Field | Skyhook | TVmaze | TMDB | TheTVDB |
|---|---|---|---|---|
| Overview, genres, network | 100 % | 100 % | 95-100 % | 100 % |
| Rating | 100 % | 84 % | 95 % | 100 % |
| Runtime | 100 % | 95 % | 47 % | 100 % |
| Air time (with zone) | 85 % | 58 % (the only one with the zone) | no | 85 % |
| Content rating | 80 % | no | 100 % | 80 % |
| Cast | 80 % | separate call | 89 % | 85 % |
| Clear logo | 65 % | no | 95 % | 65 % |
| Fanart | in images | no | 100 % | in artworks |
| Episodes: title / overview / air date / still | 100 / 95 / 90 / 85 % | separate call | separate call per season | separate call |

So Skyhook is both the fastest and the fullest (it carries every episode in the one answer), TMDB is the second source for cast,
certification, logos and fanart, TheTVDB direct repeats Skyhook more slowly (its extended answer has no episodes), and TVmaze is the
slowest and thinnest but brings the air-time zone. The default order is Skyhook, TMDB, TheTVDB, TVmaze, AniList; it is a setting
(Settings, Metadata, TV series).
