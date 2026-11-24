# Safe mode

An instance flag for a second Solarr that watches a library another instance manages. `safe_mode: true` in `config.yml`, or `SOLARR_SAFE_MODE=true`, read at start; it is not a setting and Settings cannot change it.

While it is set these never run, whatever their stored settings say (nothing is rewritten, so removing the flag restores the instance as it was):

| What | Where it is refused |
|---|---|
| Transcoding | the dispatcher, the analysis and every file conversion (`transcoder.Service.SafeMode`) |
| The automatic search (hunt) | its budget is zero (`searchesPerHour`) |
| The RSS sync | the job does nothing (`unlessSafe`) |
| The cutoff check | the job does nothing (`unlessSafe`) |
| Sending monitoring to an *Arr | the scheduled push, "Send now", and the companion add (`ArrSync.SafeMode`) |

`GET /settings/general` reports `safe_mode`; the Transcode, Priority, Scheduler and *Arr connection pages show a banner. Safe mode is independent of [Solarr mode](solarr-mode.md): both apply.
