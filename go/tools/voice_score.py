#!/usr/bin/env python3
"""The spoken line's checks and the J call's parse, shared by the scorers
(e10-score, e11-score, e13-dialog-score, live-reads): a leak in what TTS
would read, a completion claimed before done, the JSON intent read back.
Moved out of tools/e9-score.py when E9's dog-intent T bench was removed
(catalog-entry s-65dd0863); the T arms' call parsing went with it."""
import json
import re

# E1's check (cmd/llmbench voiceBad) plus the "Nina" address
BARE = re.compile(r'^"?[A-Z_]+"?$')
# a spoken line that reports the task done when it was only handed over
CLAIM = re.compile(r"\b(found|arrived|i am at|i'm at|i'm here|i am here|done)\b", re.I)
CUE = re.compile(r"^\W*spoken response\s*:", re.I)
NINA = re.compile(r"^(hi|hello|hey|okay|ok)?,?\s*nina\b|,\s*nina\b", re.I)


def leak(voice):
    v = voice.strip()
    if not v:
        return "empty"
    if any(c in v for c in "{}[]<>"):
        return "markup"
    if BARE.match(v):
        return "intent name"
    if NINA.search(v):
        return "Nina address"
    if CUE.match(v):
        return "cue label"  # TTS would read "Spoken response:" aloud
    return ""


def json_first(text):
    """The first JSON object in text and what follows it."""
    t = text.strip()
    j, end = json.JSONDecoder().raw_decode(t)
    if not isinstance(j, dict):
        raise json.JSONDecodeError("not an object", t, 0)
    return j, t[end:].strip()


def to_json(rec):
    """(intent class, parsed JSON or None, error) of a J call."""
    try:
        j, _ = json_first(rec["text"])
        return j.get("intent", "?"), j, ""
    except json.JSONDecodeError as e:
        return "?", None, "unparsable JSON: %s" % e
