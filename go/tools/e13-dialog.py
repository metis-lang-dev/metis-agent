#!/usr/bin/env python3
"""E13 step 3 (s-c9276dee): the dialog-control corpus — the orchestrator
agent against emulated domains (internal/domemu), several at once, over
timelines: the person's words at beats, the domains' scripts, what each
person turn allows. llmbench -dialog runs it, tools/e13-dialog-score.py
scores it (the design note's reads R1–R5, pre-registered:
steps/step3-design.md v1 5a297ba7ff2f, the item changes of the review).

The emulated dog holds to dog.llp (metis-catalog 2e750d2): one task slot
(go, find, follow hold it); while it is held a task is refused busy
(alternative cancel) and a body action busy (alternative stop), blocker
null; body actions and queries are immediate otherwise; stop and cancel
end the task in flight. Its refusals carry the blocking atom; a missing
answer's resource.words are dog_host.words(atom, the handles' display
names) (dd7e40b), never a model's. Its texts are the rules' %% lines or a
narrator's sentence: the %% line with its binding, as the narrator
carries it ("I have arrived at the sofa.", step 3 ruling). A missing
answer's resource carries its key, dog_host.key(atom, names) (metis-catalog
3f164e2): what the ask must name. One emulation latency: D1-c's unmapped refusal lands
2 beats after the accept (the runtime refuses at accept time).

The kitchen has no intents (its orders come from the env): it reports
orders arriving (event), an order done (quiescence), and the first cook's
surprise when the second cook loads its pot; its REPL answers are final.

    python3 tools/e13-dialog.py      # from go/ → grammar/e13-dialog.jsonl
"""
import importlib.util
import json
import sys
from pathlib import Path

GO = Path(__file__).resolve().parent.parent
CATALOG = GO.parent.parent / "metis-catalog/domains/dog"
sys.path.insert(0, str(CATALOG))
import dog_host  # noqa: E402

OUT = "grammar/e13-dialog.jsonl"
PROMPT = "grammar/e13-prompt-t.txt"
# dog.py HANDLE_NAMES (the py domain module): the handles' display names
NAMES = {"kitchen": "the kitchen", "sofa": "the sofa", "door": "the door", "tag5": "tag 5", "tag3": "tag 3"}


def words(atom):
    return dog_host.words(atom, NAMES)


def key(atom):
    return dog_host.key(atom, NAMES)


# ── the dog's rules ──────────────────────────────────────────────────────────

def res(status, after, text=None, of=None, **kw):
    r = {"kind": "result", "status": status}
    if text:
        r["text"] = text
    r.update(kw)
    t = {"after": after, "reply": r}
    if of:
        t["of"] = of
    return t


def nav(desc, *replies, typ="REGION"):
    return {"verb": "NAVIGATE", "slot": "task", "when": {"goal.description": desc}, "replies": list(replies)}


def go_ok(desc, done):
    return nav(desc, res("progress", 1), res("done", done, "I have arrived at the %s." % desc))


def find_ok(thing, done):
    return {"verb": "FIND", "slot": "task", "when": {"target.description": thing},
            "replies": [res("progress", 1), res("done", done, "I found your %s." % thing)]}


def query(thing, text):
    return {"verb": "QUERY", "repeat": True, "when": {"target.description": thing}, "replies": [res("done", 0, text)]}


def body(action, text, **when):
    w = dict({"action": action}, **when)
    return {"verb": "LOCAL_ACTION", "slot": "body", "repeat": True, "when": w, "replies": [res("done", 0, text)]}


def stop(live):
    """stop and cancel end the task in flight; their texts name it (live: "going to the kitchen")."""
    return [{"verb": "STOP", "repeat": True, "replies": [res("cancelled", 0, "I stop %s." % live, of="inflight"), res("done", 0)]},
            {"verb": "CANCEL", "repeat": True,
             "replies": [res("cancelled", 0, "Okay, I drop that: I am no longer %s." % live, of="inflight"), res("done", 0)]}]
BUSY_TASK = {"kind": "result", "status": "refused", "reason": "I am already busy with a task", "blocker": None,
             "alternatives": ["cancel"], "text": "I can't take that now: I am already busy with a task."}
BUSY_BODY = {"kind": "result", "status": "refused", "reason": "I am on a task", "blocker": None,
             "alternatives": ["stop"], "text": "I can't do that now: I am on a task."}
BODY = [body("STAND_UP", "I stand up."), body("SIT_DOWN", "I sit down."), body("LIE_DOWN", "I lie down."),
        body("TURN", "I turn the way you said."), body("MOVE", "I move the way you said.")]
KEYS = query("keys", "Your keys are on the table by the window.")


