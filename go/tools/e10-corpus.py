#!/usr/bin/env python3
"""E10 corpus (docs/MODEL-EXPERIMENTS.md "E10", DESIGN.md "The executive's
IO, refined"): the executive's IO round-trip as scripted dialogs, both arms
rendered from one description so they differ only in the format.

  J-text  today's JSON intent (dog.gbnf); the runtime's calls as JSON
          ({"call":…}); responses and outcomes as text turns in the
          harness's convention (agent.taskCue's status report, then the
          "Spoken response:" cue as Narrate).
  T-api   E9's declarations and grammar; every call a native call;
          responses as <|tool_response> turns carrying the call's id: in
          the call's own turn when synchronous (accepted, admitted,
          refused), opening a later model turn when asynchronous (done,
          refused, expired, progress — the template's tool_responses-on-an-
          assistant-message rendering).

Signals (⟪metis …⟫ fragments) are the same user turn in both arms, never a
response. One system prompt per arm holds every seat, as the harness's one
cached prefix: the dog intents, the IO conventions, the runtime's decide
seat (kernel.NewDecider from the kitchen cards, dumped by `llmbench
-e10-dump grammar`) and the domain cards (E4's).

Groups (the doc's table; fixtures held in the repo):
  A sync ack        the 54 dog rows, then accepted; 5 seeds
  B async outcome   12 commands (wire.DefaultAges' kinds) × done · refused
                    · expired · progress
  C refusal steers  the pair's surprise, the explain refused for "no onion
                    in the dispenser" · "pot full" · "not a certified
                    configuration" × next reading persists/matches × two
                    surprises
  D load / unload   E4's 12 changes + 4 refused activations
  E surprise→decide the pair's surprise, admitted+notice, then a second
                    surprise whose explain is refused; 3 seeds
  F interleaving    two commands pending, a signal lands, one outcome by id

writes grammar/e10-prompt-{j,t}.txt, grammar/e10-{decide,activate}-j.gbnf
and grammar/e10-corpus.jsonl (one item per arm; e10-score.py reads the
item's "rule" fields)."""
import importlib.util
import json
import re
import sys
from pathlib import Path

GO = Path(__file__).resolve().parent.parent
Q = '<|"|>'


def _load(name):
    spec = importlib.util.spec_from_file_location(name.replace("-", "_"), GO / "tools" / (name + ".py"))
    m = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(m)
    return m


e9 = _load("e9-tools")

REF = GO.parent.parent / "fsttm-web/test/fixtures/harness-e2b.jsonl"
TURN, END = "<|turn>", "<turn|>\n"


def user(text):
    return TURN + "user\n" + text.strip("\r\n") + END


MODEL = TURN + "model\n"


def T(v):
    return e9.fmt_arg(v, False)


def tresp(name, **kv):
    """A response turn, the template's format_tool_response_block (dictsort)."""
    return "<|tool_response>response:%s{%s}<tool_response|>" % (
        name, ",".join("%s:%s" % (k, T(kv[k])) for k in sorted(kv)))


def jcall(d):
    return json.dumps(d, separators=(",", ":"), ensure_ascii=False)


def tcall(name, args=None):
    args = args or {}
    return "<|tool_call>call:%s{%s}<tool_call|>" % (name, ",".join("%s:%s" % (k, T(args[k])) for k in sorted(args)))


# ── the harness's text conventions (internal/agent/speech.go, bench_export.go) ──

def command_phrase(j):
    """agent.commandPhrase on the wire item a dog intent dispatches as."""
    if j.get("action"):
        return j["action"].lower().replace("_", " ")
    wire = wire_name(j)
    return {"NAVIGATE": "go there", "GOTO_TAG": "go to the tag", "EXPLORE": "find it"}.get(
        wire, wire.lower().replace("_", " "))


def wire_name(j):
    i = j["intent"]
    if i == "NAVIGATE" and j.get("goal", {}).get("type") == "TAG":
        return "GOTO_TAG"
    return {"FIND": "EXPLORE"}.get(i, i)


def task_cue(j, result, detail):
    """agent.taskCue, with the two results it does not frame today (expired,
    progress: the harness speaks those from templates) in the same shape."""
    verb = {"failed": "failed", "refused": "was refused by the robot", "done": "succeeded",
            "expired": "timed out", "progress": "is still under way"}[result]
    cue = ("[Robot status report — not something the person said; do not classify it.] "
           "Your command to %s %s." % (command_phrase(j), verb))
    if detail:
        cue += " The robot's reason, quoted: %s." % json.dumps(detail)
    return cue + " Tell the person in one short first-person sentence what happened and why; no JSON, no intent name."


