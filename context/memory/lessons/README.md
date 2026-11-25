# Lessons (memory)

One file per domain: `<domain>.jsonl`, one JSON object per line:

    {"lesson": "…", "evidence": ["<event id or trail session id>"], "harness_version": "…"}

The context compiler adds the last 5 lessons for the request's domain to the
per-turn part of the prompt. The V4 reflector appends here; a human may too.