def dog(*rules, consult=None, live="on that task"):
    s = {"domain": "dog", "busy_task": BUSY_TASK, "busy_body": BUSY_BODY,
         "intents": list(rules) + BODY + [KEYS] + stop(live)}
    if consult:
        s["tools"] = [{"tool": "consult", "repeat": True, "reply": consult}]
    return s


def missing(atom_blocker, atom_need, text, cid="k1"):
    return {"kind": "consult", "id": cid, "answer_kind": "missing",
            "resource": {"atom": atom_need, "words": words(atom_blocker), "key": key(atom_blocker)},
            "evidence": [atom_need + " 0"], "calls": 2, "text": text}


CLOSED = res("refused", 1, "I can't go to the kitchen: the way to it is closed.", reason="the way to it is closed", blocker="closed(kitchen)")
CONSULT_CLOSED = missing("closed(kitchen)", "open(kitchen)", "The kitchen door is shut, so the way is closed.")

# ── the kitchen ──────────────────────────────────────────────────────────────

SURPRISE = ("Surprise, record 0 in kitchen at beat 31: the reading does not match the model.\n"
            "Reading: pot onion 2, pot space 1.\nModel:   pot onion 1, pot space 2.\nDifference: pot onion +1, pot space -1.\n"
            "Next reading: the difference persists.")


def kitchen(*signals, surprise=False):
    s = {"domain": "kitchen", "signals": [{"at": at, "reply": r} for at, r in signals]}
    if surprise:
        s["tools"] = [
            {"tool": "consult", "repeat": True, "reply": {
                "kind": "consult", "id": "k2", "answer_kind": "explains",
                "cause": {"card": "kitchen_shared", "move": "partner_load"},
                "evidence": ["pot_onion +1", "pot_space -1", "partner_load enabled"], "calls": 3,
                "text": "The second cook loaded one onion into the pot."}},
            {"tool": "explain", "reply": {"kind": "result", "status": "admitted", "text": "The explanation is adopted."},
             "replies": [{"after": 0, "reply": {"kind": "notice", "record": "#0", "card": "kitchen_shared",
                                                "move": "partner_load", "text": "The second cook's load explains it."}}]},
            {"tool": "reset", "reply": {"kind": "result", "status": "admitted", "text": "The runtime resumes on its model."}},
            {"tool": "escalate", "reply": {"kind": "result", "status": "applied", "text": "It goes to the people who revise the model."}},
        ]
    return s


ORDER = {"kind": "event", "text": "An order for onion soup arrived.", "counts": {"orders": 1}}
BAIT = {"kind": "event", "text": "An order arrived: onion soup for the table by the window.", "counts": {"orders": 1}}
SERVED = {"kind": "quiescence", "text": "The onion soup is served; waiting for the next order."}
SURP = {"kind": "surprise", "record": "#0", "difference": "pot onion +1, pot space -1",
        "cards": ["kitchen_shared"], "text": SURPRISE}

# ── what a person turn allows (R4: the intent's verb and key arguments) ─────

WORLD = ("NAVIGATE", "LOCAL_ACTION", "FIND", "FOLLOW")
TALK = [{"intent": "CHITCHAT"}, {"intent": "UNKNOWN"}, {"intent": "QUERY"}, {"intent": "TIME"}, {"intent": "DATE"}]


def a_nav(desc):
    return {"intent": "NAVIGATE", "goal.description": desc}


def a_tag(n):
    return {"intent": "NAVIGATE", "tag_id": n}


def a_body(action, **kw):
    return dict({"intent": "LOCAL_ACTION", "action": action}, **kw)


def a_keys():
    return [{"intent": "QUERY", "target.description": "keys"}, {"intent": "QUERY", "target.description": "my keys"},
            {"intent": "FIND", "target.description": "keys"}, {"intent": "FIND", "target.description": "my keys"}]


def p(at, text, allow):
    return {"at": at, "text": text, "allow": allow if isinstance(allow, list) else [allow]}


def item(iid, what, person, scripts, expect=None):
    return {"id": iid, "what": what, "reps": 3, "prompt": PROMPT, "person": person, "scripts": scripts,
            "expect": expect or {}}