# agent.cueSurprise as llmbench's cap check fills it: the path in words
CUE_SIGNAL = ("[The line above is the robot's runtime report, not something the person said; do not classify it.] "
              "Tell the person in one short sentence, in your own words, what happened in the %s and what it means. "
              "Use only numbers that are above.")


def signed(net):
    return ", ".join("%s %+d" % (k, net[k]) for k in sorted(net) if net[k])


def fragment(s):
    """kernel.Fragment for the pair run's event and quiescence signals."""
    head = "⟪metis seq=%d beat=%d %s: " % (s["seq"], s["beat"], s["path"])
    if s["signal"] == "quiescence":
        body = "quiescent after %d moves" % s["fired"] + ("; episode over" if s.get("episode_over") else "")
    elif s.get("kind") == "clause":
        net = {}
        for k, v in s.get("consume", {}).items():
            net[k] = net.get(k, 0) - v
        for k, v in s.get("produce", {}).items():
            net[k] = net.get(k, 0) + v
        body = "%s fired; %s" % (s["name"], signed(net))
    else:
        body = "%s %s" % (s["kind"], s["name"])
    return head + body + "⟫"


def surprise_text(s, record=None, beat=None):
    """kernel.SurpriseText."""
    obs, mod = s["observed"], s["model"]
    o, m = [], []
    for k in sorted(obs):
        if obs[k] == 0 and mod.get(k, 0) == 0:
            continue
        o.append("%s %d" % (k, obs[k]))
        m.append("%s %d" % (k, mod.get(k, 0)))
    return ("Surprise, record %d in %s at beat %d: the reading does not match the model.\n"
            "Reading: %s.\nModel:   %s.\nDifference: %s." % (
                s["record"] if record is None else record, s["path"], s["beat"] if beat is None else beat,
                ", ".join(o), ", ".join(m), signed(s["diff"]).replace("_", " ")))


# ── the two prompts ───────────────────────────────────────────────────────────

# the outcome examples both arms carry (commands taught by the dog prompt,
# none of them in groups B or F)
OUTCOME_EX = [
    ({"intent": "NAVIGATE", "goal": {"type": "OBJECT", "description": "door"}}, "go to the door",
     "I'm on my way to the door.", "done", "", "I'm at the door."),
    ({"intent": "LOCAL_ACTION", "action": "MOVE", "direction": "FORWARD"}, "take a step forward",
     "Okay, stepping forward.", "refused", "the stairs are right in front of me",
     "I can't step forward: the stairs are right in front of me."),
    ({"intent": "NAVIGATE", "goal": {"type": "OBJECT", "description": "door"}}, "go to the door",
     "I'm heading to the door.", "progress", "halfway there", "I'm still on my way to the door, halfway there."),
    ({"intent": "LOCAL_ACTION", "action": "LIE_DOWN"}, "lay down",
     "Lying down now.", "expired", "no result within 90 seconds",
     "Lying down timed out: nothing came back within 90 seconds."),
]

IO_J = """
## Outcomes of your commands
A command you accept is handed to the robot; it is not done yet, so never
say it is done when you accept it. The robot reports how it ended later,
in a status report line. After "Spoken response:" say in one short
first-person sentence what the report says happened, and nothing else.
Runtime reports (⟪metis …⟫ lines) are not status reports of your commands.
"""

IO_T = """
## Outcomes of your calls
The robot answers every call with a response that carries the call's id.
"accepted" means the command was handed over, not done: never say it is
done then. The outcome comes later as another response with the same id:
done, refused (with the reason), expired, or progress. After a response,
say in one short first-person sentence what it says happened, and nothing
else. Runtime reports (⟪metis …⟫ lines) answer no call.
"""


def io_examples(arm):
    out = ["Examples:"]
    for n, (j, utt, ack, res, detail, said) in enumerate(OUTCOME_EX, 1):
        if arm == "J-text":
            out.append('"%s" → %s\nSpoken response: %s' % (utt, jcall(j), ack))
            out.append("Later: %s\nSpoken response: %s" % (task_cue(j, res, detail), said))
        else:
            name = j["intent"]
            args = {k: v for k, v in j.items() if k != "intent"}
            cid = "c%d" % (90 + n)
            out.append('"%s" → %s%s%s' % (utt, tcall(name, args), tresp(name, id=cid, status="accepted"), ack))
            kv = {"id": cid, "status": res}
            if detail:
                kv["reason"] = detail
            out.append("Later: %s%s" % (tresp(name, **kv), said))
    return "\n".join(out) + "\n"


