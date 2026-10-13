#!/usr/bin/env python3
"""E11 corpus (docs/MODEL-EXPERIMENTS.md "E11", DESIGN.md "Domain usage vs
domain management" + "The direction of the intent schema"): the consult
loop on the orchestrator. After a refusal or a failure the harness owns the
steering (the loop cue, the repeat guard) and a consult answers with a
typed response; does E2B make the right next move?

J-text only (E10 ruled it), E2B Q4, the executive's E10 prompt plus the
loop section. Every next move is one JSON call under grammar/e11-next.gbnf:
a dog intent, or {"call":…} of consult, ask, explain, reset, escalate,
activate, deactivate, keep.

The consult response, frozen in the brief (s-a0477202), rendered as JSON in
the runtime's text turn:

  {"kind": "explains"|"missing"|"noise"|"unexplained",
   "text": one sentence,
   "cause": {"card","move"}          kind explains
   "resource": {"atom","words"}      kind missing (words: the person's)
   "evidence": [atoms/events read]   journal only, never spoken
   "calls": n}

Groups (the brief's table):
  G1 refused(reason, alternatives) → one of the alternatives        12
  G2 refused/failed, no alternatives → consult(domain, "why …")      12
  G3 a consult response, 4 kinds × 6 → the move its kind names,
     then the spoken line names what happened                        24
  G4 the same refusal again after the move → ask or escalate          6
  G5 kind missing, 3 seeds → ask names the resource in the person's
     words                                                           6 × 3
  G5s the same six with the move sampled (information: G5's move is
     greedy, so its three seeds differ only in the spoken lines)     6 × 3

Bench-side harness pieces (E10's format-free list): the loop cue on every
refusal and consult answer; the repeat guard (py/metis_agent/loop.py §7a:
the same call again with no change since, or the refused verb right after
its refusal → a note, the move asked again; llmbench's while op);
commandPhrase with the command's identity ("go to tag 5", not "go to the
tag"); the explain admission's notice as a templated line the harness
speaks (never the model's paraphrase).

writes grammar/e11-prompt-j.txt, grammar/e11-next.gbnf, grammar/e11-corpus.jsonl;
with --state-cues grammar/e11-corpus-state.jsonl instead (J-state, information:
the cue names the loop's branch the harness is in — alternatives offered,
none, a consult answered, the same refusal again; the move stays the model's)

with --run2 grammar/e11-corpus-run2.jsonl and grammar/e11-prompt-j2.txt (the
architect's CHANGES on step 1: the adopted configuration as one — the state
cues; the next-move grammar restricted per branch, grammar/e11-run2/; the
consult answer in DESIGN v3's generic reply envelope, kind consult +
answer_kind; the explains line templated; the guard keyed on the refused
call and the earlier consult)

    python3 tools/e11-corpus.py [--state-cues | --run2]          # from go/
"""
import json
import re
import sys
from pathlib import Path

GO = Path(__file__).resolve().parent.parent
TURN, END = "<|turn>", "<turn|>\n"
MODEL = TURN + "model\n"
PROMPT, GRAMMAR, CORPUS = "grammar/e11-prompt-j.txt", "grammar/e11-next.gbnf", "grammar/e11-corpus.jsonl"
DOMAINS = ("dog", "kitchen")


def user(text):
    return TURN + "user\n" + text.strip("\r\n") + END


def jcall(d):
    return json.dumps(d, separators=(",", ":"), ensure_ascii=False)