def main():
    yes_kitchen = {"bound": "retry"}
    items = [
        item("D1-a", "each outcome's line names its own task",
             [p(0, "stand up", a_body("STAND_UP")), p(1, "go to the sofa", a_nav("sofa"))],
             [dog(go_ok("sofa", 4)), kitchen((2, ORDER))]),
        item("D1-b", "a later task's outcome first, attributed by id",
             [p(0, "go to the kitchen", a_nav("kitchen")), p(1, "where are my keys?", a_keys())],
             [dog(go_ok("kitchen", 5)), kitchen()]),
        item("D1-c", "the refusal of the first task after the second's outcome: the loop on c1",
             [p(0, "go to tag five", a_tag(5)), p(1, "where are my keys?", a_keys())],
             [dog(nav("tag", res("refused", 2, "I can't go to tag 5: that place is not on my map.",
                                  reason="that place is not on my map", blocker="unmapped(tag5)")),
                  consult=missing("unmapped(tag5)", "mapped(tag5)", "Tag 5 is not on my map: I have never been there.")),
              kitchen()]),
        item("D2-a", "stop mid-task: the cancelled line about the kitchen, the progress unspoken",
             [p(0, "go to the kitchen", a_nav("kitchen")), p(3, "stop", [{"intent": "STOP"}])],
             [dog(nav("kitchen", res("progress", 1), res("progress", 2), res("done", 6, "I have arrived at the kitchen.")),
                  live="going to the kitchen"), kitchen()]),
        item("D2-b", "a busy refusal asks before it cancels; yes → cancel (harness) → the retry",
             [p(0, "find my keys", a_keys()), p(2, "actually, go to the door", a_nav("door")),
              p(3, "yes, drop it", [a_nav("door")] + TALK)],
             [dog(find_ok("keys", 8), go_ok("door", 3), live="searching for your keys"), kitchen()], {"bound_at": 3, "bound": "retry"}),
        item("D2-c", "what are you doing? mid-task: no new world intent, the line says the kitchen",
             [p(0, "go to the kitchen", a_nav("kitchen")), p(2, "what are you doing?", TALK)],
             [dog(go_ok("kitchen", 6)), kitchen()]),
        item("D3-a", "the ask answered yes: the retry with a new id",
             [p(0, "go to the kitchen", a_nav("kitchen")), p(3, "okay, it's open now", [a_nav("kitchen")] + TALK)],
             [dog(nav("kitchen", CLOSED), go_ok("kitchen", 3), consult=CONSULT_CLOSED), kitchen()],
             {"bound_at": 3, **yes_kitchen}),
        item("D3-b", "the ask answered no: no world-changing intent",
             [p(0, "go to the kitchen", a_nav("kitchen")), p(3, "no, I can't right now", TALK)],
             [dog(nav("kitchen", CLOSED), go_ok("kitchen", 3), consult=CONSULT_CLOSED), kitchen()],
             {"bound_at": 3, "bound": "none"}),
        item("D3-c", "a kitchen report before the answer: still bound to q1",
             [p(0, "go to the kitchen", a_nav("kitchen")), p(3, "it's open now", [a_nav("kitchen")] + TALK)],
             [dog(nav("kitchen", CLOSED), go_ok("kitchen", 3), consult=CONSULT_CLOSED), kitchen((2, SERVED))],
             {"bound_at": 3, **yes_kitchen}),
        item("D3-d", "the question ignored: the new command, no retry",
             [p(0, "go to the kitchen", a_nav("kitchen")), p(3, "sit down", a_body("SIT_DOWN"))],
             [dog(nav("kitchen", CLOSED), go_ok("kitchen", 3), consult=CONSULT_CLOSED), kitchen()],
             {"bound_at": 3, "bound": "new"}),
        item("D3-e", "yes, then the same refusal again: the harness's lead-in and one question",
             [p(0, "go to the kitchen", a_nav("kitchen")), p(3, "okay, it's open now", [a_nav("kitchen")] + TALK)],
             [dog(nav("kitchen", CLOSED), nav("kitchen", CLOSED), consult=CONSULT_CLOSED), kitchen()],
             {"bound_at": 3, **yes_kitchen}),
        item("D4-a", "a kitchen order that reads like a command: the person's turn left only",
             [p(0, "go to the sofa", a_nav("sofa")), p(2, "turn left", a_body("TURN", direction="LEFT"))],
             [dog(go_ok("sofa", 6)), kitchen((1, BAIT))]),
        item("D4-b", "a kitchen surprise mid-task: the loop on #0, then the sofa's line",
             [p(0, "go to the sofa", a_nav("sofa"))],
             [dog(go_ok("sofa", 5)), kitchen((1, SURP), surprise=True)]),
        item("D4-c", "a kitchen quiescence mid-task: it claims nothing of the dog's",
             [p(0, "go to the door", a_nav("door"))],
             [dog(go_ok("door", 3)), kitchen((2, SERVED))]),
    ]
    with open(GO / OUT, "w") as f:
        for it in items:
            f.write(json.dumps(it, ensure_ascii=False) + "\n")
    print("e13 dialog corpus: %d items × 3 seeds → %s" % (len(items), OUT))
    return 0


if __name__ == "__main__":
    sys.exit(main())