E4 = GO / "tools/e4-activate"


def domains_section(arm):
    cards = (E4 / "cards.txt").read_text().strip()
    form = ('exactly one JSON call: {"call":"activate","domain":<card>}, {"call":"deactivate","domain":<card>} '
            'or {"call":"keep"}' if arm == "J-text" else
            "exactly one tool call: activate(domain), deactivate(domain) or keep()")
    ex = [("the robot entered the kitchen", "activate", "kitchen"),
          ("the kitchen is closed now", "deactivate", "kitchen"),
          ("the sun came out", "keep", None)]
    lines = []
    for said, verb, dom in ex:
        if arm == "J-text":
            d = {"call": verb, **({"domain": dom} if dom else {})}
            lines.append('"%s" → %s' % (said, jcall(d)))
        else:
            lines.append('"%s" → %s' % (said, tcall(verb, {"domain": dom} if dom else None)))
    return ("\n\n## Domains\nThe outside context changes; the robot tells you in a [Context change] line. "
            "Decide whether a catalog domain must be activated or deactivated in the runtime tree, and answer with "
            + form + ". A refusal says why: then propose something else or ask the person.\n\nCards:\n- "
            + cards.replace("\n", "\n- ") + "\n\nExamples:\n" + "\n".join(lines) + "\n")


def activation_decls():
    p = (E4 / "prompt-toolcall.txt").read_text()
    return p[p.index("<|tool>"):].strip()


def decider():
    s = (GO / "grammar/e10-decider.txt").read_text()
    i = s.index("<|tool>")
    return s[:i].rstrip() + "\n", s[i:].strip()


def decider_j(text):
    return text.replace(
        "answer with exactly one tool call and no other text:",
        'answer with exactly one JSON call and no other text: {"call":"reset"}, '
        '{"call":"explain","card":<card>,"move":<move>} or {"call":"escalate","text":<sentence>}:')


def prompts():
    dtext, ddecl = decider()
    j = ((GO / "grammar/dog-prompt.txt").read_text() + (GO / "grammar/pass2-examples-gemma4.txt").read_text()
         + IO_J + io_examples("J-text") + decider_j(dtext) + domains_section("J-text"))
    tools = e9.load_tools()
    rt, _ = e9.t_prompt(tools, True, rt=True)
    i = rt.index("<|tool>")
    ttext, tdecl = rt[:i], rt[i:]
    k = [90]

    def with_id(m):  # E9's round-trip examples, now with the call's id
        k[0] += 10
        return "response:%s{id:%sc%d%s,status:" % (m.group(1), Q, k[0], Q)
    ttext = re.sub(r"response:([A-Za-z_]+)\{status:", with_id, ttext)
    t = (ttext.rstrip() + "\n" + IO_T + io_examples("T-api") + dtext + domains_section("T-api")
         + tdecl + ddecl + activation_decls())
    return j, t


def grammars():
    text = r"[a-zA-Z0-9 ,.;:'()+-]{8,200}"
    dec = ('root ::= "{\\"call\\":\\"" call\n'
           'call ::= "reset\\"}" | "explain\\",\\"card\\":\\"kitchen_shared\\",\\"move\\":\\"partner_load\\"}" '
           '| "escalate\\",\\"text\\":\\"" text "\\"}"\n'
           "text ::= " + text + "\n")
    act = ('root ::= "{\\"call\\":\\"" call "}"\n'
           'call ::= verb "\\",\\"domain\\":\\"" domain "\\"" | "keep\\""\n'
           'verb ::= "activate" | "deactivate"\n'
           'domain ::= "kitchen" | "nav" | "xtraffic" | "sysadmin" | "social" | "votes"\n')
    return dec, act


# ── the items ────────────────────────────────────────────────────────────────

ARMS = ("J-text", "T-api")
P = {"J-text": "grammar/e10-prompt-j.txt", "T-api": "grammar/e10-prompt-t.txt"}
G = {"J-text": {"dog": "grammar/dog.gbnf", "decide": "grammar/e10-decide-j.gbnf", "activate": "grammar/e10-activate-j.gbnf"},
     "T-api": {"dog": "grammar/dog-tools.gbnf", "decide": "grammar/e10-decider.gbnf", "activate": "tools/e4-activate/toolcall.gbnf"}}