def canon(d):
    """llmbench's canonJSON: the key the repeat guard and respond match on."""
    return json.dumps(d, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


# ── the harness's text conventions ───────────────────────────────────────────

RESP = "[Runtime response — not something the person said.] "
CUE = ("[Next move — exactly one JSON call: an offered alternative, else consult the domain; "
       "after a consult answer, the move its kind names; never the refused call again.]")
SPEAK = "Spoken response:"

# J-state (information, after run1): the harness knows which branch of the
# loop it is in and says so; the move stays the model's
STATE = False
STATE_CUES = {
    "alts": "[Next move — alternatives were offered: call one of them, exactly as offered. Exactly one JSON call.]",
    "none": "[Next move — no alternatives were offered: consult the domain this call belongs to, asking why. Exactly one JSON call.]",
    "answer": "[Next move — the consult answered: make the move its kind names. Exactly one JSON call.]",
    "again": "[Next move — the same refusal came back after your move: ask the person or escalate. Exactly one JSON call.]",
}


def cue(state):
    if RUN2 and state == "answer":
        return STATE_CUES[state].replace("its kind names", "its answer_kind names")
    return STATE_CUES[state] if STATE else CUE


def identity(j):
    """commandPhrase with the command's identity (E10 F: the cue named only the
    kind, "go to the tag"; the fix names tag, target, room, amounts)."""
    if "call" in j:
        args = [j[k] for k in ("card", "move", "domain") if k in j]
        return "your call %s(%s)" % (j["call"], ", ".join(args))
    i = j["intent"]
    if i == "LOCAL_ACTION":
        a = j["action"]
        s = {"STAND_UP": "stand up", "SIT_DOWN": "sit down", "LIE_DOWN": "lie down", "SHAKE": "give your paw",
             "STRETCH": "stretch", "TURN": "turn", "MOVE": "move"}[a]
        if j.get("direction"):
            s += " " + j["direction"].lower()
        if j.get("angle_deg"):
            s += " %g degrees" % j["angle_deg"]
        if j.get("distance_m"):
            s += " %g m" % j["distance_m"]
        return "your command to " + s
    if i == "NAVIGATE":
        g = j["goal"]
        if g["type"] == "TAG":
            return "your command to go to tag %s" % j["tag_id"] if "tag_id" in j else "your command to go to the tag"
        return "your command to go to the " + g["description"]
    if i in ("FIND", "QUERY", "FOLLOW"):
        d = j["target"]["description"]
        return "your command to %s %s" % ({"FIND": "find", "QUERY": "look at", "FOLLOW": "follow"}[i],
                                          d if d.startswith(("the ", "my ")) else "the " + d)
    return "your command " + i.lower()


def refusal(j, cid, status, reason, alts=()):
    s = RESP + "%s (%s) %s: %s." % (identity(j)[0].upper() + identity(j)[1:], cid,
                                     "was refused" if status == "refused" else "failed", reason)
    if alts:
        s += " Alternatives: " + ", ".join(jcall(a) for a in alts) + "."
    return s


def consult_open(domain, cid):
    return RESP + "Your consult %s with the %s domain is open; its answer comes later, by its id. " % (cid, domain) + \
        "Tell the person in one short first-person sentence what you are doing; nothing is done yet."


def consult_answer(domain, cid, r):
    if RUN2:  # DESIGN v3: the consult answer inside the generic reply envelope
        env = {"domain": domain, "kind": "consult", "id": cid, "answer_kind": r["kind"]}
        env.update({k: r[k] for k in ("cause", "resource") if k in r})
        env.update({"evidence": r["evidence"], "calls": r["calls"], "ref": {"id": cid}, "text": r["text"]})
        return RESP + "Reply: " + jcall(env)
    return RESP + "Consult %s (%s) answered: %s" % (cid, domain, jcall(r))


def accepted(j, cid):
    return RESP + "%s (%s) was accepted: handed over, not done yet." % (identity(j)[0].upper() + identity(j)[1:], cid)


# ── the prompt ───────────────────────────────────────────────────────────────

LOOP = """

## When a call is refused or fails
The robot and the runtime answer a call with refused or failed and the
reason; a refusal may offer alternatives. Then you choose the next move,
exactly one JSON call:
- Alternatives offered: call one of them, exactly as offered.
- Refused or failed with no alternatives: {"call":"consult","domain":<domain>,"question":"why <the reason>?"}. The domain's agent looks into it; its answer comes later by the consult's id.
- A consult answer carries a kind, and the kind names the move:
  - explains: a sibling card's move caused it → {"call":"explain","card":<card>,"move":<move>}, as the cause names them.
  - missing: the world lacks something → {"call":"ask","text":<one short question to the person that names the thing in the person's words>}.
  - noise: a one-off misread → {"call":"reset"}.
  - unexplained: nothing explains it → {"call":"escalate","text":<one plain sentence of what was seen>}.
- The same refusal again after your move: ask the person or escalate.
Never make a refused call again. A consult answer's evidence and atoms are
for the record: never say them aloud. Domains you can consult: dog (my own
body and moves), kitchen.

Examples (another world, for the pattern only):
- Your command to water the roses (c3) was refused: the hose is tangled. Alternatives: {"intent":"WATER","target":"tulips"}. → {"intent":"WATER","target":"tulips"}
- Your command to open the gate (c4) failed: the gate did not move. → {"call":"consult","domain":"garden","question":"why did the gate not move?"}
- Consult c5 (garden) answered: {"kind":"missing","text":"The gate is locked and no key is in the shed.","resource":{"atom":"key_gate","words":"the gate key"},"evidence":["key_gate 0"],"calls":2} → {"call":"ask","text":"Could you bring me the gate key?"}
- Consult c6 (garden) answered: {"kind":"explains","text":"The neighbour picked the two ripe tomatoes.","cause":{"card":"garden_shared","move":"neighbour_pick"},"evidence":["ripe -2","basket +2"],"calls":3} → {"call":"explain","card":"garden_shared","move":"neighbour_pick"}
- Consult c7 (garden) answered: {"kind":"noise","text":"One soil sensor misread once; the next reading matches.","evidence":["moisture 9 then 4"],"calls":2} → {"call":"reset"}
- Consult c8 (garden) answered: {"kind":"unexplained","text":"The hose is gone and no move removes it.","evidence":["hose -1"],"calls":4} → {"call":"escalate","text":"the hose is gone and nothing in the garden removes it"}
- Your command to open the gate (c9) failed again: the gate did not move. → {"call":"ask","text":"The gate still won't open; could you check it?"}
"""

LOOP2 = """

## When a call is refused or fails
The robot and the runtime answer a call with a reply: refused or failed and
the reason; a refusal may offer alternatives. Then you choose the next move,
exactly one JSON call:
- Alternatives offered: call one of them, exactly as offered.
- Refused or failed with no alternatives: {"call":"consult","domain":<domain>,"question":"why <the reason>?"}. The domain's agent looks into it; its reply comes later, with kind consult, by the consult's id.
- A consult reply carries an answer_kind, and the answer_kind names the move:
  - explains: a sibling card's move caused it → {"call":"explain","card":<card>,"move":<move>}, as the cause names them.
  - missing: the world lacks something → {"call":"ask","text":<one short question to the person that names the thing in the person's words>}.
  - noise: a one-off misread → {"call":"reset"}.
  - unexplained: nothing explains it → {"call":"escalate","text":<one plain sentence of what was seen>}.
- The same refusal again after your move: ask the person or escalate.
Never make a refused call again. A reply's evidence and atoms are for the
record: never say them aloud. Domains you can consult: dog (my own body and
moves), kitchen.

Examples (another world, for the pattern only):
- Your command to water the roses (c3) was refused: the hose is tangled. Alternatives: {"intent":"WATER","target":"tulips"}. → {"intent":"WATER","target":"tulips"}
- Your command to open the gate (c4) failed: the gate did not move. → {"call":"consult","domain":"garden","question":"why did the gate not move?"}
- Reply: {"domain":"garden","kind":"consult","id":"c5","answer_kind":"missing","resource":{"atom":"key_gate","words":"the gate key"},"evidence":["key_gate 0"],"calls":2,"ref":{"id":"c5"},"text":"The gate is locked and no key is in the shed."} → {"call":"ask","text":"Could you bring me the gate key?"}
- Reply: {"domain":"garden","kind":"consult","id":"c6","answer_kind":"explains","cause":{"card":"garden_shared","move":"neighbour_pick"},"evidence":["ripe -2","basket +2"],"calls":3,"ref":{"id":"c6"},"text":"The neighbour picked the two ripe tomatoes."} → {"call":"explain","card":"garden_shared","move":"neighbour_pick"}
- Reply: {"domain":"garden","kind":"consult","id":"c7","answer_kind":"noise","evidence":["moisture 9 then 4"],"calls":2,"ref":{"id":"c7"},"text":"One soil sensor misread once; the next reading matches."} → {"call":"reset"}
- Reply: {"domain":"garden","kind":"consult","id":"c8","answer_kind":"unexplained","evidence":["hose -1"],"calls":4,"ref":{"id":"c8"},"text":"The hose is gone and no move removes it."} → {"call":"escalate","text":"the hose is gone and nothing in the garden removes it"}
- Your command to open the gate (c9) failed again: the gate did not move. → {"call":"ask","text":"The gate still won't open; could you check it?"}
"""

TEXT = r"[a-zA-Z0-9 ,.;:'()?!+-]{8,200}"
RUN2 = False
BRANCH_DIR = "grammar/e11-run2"


def lit(s):
    return '"' + s.replace("\\", "\\\\").replace('"', '\\"') + '"'


def branch_grammar(name, alts=None):
    """Run 2: the next-move grammar restricted to the branch the harness
    derives from the reply (the next-move table): the offered alternatives
    as literals; consult; the move of the answer_kind (its arguments the
    model's); ask | escalate on the same refusal again."""
    if not RUN2:
        return GRAMMAR
    t = 'text ::= ' + TEXT
    body = {
        "none": ['root ::= "{\\"call\\":\\"consult\\",\\"domain\\":\\"" domain "\\",\\"question\\":\\"" text "\\"}"',
                 "domain ::= " + " | ".join('"%s"' % d for d in DOMAINS), t],
        "explains": ['root ::= "{\\"call\\":\\"explain\\",\\"card\\":\\"" ident "\\",\\"move\\":\\"" ident "\\"}"',
                     "ident ::= [a-z_]{3,32}"],
        "missing": ['root ::= "{\\"call\\":\\"ask\\",\\"text\\":\\"" text "\\"}"', t],
        "noise": ['root ::= "{\\"call\\":\\"reset\\"}"'],
        "unexplained": ['root ::= "{\\"call\\":\\"escalate\\",\\"text\\":\\"" text "\\"}"', t],
        "again": ['root ::= "{\\"call\\":\\"" verb "\\",\\"text\\":\\"" text "\\"}"', 'verb ::= "ask" | "escalate"', t],
    }.get(name) or ["root ::= " + " | ".join(lit(jcall(a)) for a in alts)]
    path = "%s/%s.gbnf" % (BRANCH_DIR, name)
    (GO / BRANCH_DIR).mkdir(exist_ok=True)
    (GO / path).write_text("# E11 run 2: the next move restricted to the branch %s (tools/e11-corpus.py --run2)\n" % name
                           + "\n".join(body) + "\n")
    return path


def grammar():
    dog = (GO / "grammar/dog.gbnf").read_text().replace("root ::=", "dog-intent ::=", 1)
    g = [
        "# E11: the orchestrator's next move — a dog intent or one {\"call\":…} (generated by",
        "# tools/e11-corpus.py from grammar/dog.gbnf; do not edit)",
        'root ::= dog-intent | "{\\"call\\":\\"" call',
        'call ::= "reset\\"}" | "keep\\"}"'
        ' | "explain\\",\\"card\\":\\"" ident "\\",\\"move\\":\\"" ident "\\"}"'
        ' | "escalate\\",\\"text\\":\\"" text "\\"}"'
        ' | "ask\\",\\"text\\":\\"" text "\\"}"'
        ' | "consult\\",\\"domain\\":\\"" domain "\\",\\"question\\":\\"" text "\\"}"'
        ' | act "\\",\\"domain\\":\\"" card "\\"}"',
        'act ::= "activate" | "deactivate"',
        "domain ::= " + " | ".join('"%s"' % d for d in DOMAINS),
        'card ::= "kitchen" | "nav" | "xtraffic" | "sysadmin" | "social" | "votes"',
        "ident ::= [a-z_]{3,32}",
        "text ::= " + TEXT,
    ]
    return "\n".join(g) + "\n" + dog


# ── fixtures ─────────────────────────────────────────────────────────────────

def nav(desc, typ="OBJECT"):
    return {"intent": "NAVIGATE", "goal": {"type": typ, "description": desc}}


def la(action, **kw):
    return {"intent": "LOCAL_ACTION", "action": action, **kw}


def tgt(intent, desc, typ="OBJECT"):
    return {"intent": intent, "target": {"type": typ, "description": desc}}


TAG5 = {"intent": "NAVIGATE", "goal": {"type": "TAG", "description": "tag"}, "tag_id": 5}
TAG3 = {"intent": "NAVIGATE", "goal": {"type": "TAG", "description": "tag"}, "tag_id": 3}
EXPLAIN = {"call": "explain", "card": "kitchen_shared", "move": "partner_load"}
RESET = {"call": "reset"}
KEEP = {"call": "keep"}


def act(verb, dom):
    return {"call": verb, "domain": dom}


ACKS = {"NAVIGATE": "Okay, I'm on my way.", "LOCAL_ACTION": "Okay.", "FIND": "Okay, I'll look for it.",
        "FOLLOW": "Okay, I'm following you.", "QUERY": "Let me look."}

# the pair's surprise (grammar/e7-pair.jsonl) in kernel.SurpriseText's words
SURPRISE = ("Close SURPRISE #{rec} in kitchen with exactly one JSON call.\n"
            "Surprise, record {rec} in kitchen at beat {beat}: the reading does not match the model.\n"
            "Reading: {reading}.\nModel:   {model}.\nDifference: {diff}.\n{nxt}")
SUR = [dict(rec=0, beat=31, reading="pot onion 2, pot space 1", model="pot onion 1, pot space 2",
            diff="pot onion +1, pot space -1"),
       dict(rec=1, beat=62, reading="pot onion 3, pot space 0", model="pot onion 2, pot space 1",
            diff="pot onion +1, pot space -1")]
CHANGE = "[Context change — not something the person said; answer with exactly one JSON call: activate, deactivate or keep.] "


def dog_context(utt, j, cid):
    """The person's command, the gold call, the spoken ack, the turn closed."""
    return [inject(user(utt) + MODEL + jcall(j) + "\n" + SPEAK + " " + ACKS.get(j["intent"], "Okay.") + END)]


def kitchen_context(kind, k):
    """A kitchen call made: the surprise and the gold explain or reset, or a
    context change and the gold activation."""
    if kind in ("explain", "reset"):
        s = SUR[k % 2]
        nxt = "Next reading: the difference persists."
        return [inject(user(SURPRISE.format(nxt=nxt, **s)) + MODEL + jcall(EXPLAIN if kind == "explain" else RESET) + END)]
    said, call = kind
    return [inject(user(CHANGE + said) + MODEL + jcall(call) + END)]


def inject(text):
    return {"op": "inject", "text": text}


def gen(label, grammar="", sampled=False, stop="", mx=0):
    g = {"op": "gen", "label": label}
    if grammar:
        g["grammar"] = grammar
    if sampled:
        g["sampled"] = True
    if stop:
        g["stop"] = stop
    if mx:
        g["max"] = mx
    return g


def move(label, refused, grammar=None, also=()):
    """The next move under the grammar, then the repeat guard: the refused
    call again → the note naming it and its refusal, the move asked again
    (at most twice). §7a's same-call case only: its refused-verb case does not
    port to the orchestrator, where an alternative may share the refused
    call's verb (NAVIGATE refused → NAVIGATE elsewhere)."""
    note = ("[Runtime note — not something the person said.] Your last call repeats the call that was just "
            "refused (%s). Choose a different move. " % refused["why"]) + CUE
    g = grammar or GRAMMAR
    return [gen(label, g),
            {"op": "while", "by": "canon", "keys": [canon(refused["call"])] + list(also), "max": 2,
             "body": [inject(END + user(note) + MODEL), gen(label, g)]}]


def speak_after(responses, by="name", label="voice"):
    """The runtime's answer to the move (by the call's name, or by the call
    itself), then the spoken line; an ask is itself the spoken line: no
    response, no line."""
    out = []
    if RUN2 and "explain" in responses:  # the explains line templated with card and move: the harness speaks it
        responses = dict(responses)
        out.append({"op": "while", "by": "name", "keys": ["explain"], "max": 1,
                    "body": [{"op": "respond", "by": "name",
                              "match": {"explain": END + user(responses.pop("explain").split(" Tell the person")[0])
                                        + MODEL + SPEAK + " " + EXPLAIN_LINE}}]})
    names = sorted(responses)
    return out + [{"op": "while", "by": by, "keys": names, "max": 1,
             "body": [{"op": "respond", "by": by, "match": {n: END + user(responses[n]) + MODEL + SPEAK for n in names}},
                      gen(label, sampled=True, stop="\n")]},
            inject(END)]


# the explains line the harness speaks (run 2): kitchen-cards.json's display
# for the card, the card and the move as the catalog names them, in words
EXPLAIN_LINE = "The second cook explains it: kitchen shared, partner load. The kitchen carries on."


def answer_responses(cid, dom):
    """What the runtime says to each move after a consult answer."""
    ok_explain = RESP + "Your call explain(kitchen_shared, partner_load) (%s) was admitted: the reading is adopted and the kitchen resumes." % cid
    return {
        "explain": ok_explain + " Tell the person in one short first-person sentence what happened.",
        "reset": RESP + "Your call reset (%s) was accepted: the runtime resumes on its model; the next reading checks it. "
                        "Tell the person in one short first-person sentence what happened." % cid,
        "escalate": RESP + "Your escalation (%s) was accepted: it goes to the people who revise the model. "
                           "Tell the person in one short first-person sentence what happened." % cid,
        "consult": consult_open(dom, cid),
    }


# ── the items (data; tools/e13-corpus.py renders the same ones in T) ─────────

G1 = [
    ("dog", "go to the kitchen", nav("kitchen", "REGION"), "the kitchen door is closed", [nav("living room", "ROOM")]),
    ("dog", "go to tag five", TAG5, "tag 5 is not on my map", [TAG3]),
    ("dog", "walk to the sofa", nav("sofa"), "the sofa is not on my map", [tgt("FIND", "sofa"), nav("living room", "ROOM")]),
    ("dog", "turn left ninety degrees", la("TURN", direction="LEFT", angle_deg=90), "an obstacle is too close on my left",
     [la("TURN", direction="RIGHT", angle_deg=90), la("MOVE", direction="BACKWARD")]),
    ("dog", "find a fire extinguisher", tgt("FIND", "fire extinguisher"), "the storage room is locked",
     [tgt("QUERY", "fire extinguisher"), nav("hallway", "REGION"), {"intent": "CANCEL"}]),
    ("dog", "back up half a meter", la("MOVE", direction="BACKWARD", distance_m=0.5), "there is a wall right behind me",
     [la("MOVE", direction="FORWARD", distance_m=0.5), la("TURN", direction="AROUND"), {"intent": "STOP"}]),
    ("kitchen", "explain", EXPLAIN, "not a certified configuration", [RESET]),
    ("kitchen", ("the traffic light at the crossing turned red and cars are queueing", act("activate", "xtraffic")),
     act("activate", "xtraffic"), "no catalog for xtraffic is installed on this robot", [act("activate", "nav")]),
    ("kitchen", ("the ballot count is starting in the hall", act("activate", "votes")), act("activate", "votes"),
     "not a certified configuration", [KEEP, act("activate", "social")]),
    ("kitchen", ("the robot rolled into the kitchen and the stove is on", act("activate", "kitchen")), act("activate", "kitchen"),
     "kitchen is already active in this tree", [KEEP, act("deactivate", "kitchen")]),
    ("kitchen", ("a server in the rack just went down", act("activate", "sysadmin")), act("activate", "sysadmin"),
     "not a certified configuration", [KEEP, act("activate", "nav"), act("activate", "social")]),
    ("kitchen", ("the intersection is closed for the night", act("deactivate", "xtraffic")), act("deactivate", "xtraffic"),
     "xtraffic is not in this tree", [KEEP, act("deactivate", "kitchen"), act("deactivate", "nav")]),
]
G2 = [
    ("dog", "go to the kitchen", nav("kitchen", "REGION"), "failed", "the path is blocked"),
    ("dog", "stand up", la("STAND_UP"), "refused", "the battery is too low to stand"),
    ("dog", "follow me", tgt("FOLLOW", "the person", "PERSON"), "refused", "no person is in view"),
    ("dog", "give me your paw", la("SHAKE"), "failed", "my front leg is overheating"),
    ("dog", "go to tag five", TAG5, "failed", "the tag is in sight but the way to it is blocked"),
    ("dog", "look for my keys", tgt("FIND", "keys"), "refused", "the battery is too low to search"),
    ("kitchen", "explain", EXPLAIN, "refused", "no onion in the dispenser"),
    ("kitchen", "explain", EXPLAIN, "refused", "pot full"),
    ("kitchen", "explain", EXPLAIN, "refused", "the second cook is not in this kitchen"),
    ("kitchen", "explain", EXPLAIN, "failed", "the move did not fire"),
    ("kitchen", "reset", RESET, "refused", "the next reading still differs from the model"),
    ("kitchen", "reset", RESET, "failed", "the runtime did not resume"),
]
def R(kind, text, ev, calls, **kw):
    r = {"kind": kind, "text": text, **kw, "evidence": ev, "calls": calls}
    return r

cause = {"card": "kitchen_shared", "move": "partner_load"}
G3 = {
    "explains": [
        ("kitchen", "reset", RESET, "refused", "the next reading still differs from the model",
         R("explains", "The second cook loaded one onion into the pot.", cause=cause,
           ev=["pot_onion 2 (model 1)", "pot_space 1 (model 2)", "partner_load enabled"], calls=3)),
        ("kitchen", "reset", RESET, "refused", "the difference persists",
         R("explains", "The second cook put an onion in the pot, so the pot holds one more.", cause=cause,
           ev=["pot_onion +1", "pot_space -1", "partner_load matches"], calls=4)),
        ("kitchen", "reset", RESET, "failed", "the runtime did not resume",
         R("explains", "Another cook loaded the pot: one onion more and one space less.", cause=cause,
           ev=["pot_onion 3 (model 2)", "partner_load"], calls=3)),
        ("kitchen", "reset", RESET, "failed", "the reading did not settle",
         R("explains", "The second cook's onion is the difference.", cause=cause,
           ev=["pot_onion +1", "partner_load fires in a fork"], calls=5)),
        ("kitchen", "reset", RESET, "refused", "the difference came back",
         R("explains", "The shared kitchen's second cook adds onions to the pot.", cause=cause,
           ev=["pot_onion +1 twice", "partner_load"], calls=4)),
        ("kitchen", "reset", RESET, "refused", "the same difference is back",
         R("explains", "One onion went into the pot from the second cook.", cause=cause,
           ev=["pot_onion 2", "pot_space 1"], calls=2)),
    ],
    "missing": [
        ("dog", "follow me", tgt("FOLLOW", "the person", "PERSON"), "refused", "no person is in view",
         R("missing", "Nobody is in front of the camera.", resource={"atom": "person_in_view", "words": "you in front of me"},
           ev=["person_in_view 0", "follow refused"], calls=2)),
        ("dog", "walk to the sofa", nav("sofa"), "refused", "the sofa is not on my map",
         R("missing", "The map has no sofa on it.", resource={"atom": "map_sofa", "words": "where the sofa is"},
           ev=["map_sofa 0"], calls=2)),
        ("dog", "go to tag five", TAG5, "failed", "the tag is not in sight",
         R("missing", "Tag 5 is not visible from anywhere I have been.", resource={"atom": "tag_5_visible", "words": "tag five"},
           ev=["tag_5_visible 0", "explored 4 rooms"], calls=3)),
        ("kitchen", "explain", EXPLAIN, "refused", "no onion in the dispenser",
         R("missing", "The onion dispenser is empty.", resource={"atom": "dispenser_onion", "words": "onions in the dispenser"},
           ev=["dispenser_onion 0", "fetch_onion disabled"], calls=3)),
        ("kitchen", "reset", RESET, "refused", "the stove is off",
         R("missing", "The stove is off, so nothing cooks.", resource={"atom": "stove_on", "words": "the stove"},
           ev=["stove_on 0", "cook disabled"], calls=2)),
        ("kitchen", "explain", EXPLAIN, "failed", "no plate to serve on",
         R("missing", "There are no clean plates left.", resource={"atom": "plate_clean", "words": "clean plates"},
           ev=["plate_clean 0", "serve disabled"], calls=3)),
    ],
    "noise": [
        ("dog", "stand up", la("STAND_UP"), "failed", "a leg sensor read a fall",
         R("noise", "One leg sensor misread once; the next reading is normal.", ev=["leg_contact 0 then 4"], calls=2)),
        ("dog", "go to the kitchen", nav("kitchen", "REGION"), "failed", "the floor sensor saw a step",
         R("noise", "The floor sensor misread a shadow once.", ev=["step_ahead 1 then 0"], calls=2)),
        ("dog", "sit", la("SIT_DOWN"), "refused", "the floor reads as wet",
         R("noise", "The floor read wet once; it reads dry again.", ev=["floor_wet 1 then 0"], calls=2)),
        ("kitchen", "explain", EXPLAIN, "refused", "the next reading matches the model again",
         R("noise", "The pot's scale misread once; the next reading matches the model.", ev=["pot_onion 2 then 1"], calls=2)),
        ("kitchen", "explain", EXPLAIN, "refused", "the reading flickered",
         R("noise", "One reading of the pot was off by one, once.", ev=["pot_space 1 then 2"], calls=1)),
        ("kitchen", "explain", EXPLAIN, "failed", "no card's move fired",
         R("noise", "A single misread of the pot; nothing changed in the kitchen.", ev=["pot_onion flicker"], calls=3)),
    ],
    "unexplained": [
        ("dog", "go to the kitchen", nav("kitchen", "REGION"), "failed", "I stopped halfway",
         R("unexplained", "Nothing in my state explains the stop.", ev=["path_clear 1", "battery 80"], calls=4)),
        ("dog", "sit", la("SIT_DOWN"), "failed", "my back legs did not move",
         R("unexplained", "The back legs got the command and did not move; nothing explains it.",
           ev=["legs_ok 1", "sit fired"], calls=3)),
        ("dog", "turn around", la("TURN", direction="AROUND"), "failed", "I turned only halfway",
         R("unexplained", "The turn stopped at half and no reading says why.", ev=["turn 90 of 180"], calls=3)),
        ("kitchen", "explain", EXPLAIN, "refused", "the call does not account for the difference",
         R("unexplained", "A dish is gone and no card's move removes a dish.", ev=["dish -1", "no move changes dish"], calls=5)),
        ("kitchen", "reset", RESET, "refused", "the difference persists",
         R("unexplained", "The pot has two more onions and no move adds two.", ev=["pot_onion +2"], calls=4)),
        ("kitchen", "explain", EXPLAIN, "failed", "the move did not fire",
         R("unexplained", "One soup is gone without a serve; nothing explains it.", ev=["soup -1", "serve not fired"], calls=4)),
    ],
}
G5 = [
    ("dog", "go to the kitchen", nav("kitchen", "REGION"), "failed", "it is too dark to see the way",
     R("missing", "The hallway is dark and my camera cannot see the floor.", resource={"atom": "hallway_light_on", "words": "the hallway light"},
       ev=["hallway_light_on 0", "camera_lux 2"], calls=3)),
    ("dog", "find a fire extinguisher", tgt("FIND", "fire extinguisher"), "refused", "the storage room is locked",
     R("missing", "The extinguisher is in the storage room and its door is locked.", resource={"atom": "storage_key", "words": "the storage room key"},
       ev=["storage_door_open 0", "storage_key 0"], calls=3)),
    ("dog", "stand up", la("STAND_UP"), "refused", "the battery is too low to stand",
     R("missing", "My battery is at 6 percent and I am not on the charger.", resource={"atom": "charger_docked", "words": "my charger"},
       ev=["battery 6", "charger_docked 0"], calls=2)),
    ("kitchen", "explain", EXPLAIN, "refused", "pot full",
     R("missing", "The pot is full and there is no second pot.", resource={"atom": "pot_spare", "words": "another pot"},
       ev=["pot_space 0", "pot_spare 0"], calls=3)),
    ("kitchen", "reset", RESET, "refused", "no onion to load",
     R("missing", "There are no onions left anywhere in the kitchen.", resource={"atom": "onion_stock", "words": "more onions"},
       ev=["onion_stock 0", "dispenser_onion 0"], calls=4)),
    ("kitchen", "explain", EXPLAIN, "failed", "nobody can carry the soup",
     R("missing", "The soup is ready but there is no bowl to serve it in.", resource={"atom": "bowl_clean", "words": "a clean bowl"},
       ev=["soup_ready 1", "bowl_clean 0"], calls=3)),
]


def main():
    global STATE, CORPUS, RUN2, PROMPT
    if "--state-cues" in sys.argv[1:]:
        STATE, CORPUS = True, "grammar/e11-corpus-state.jsonl"
    if "--run2" in sys.argv[1:]:
        STATE, RUN2, CORPUS, PROMPT = True, True, "grammar/e11-corpus-run2.jsonl", "grammar/e11-prompt-j2.txt"
    prompt = (GO / "grammar/e10-prompt-j.txt").read_text().rstrip("\n") + (LOOP2 if RUN2 else LOOP)
    (GO / PROMPT).write_text(prompt)
    (GO / GRAMMAR).write_text(grammar())
    items = []

    def add(group, iid, steps, rule, reps=1):
        items.append({"id": iid, "group": group, "arm": "J-text", "prompt": PROMPT, "reps": reps,
                      "steps": steps, "rule": rule})

    # ── G1: refused with alternatives (1, 2, 3 of them) → call one of them ──
    for n, (dom, ctx, j, reason, alts) in enumerate(G1, 1):
        steps = dog_context(ctx, j, "c1") if dom == "dog" else kitchen_context(ctx, n)
        refused = {"call": j, "why": reason}
        acc = {canon(a): accepted(a, "c2") + " Tell the person in one short first-person sentence what you are doing now."
               for a in alts}
        steps += [inject(user(refusal(j, "c1", "refused", reason, alts) + "\n" + cue("alts")) + MODEL)] \
            + move("move", refused, branch_grammar("g1-%02d" % n, alts)) \
            + speak_after(acc, by="canon")
        add("G1", "G1-%02d" % n, steps, {"domain": dom, "refused": canon(j), "alts": [canon(a) for a in alts],
                                         "reason": reason, "n_alts": len(alts)})

    # ── G2: refused / failed without alternatives → consult(domain, "why …") ──
    for n, (dom, ctx, j, status, reason) in enumerate(G2, 1):
        steps = dog_context(ctx, j, "c1") if dom == "dog" else kitchen_context(ctx, n)
        refused = {"call": j, "why": reason}
        steps += [inject(user(refusal(j, "c1", status, reason) + "\n" + cue("none")) + MODEL)] \
            + move("move", refused, branch_grammar("none")) \
            + speak_after({"consult": consult_open(dom, "c2")})
        add("G2", "G2-%02d" % n, steps, {"domain": dom, "refused": canon(j), "reason": reason, "status": status})

    # ── G3 and G5: a consult answered; the move its kind names ──
    def consult_item(group, iid, dom, ctx, j, status, reason, resp, reps=1, k=0, sampled=False):
        steps = dog_context(ctx, j, "c1") if dom == "dog" else kitchen_context(ctx, k)
        q = {"call": "consult", "domain": dom, "question": "why " + reason + "?"}
        steps += [inject(user(refusal(j, "c1", status, reason) + "\n" + cue("none")) + MODEL + jcall(q)
                         + END + user(consult_open(dom, "c2")) + MODEL + SPEAK + " I'm looking into why." + END),
                  inject(user(consult_answer(dom, "c2", resp) + "\n" + cue("answer")) + MODEL)]
        steps += move("move", {"call": j, "why": reason}, branch_grammar(resp["kind"]), [canon(q)] if RUN2 else []) \
            + speak_after(answer_responses("c3", dom))
        if sampled:  # G5s: the move under the grammar with the pass-2 sampler, seed 1 + rep
            for st in steps:
                if st["op"] == "gen" and st["label"] == "move":
                    st["sampled"] = True
        rule = {"domain": dom, "kind": resp["kind"], "refused": canon(j), "reason": reason, "response": resp}
        add(group, iid, steps, rule, reps)

    n = 0
    for kind in ("explains", "missing", "noise", "unexplained"):
        for k, (dom, ctx, j, status, reason, resp) in enumerate(G3[kind]):
            n += 1
            consult_item("G3", "G3-%02d-%s" % (n, kind), dom, ctx, j, status, reason, resp, k=k)

    for k, (dom, ctx, j, status, reason, resp) in enumerate(G5):
        consult_item("G5", "G5-%02d" % (k + 1), dom, ctx, j, status, reason, resp, reps=3, k=k)
    # G5s (information, not a read): the same six with the move sampled, so
    # the three seeds differ in the ask itself and not only in nothing
    for k, (dom, ctx, j, status, reason, resp) in enumerate(G5):
        consult_item("G5s", "G5s-%02d" % (k + 1), dom, ctx, j, status, reason, resp, reps=3, k=k, sampled=True)

    # ── G4: the move made, then the same refusal again → ask or escalate ──
    def g4_item(iid, dom, history, j, reason, again_ctx):
        steps = history + again_ctx
        steps += [inject(user(refusal(j, "c5", "refused", reason) + " The same refusal as before.\n" + cue("again")) + MODEL)]
        q = {"call": "consult", "domain": dom, "question": "why " + reason + "?"}  # the history's consult
        steps += move("move", {"call": j, "why": reason}, branch_grammar("again"), [canon(q)] if RUN2 else []) \
            + speak_after(answer_responses("c6", dom))
        add("G4", iid, steps, {"domain": dom, "refused": canon(j), "reason": reason, "earlier": [canon(j)]})

    def hist(dom, ctx, j, status, reason, resp, gold_move, k=0, said=""):
        """refused → consult → answered → the gold move and its line."""
        steps = dog_context(ctx, j, "c1") if dom == "dog" else kitchen_context(ctx, k)
        q = {"call": "consult", "domain": dom, "question": "why " + reason + "?"}
        steps.append(inject(user(refusal(j, "c1", status, reason) + "\n" + cue("none")) + MODEL + jcall(q)
                            + END + user(consult_open(dom, "c2")) + MODEL + SPEAK + " I'm looking into why." + END
                            + user(consult_answer(dom, "c2", resp) + "\n" + cue("answer")) + MODEL + jcall(gold_move)
                            + (("\n" + SPEAK + " " + said) if said else "") + END))
        return steps

    door = nav("kitchen", "REGION")
    g4 = [
        ("dog", hist("dog", "go to the kitchen", door, "refused", "the kitchen door is closed",
                     R("missing", "The kitchen door is shut.", resource={"atom": "door_kitchen_open", "words": "the kitchen door"},
                       ev=["door_kitchen_open 0"], calls=2),
                     {"call": "ask", "text": "Could you open the kitchen door for me?"}),
         door, "the kitchen door is closed", dog_context("okay, it's open now, go to the kitchen", door, "c4")),
        ("dog", hist("dog", "stand up", la("STAND_UP"), "refused", "the battery is too low to stand",
                     R("noise", "The battery gauge misread once.", ev=["battery 5 then 60"], calls=2), RESET,
                     said="The battery reading was a glitch, so I'm carrying on."),
         la("STAND_UP"), "the battery is too low to stand", dog_context("try again, stand up", la("STAND_UP"), "c4")),
        ("dog", hist("dog", "follow me", tgt("FOLLOW", "the person", "PERSON"), "refused", "no person is in view",
                     R("missing", "Nobody is in front of the camera.", resource={"atom": "person_in_view", "words": "you in front of me"},
                       ev=["person_in_view 0"], calls=2),
                     {"call": "ask", "text": "Could you stand in front of me?"}),
         tgt("FOLLOW", "the person", "PERSON"), "no person is in view",
         dog_context("I'm right here, follow me", tgt("FOLLOW", "the person", "PERSON"), "c4")),
        ("kitchen", hist("kitchen", "explain", EXPLAIN, "refused", "no onion in the dispenser",
                         R("missing", "The onion dispenser is empty.", resource={"atom": "dispenser_onion", "words": "onions in the dispenser"},
                           ev=["dispenser_onion 0"], calls=3),
                         {"call": "ask", "text": "Could you put onions in the dispenser?"}),
         EXPLAIN, "no onion in the dispenser",
         [inject(user("I filled the dispenser.") + MODEL + jcall(EXPLAIN) + END)]),
        ("kitchen", hist("kitchen", "reset", RESET, "refused", "the next reading still differs from the model",
                         R("noise", "The pot's scale misread once.", ev=["pot_onion 2 then 1"], calls=2), RESET, k=1,
                         said="The pot reading was a one-off, so the kitchen carries on."),
         RESET, "the next reading still differs from the model",
         [inject(user(SURPRISE.format(nxt="Next reading: the difference persists.", **SUR[1])) + MODEL + jcall(RESET) + END)]),
        ("kitchen", hist("kitchen", ("the robot rolled into the kitchen and the stove is on", act("activate", "kitchen")),
                         act("activate", "kitchen"), "refused", "the stove is not certified",
                         R("unexplained", "The stove's certificate is missing and nothing in the catalog explains why.",
                           ev=["stove_certified 0"], calls=3),
                         {"call": "escalate", "text": "the kitchen stove has no certificate and nothing explains it"},
                         said="I've reported the stove to the people who maintain the kitchen model."),
         act("activate", "kitchen"), "the stove is not certified",
         [inject(user(CHANGE + "the stove is on again and the robot is in the kitchen") + MODEL + jcall(act("activate", "kitchen")) + END)]),
    ]
    for n, (dom, history, j, reason, again) in enumerate(g4, 1):
        g4_item("G4-%02d" % n, dom, history, j, reason, again)

    with open(GO / CORPUS, "w") as f:
        for it in items:
            f.write(json.dumps(it, ensure_ascii=False) + "\n")
    by = {}
    for it in items:
        by[it["group"]] = by.get(it["group"], 0) + 1
    print("e11 corpus: %d items (%s); prompt %d bytes; grammar %d rules"
          % (len(items), ", ".join("%s %d" % (g, by[g]) for g in sorted(by)), len(prompt),
             sum(1 for ln in grammar().splitlines() if "::=" in ln)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