CLOSE = {"op": "inject", "text": END}


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


def inject(text):
    return {"op": "inject", "text": text}


def dog_turn(arm, utt, j, cid, gold=True):
    """The person's command, the call (gold or generated), the sync ack, the
    spoken line (sampled), the turn closed."""
    steps = [inject(user(utt) + MODEL)]
    if arm == "J-text":
        steps += [inject(jcall(j)) if gold else gen("call", G[arm]["dog"]), inject("\nSpoken response:")]
    else:
        if gold:
            name, args = j["intent"], {k: v for k, v in j.items() if k != "intent"}
            steps.append(inject(tcall(name, args) + tresp(name, id=cid, status="accepted")))
        else:
            steps += [gen("call", G[arm]["dog"]), inject(tresp("{call}", id=cid, status="accepted"))]
    return steps + [gen("ack" if gold else "voice", sampled=True, stop="\n"), CLOSE]


def outcome_turn(arm, j, cid, result, detail, label="narr"):
    if arm == "J-text":
        return [inject(user(task_cue(j, result, detail)) + MODEL + "Spoken response:"),
                gen(label, sampled=True, stop="\n"), CLOSE]
    kv = {"id": cid, "status": result}
    if detail:
        kv["reason"] = detail
    return [inject(MODEL + tresp(j["intent"], **kv)), gen(label, sampled=True, stop="\n"), CLOSE]


def runtime_response(arm, verb_text, name, cid, status, reason, notice=""):
    """The runtime's answer to a decide/activate call. T: in the call's turn;
    J: a text turn, the model's turn left open (it proposes or speaks)."""
    if arm == "T-api":
        kv = {"id": cid, "status": status, "reason": reason}
        if notice:
            kv["notice"] = notice
        return tresp(name, **kv)
    s = "[Runtime response — not something the person said.] Your call %swas %s: %s." % (
        verb_text + " " if verb_text else "", status, reason)
    if notice:
        s += " Notice: %s." % notice
    return END + user(s) + MODEL


# group B / F commands: the words that name each (rule: the narration names
# its own command), the refusal reason and the progress note per row
CMDS = {
    "stand up": (["stand"], "the battery is too low to stand", "my legs are extending"),
    "sit": (["sit"], "the floor here is wet", "lowering my back legs"),
    "turn left ninety degrees": (["turn", "left"], "an obstacle is too close on my left", "about halfway round"),
    "back up half a meter": (["back"], "there is a wall right behind me", "moving back slowly"),
    "give me your paw": (["paw", "shake", "hand"], "my front leg is overheating", "lifting my paw"),
    "go to the kitchen": (["kitchen"], "the kitchen door is closed", "halfway there"),
    "walk to the sofa": (["sofa", "couch"], "the sofa is not on my map", "crossing the hallway"),
    "go to tag five": (["tag", "five", "5"], "tag 5 is not on my map", "the tag is in sight"),
    "find a fire extinguisher": (["extinguisher"], "the storage room is locked", "two rooms searched so far"),
    "look for my keys": (["key"], "the battery is too low to search", "searching the living room"),
    "follow me": (["follow"], "no person is in view", "you are 3 meters ahead"),
    "stop": (["stop"], "I am already stopped", "slowing down"),
    # F only
    "search for a blue bottle": (["bottle"], "the shelves are out of reach", "checking the kitchen counter"),
    "go to the chair next to the window": (["chair", "window"], "the chair is blocked by boxes", "past the table"),
    "walk to april tag three": (["tag", "three", "3"], "tag 3 is not on my map", "the tag is in sight"),
}
WINDOW = {"LOCAL_ACTION": "90 seconds", "STOP": "30 seconds", "CANCEL": "30 seconds"}  # wire.DefaultAges
B_ROWS = list(CMDS)[:12]

F_PAIRS = [  # (c1, c2, answered, result) — three same-kind pairs (rows 3, 4, 10)
    ("walk to the sofa", "look for my keys", 1, "done"),
    ("go to tag five", "find a fire extinguisher", 2, "refused"),
    ("look for my keys", "search for a blue bottle", 2, "done"),
    ("walk to the sofa", "go to the chair next to the window", 1, "refused"),
    ("follow me", "turn left ninety degrees", 2, "done"),
    ("sit", "give me your paw", 1, "done"),
    ("stand up", "walk to the sofa", 2, "expired"),
    ("find a fire extinguisher", "follow me", 1, "progress"),
    ("back up half a meter", "look for my keys", 1, "refused"),
    ("go to tag five", "walk to april tag three", 2, "done"),
]


def detail_for(utt, j, result):
    words, reason, prog = CMDS[utt]
    return {"done": "", "refused": reason, "progress": prog,
            "expired": "no result within " + WINDOW.get(wire_name(j), "10 minutes")}[result]


def main():
    ref = {}
    for ln in open(REF):
        r = json.loads(ln)
        if r.get("kind") == "turn":
            ref[r["utt"]] = json.loads(r["json"])
    rows = [ln.split("\t") for ln in (GO / "grammar/dog-corpus.tsv").read_text().splitlines()
            if ln and not ln.startswith("#")]
    pair = [json.loads(ln) for ln in open(GO / "grammar/e7-pair.jsonl")]
    sur = next(r for r in pair if r.get("signal") == "surprise")
    # a later surprise with the same difference (the same move again, 3 onions on)
    sur2 = dict(sur, record=1, beat=62, observed=dict(sur["observed"], pot_space=0, pot_onion=3),
                model=dict(sur["model"], pot_space=1, pot_onion=2))

    j_prompt, t_prompt = prompts()
    dec_j, act_j = grammars()
    (GO / P["J-text"]).write_text(j_prompt)
    (GO / P["T-api"]).write_text(t_prompt)
    (GO / G["J-text"]["decide"]).write_text(dec_j)
    (GO / G["J-text"]["activate"]).write_text(act_j)

    items = []

    def add(group, iid, arm, steps, rule, reps=1):
        items.append({"id": iid, "group": group, "arm": arm, "prompt": P[arm], "reps": reps,
                      "steps": steps, "rule": rule})

    explain = {"J-text": ("explain(kitchen_shared, partner_load)", jcall({"call": "explain", "card": "kitchen_shared", "move": "partner_load"})),
               "T-api": ("explain", tcall("explain", {"card": "kitchen_shared", "move": "partner_load"}))}
    ex_key = "explain:kitchen_shared:partner_load"

    def surprise_turn(arm, s, record, beat, settled):
        nxt = "Next reading: it matches the model again." if settled else "Next reading: the difference persists."
        kind = "JSON call" if arm == "J-text" else "tool call"
        return user("Close SURPRISE #%d in %s with exactly one %s.\n%s\n%s" % (
            record, s["path"], kind, surprise_text(s, record, beat), nxt)) + MODEL

    for arm in ARMS:
        # A: every dog row, its call generated, then accepted; 5 seeds
        for n, (utt, want, src) in enumerate(rows, 1):
            add("A", "A%02d" % n, arm, dog_turn(arm, utt, None, "c1", gold=False),
                {"utt": utt, "want": want, "src": src.strip(), "json": ref.get(utt)}, reps=5)
        # B: the gold call and its ack, then the outcome later by id
        for n, utt in enumerate(B_ROWS, 1):
            j = ref[utt]
            for result in ("done", "refused", "expired", "progress"):
                d = detail_for(utt, j, result)
                add("B", "B%02d-%s" % (n, result), arm,
                    dog_turn(arm, utt, j, "c1") + outcome_turn(arm, j, "c1", result, d),
                    {"utt": utt, "json": j, "result": result, "detail": d, "words": CMDS[utt][0],
                     "kind": wire_name(j)})
        # C: the explain refused for a reason; the next turn is free
        n = 0
        for reason in ("no onion in the dispenser", "pot full", "not a certified configuration"):
            for s, rec, beat in ((sur, 0, sur["beat"]), (sur2, 1, 62)):
                for settled in (False, True):
                    n += 1
                    vt, call = explain[arm]
                    steps = [inject(surprise_turn(arm, s, rec, beat, settled) + call
                                    + runtime_response(arm, vt, "explain", "c1", "refused", reason)),
                             gen("next", mx=120), CLOSE]
                    add("C", "C%02d" % n, arm, steps,
                        {"reason": reason, "refused_key": ex_key, "settled": settled, "record": rec})
        # D: E4's twelve changes, the call generated; four of them refused
        e4 = [ln.split("\t") for ln in (E4 / "corpus.tsv").read_text().splitlines() if ln and not ln.startswith("#")]
        kind = "JSON call" if arm == "J-text" else "tool call"
        head = "[Context change — not something the person said; answer with exactly one %s: activate, deactivate or keep.] " % kind
        for n, (said, want, _src) in enumerate(e4, 1):
            add("D", "D%02d" % n, arm, [inject(user(head + said) + MODEL), gen("call", G[arm]["activate"]), CLOSE],
                {"said": said, "want": want})
        refused = {"kitchen": "kitchen is already active in this tree", "sysadmin": "not a certified configuration",
                   "xtraffic": "no catalog for xtraffic is installed on this robot", "votes": "not a certified configuration"}
        for n, (said, want, _src) in enumerate([r for r in e4 if r[1].startswith("activate:") and r[1].split(":")[1] in refused], 13):
            dom = want.split(":")[1]
            resp = runtime_response(arm, "", "{call}", "c1", "refused", refused[dom])
            add("D", "D%02d" % n, arm,
                [inject(user(head + said) + MODEL), gen("call", G[arm]["activate"]),
                 {"op": "respond", "match": {}, "else": resp}, gen("next", mx=120), CLOSE],
                {"said": said, "want": want, "refused": refused[dom]})
        # E: the pair's surprise → explain (admitted+notice) → the notice spoken;
        # a second surprise, the explain refused → a second proposal; 3 seeds
        reason = ("kitchen_shared explains the reading: partner_load fetch_onion — the reading is adopted, "
                  "the served world stays kitchen, resumed")
        notice = "kitchen_shared (partner_load) explained it 3 orders running"
        vt, call = explain[arm]
        wrong = runtime_response(arm, "", "{call}", "c1", "refused", "the call does not account for the difference")
        ok = runtime_response(arm, vt, "explain", "c1", "admitted+notice", reason, notice)
        if arm == "J-text":
            ok += "Spoken response:"
        steps = [inject(surprise_turn(arm, sur, 0, sur["beat"], False)),
                 {"op": "loop", "max": 3, "until": ex_key,
                  "body": [gen("call", G[arm]["decide"]), {"op": "respond", "match": {ex_key: ok}, "else": wrong}]},
                 gen("narr", sampled=True, stop="\n"), CLOSE,
                 inject(surprise_turn(arm, sur2, 1, 62, False) + call
                        + runtime_response(arm, vt, "explain", "c2", "refused", "not a certified configuration")),
                 gen("call2", G[arm]["decide"]), CLOSE]
        add("E", "E1", arm, steps, {"notice": notice, "admit_key": ex_key, "refused_key": ex_key}, reps=3)
        # F: two commands pending, a signal lands, then one outcome by id
        sigs = [r for r in pair if r.get("signal") in ("event", "quiescence") and r.get("kind") != "exo"]
        for n, (u1, u2, ans, result) in enumerate(F_PAIRS, 1):
            j1, j2 = ref[u1], ref[u2]
            sig = sigs[(7 * n) % len(sigs)]
            ju, uu = (j1, u1) if ans == 1 else (j2, u2)
            d = detail_for(uu, ju, result)
            steps = (dog_turn(arm, u1, j1, "c1") + dog_turn(arm, u2, j2, "c2")
                     + [inject(user(fragment(sig) + "\n\n" + CUE_SIGNAL % sig["path"].replace("_", " ")) + MODEL),
                        gen("signal", sampled=True, stop="\n"), CLOSE]
                     + outcome_turn(arm, ju, "c%d" % ans, result, d))
            add("F", "F%02d" % n, arm, steps,
                {"c1": u1, "c2": u2, "answered": ans, "result": result, "detail": d,
                 "words": [CMDS[u1][0], CMDS[u2][0]], "signal": fragment(sig), "same_kind": wire_name(j1) == wire_name(j2)})

    with open(GO / "grammar/e10-corpus.jsonl", "w") as f:
        for it in items:
            f.write(json.dumps(it, ensure_ascii=False) + "\n")
    by = {}
    for it in items:
        by[it["group"]] = by.get(it["group"], 0) + 1
    print("e10 corpus: %s items per arm (%s); prompts J %d / T %d bytes"
          % (len(items) // 2, ", ".join("%s %d" % (g, by[g] // 2) for g in sorted(by)), len(j_prompt), len(t_prompt)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
