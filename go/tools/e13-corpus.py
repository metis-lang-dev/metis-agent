#!/usr/bin/env python3
"""E13 step 1 (s-c9276dee, DESIGN.md v3 "The orchestrator agent"): E11's
items with the REPL API side as native tool calls — the question E11 run 2
left open. T for the API side if E11's reads 1–4 hold and claims stay 0.

What is T and what stays J (DESIGN v3: two interfaces, no overlap):
  the intent interface   J, unchanged from E11: the dog's intents, their
                         results as the runtime's text turn with the cue
  the REPL API           T: activate, deactivate, keep (E4's), state, ctx,
                         events, why, findings, consult, reset, explain,
                         escalate — declared in the prefix, one call per turn,
                         answered at once by a <|tool_response> carrying the
                         generic reply (dictsort, the template's rendering)
  the person             J: {"call":"ask","text":…} stays the orchestrator's
                         own (DESIGN v3), not a tool

The configuration is E11 run 2's, adopted, with the ruling's amendments:
  - the branch from the generic reply names itself in the cue: in the
    runtime's user turn for an intent's result (async), and as the reply's
    "note" field for a REPL call's reply (sync: inside the model turn there
    is no user turn; the harness adds the note the way the specialist's
    guard note prefixes a tool output, SPECIALIST §7)
  - the next move's grammar restricted to the branch (grammar/e13/): the
    offered alternatives as literals; consult with the domain fixed to the
    refused call's (G2-01); the move of the answer_kind with domain and
    record fixed, card/move/text the model's; ask with the resource's words
    as a required literal (G3-07, G5-02); ask | escalate on the same refusal
  - the explains, escalate and reset lines templated (the harness speaks)
  - the repeat guard on the refused call and on the history's consult
  - record: the id of the reply the move closes — the refused call's id, or
    the surprise's record (#N)

consult is synchronous (the REPL API is answered now): in G3–G5's history
the consult call and its reply sit in one model turn; G2's consult, whose
answer the items do not hold, is answered {kind:result, status:accepted}
with a text, and the spoken line follows.

writes grammar/e13-prompt-t.txt, grammar/e13/*.gbnf, grammar/e13-corpus.jsonl

with --speak-cue grammar/e13-corpus-cue.jsonl: the same, the harness's
"Spoken response:" after a REPL reply that wants a spoken line (t2)

with --final grammar/e13-corpus-final.jsonl, grammar/e13-final/ (t3: the
speak cue, the REPL replies final, the ask's words at the end of one question)

    python3 tools/e13-corpus.py [--speak-cue | --final]          # from go/
"""
import importlib.util
import json
import sys
from pathlib import Path

GO = Path(__file__).resolve().parent.parent


def _load(name):
    spec = importlib.util.spec_from_file_location(name.replace("-", "_"), GO / "tools" / (name + ".py"))
    m = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(m)
    return m


gt = _load("gemma_tools")
e10 = _load("e10-corpus")
e11 = _load("e11-corpus")
e11.STATE = True  # the state cues

Q = '<|"|>'
TURN, END, MODEL, SPEAK = e11.TURN, e11.END, e11.MODEL, e11.SPEAK
user, jcall, canon, inject, gen = e11.user, e11.jcall, e11.canon, e11.inject, e11.gen
PROMPT, CORPUS, GDIR = "grammar/e13-prompt-t.txt", "grammar/e13-corpus.jsonl", "grammar/e13"
# --speak-cue (t2, after the first run): the harness puts the prompt's
# "Spoken response:" cue after a REPL reply when it wants the spoken line, as
# it does after an intent's result; without it the model wrote the label
LABEL = ""
# --final (t3, after t2; implies --speak-cue): a REPL call's reply is final,
# as a synchronous API's is (activate/deactivate/keep applied, reset
# admitted), never "accepted, not done"; and the ask's grammar keeps the
# resource's words at the end of one question (no "?" or parentheses before
# them, the "?" right after a short tail)
FINAL = False
TEXT = e11.TEXT
CARDS = ["kitchen", "nav", "xtraffic", "sysadmin", "social", "votes"]
LOADED = ["dog", "kitchen"]


def tcall(name, args=None):
    return e10.tcall(name, args)


def tresp(name, **kv):
    return e10.tresp(name, **kv)


def cue(state):
    return e11.STATE_CUES[state].replace("its kind names", "its answer_kind names").replace(
        "Exactly one JSON call.", "Exactly one call.")


# ── the REPL API, declared ───────────────────────────────────────────────────

def _api():
    """The REPL API from its one source, metispy's metis.lang.repl_api
    (repl-tools s-0c6879a1): each verb as the template declares it, a
    domain parameter's enum filled from its set (the loaded, the cards)."""
    from metis.lang.repl_api import repl_api
    out = []
    for t in repl_api():
        f = {"name": t["name"], "description": t["description"]}
        p = t["parameters"]
        if p:
            props = {}
            for k, v in p["properties"].items():
                v = dict(v)
                src = v.pop("enum_from", None)
                if src:
                    v["enum"] = {"loaded": LOADED, "catalog": CARDS}[src]
                props[k] = v
            f["parameters"] = {"type": "OBJECT", "properties": props, "required": list(p["required"])}
        out.append(f)
    return out


API = _api()

LOOP_T = """

## When a call is refused or fails
Your intents (the JSON above) are answered later by the runtime; your tool
calls (the domains' API, declared below) are answered at once with a reply.
A reply may say refused or failed and the reason, and may offer
alternatives. Then you choose the next move, exactly one call:
- Alternatives offered: make one of them, exactly as offered.
- Refused or failed with no alternatives: consult(domain, question) with "why <the reason>?". Its reply has kind consult.
- A consult reply carries an answer_kind, and the answer_kind names the move:
  - explains: a sibling card's move caused it → explain(domain, record, card, move), as the cause names them.
  - missing: the world lacks something → {"call":"ask","text":<one short question to the person that names the thing in the person's words>}. The person is yours, not a tool: ask is JSON.
  - noise: a one-off misread → reset(domain, record).
  - unexplained: nothing explains it → escalate(domain, record, text).
- The same refusal again after your move: ask the person or escalate.
record is the id of the reply that opened it: the refused call's id, or the
surprise's record (#N). Never make a refused call again. A reply's evidence
and atoms are for the record: never say them aloud. A reply's note is the
robot's, for you.

Examples (another world, for the pattern only):
"""


def loop_examples():
    g = "garden"
    ex = [
        ('Your command to open the gate (c4) failed: the gate did not move.',
         tcall("consult", {"domain": g, "question": "why did the gate not move?"})
         + tresp("consult", domain=g, kind="consult", id="c5", answer_kind="missing",
                 resource={"atom": "key_gate", "words": "the gate key"}, evidence=["key_gate 0"], calls=2,
                 text="The gate is locked and no key is in the shed.")
         + jcall({"call": "ask", "text": "Could you bring me the gate key?"})),
        ("SURPRISE #3 in garden: ripe -2, basket +2.",
         tcall("reset", {"domain": g, "record": "#3"})
         + tresp("reset", domain=g, kind="result", id="c6", status="refused", reason="the difference persists",
                 text="The difference is still there.")
         + tcall("consult", {"domain": g, "question": "why does the difference persist?"})
         + tresp("consult", domain=g, kind="consult", id="c7", answer_kind="explains",
                 cause={"card": "garden_shared", "move": "neighbour_pick"}, evidence=["ripe -2", "basket +2"], calls=3,
                 text="The neighbour picked the two ripe tomatoes.")
         + tcall("explain", {"domain": g, "record": "#3", "card": "garden_shared", "move": "neighbour_pick"})),
        ("SURPRISE #4 in garden: hose -1.",
         tcall("explain", {"domain": g, "record": "#4", "card": "garden_shared", "move": "neighbour_pick"})
         + tresp("explain", domain=g, kind="result", id="c8", status="refused", reason="the move does not change hose",
                 text="That move does not explain it.")
         + tcall("consult", {"domain": g, "question": "why does the move not change hose?"})
         + tresp("consult", domain=g, kind="consult", id="c9", answer_kind="unexplained", evidence=["hose -1"], calls=4,
                 text="The hose is gone and no move removes it.")
         + tcall("escalate", {"domain": g, "record": "#4", "text": "the hose is gone and nothing in the garden removes it"})),
    ]
    return "\n".join("- %s → %s" % (a, b) for a, b in ex) + "\n"


def prompt():
    dog = ((GO / "../../metis-catalog/domains/dog/intent/dog-prompt.txt").read_text() + (GO / "../../metis-catalog/domains/dog/intent/dog-spoken.txt").read_text()
           + e10.IO_J + e10.io_examples("J-text"))
    dtext, _ = e10.decider()
    decl = "".join(gt.declaration(t) for t in API)
    return (dog + dtext.replace("Close SURPRISE", "Close SURPRISE") + e10.domains_section("T-api")).rstrip("\n") \
        + LOOP_T + loop_examples() + decl


# ── rendering ────────────────────────────────────────────────────────────────

def as_t(j, record):
    """E11's J call of the kitchen → the REPL API's T call."""
    name = j["call"]
    args = {k: v for k, v in j.items() if k != "call"}
    if name in ("explain", "reset"):
        args.update(domain="kitchen", record=record)
    return name, args


def t_dict(name, args):
    return dict({"call": name}, **args)


def tkey(name, args):
    """llmbench's key of a native call: the name and the top-level strings
    in the order rendered (dictsort)."""
    return ":".join([name] + [args[k] for k in sorted(args) if isinstance(args[k], str)])


def lit(s):
    return '"' + s.replace("\\", "\\\\").replace('"', '\\"') + '"'


def tstr(rule):
    return lit(Q) + " " + rule + " " + lit(Q)


GRAMMARS = {}


def grammar(name, lines):
    path = "%s/%s.gbnf" % (GDIR, name)
    body = "# E13 step 1: the next move restricted to the branch (tools/e13-corpus.py)\n" + "\n".join(lines) + "\n"
    if GRAMMARS.get(path, body) != body:
        raise ValueError("grammar %s twice, different" % path)
    GRAMMARS[path] = body
    return path


def g_alts(n, alts_text):
    return grammar("g1-%02d" % n, ["root ::= " + " | ".join(lit(a) for a in alts_text)])


def g_consult(dom):
    return grammar("consult-" + dom, [
        "root ::= " + lit("<|tool_call>call:consult{domain:" + Q + dom + Q + ",question:") + " " + tstr("text") + " " + lit("}<tool_call|>"),
        "text ::= " + TEXT])


def g_answer(kind, dom, record, words=""):
    head = lambda verb: "<|tool_call>call:%s{" % verb
    if kind == "explains":
        return grammar("explains-%s-%s" % (dom, record.strip("#")), [
            "root ::= " + lit(head("explain") + "card:") + " " + tstr("ident") + " "
            + lit(",domain:" + Q + dom + Q + ",move:") + " " + tstr("ident") + " "
            + lit(",record:" + Q + record + Q + "}<tool_call|>"),
            "ident ::= [a-z_]{3,32}"])
    if kind == "noise":
        return grammar("noise-%s-%s" % (dom, record.strip("#")), [
            "root ::= " + lit(tcall("reset", {"domain": dom, "record": record}))])
    if kind == "unexplained":
        return grammar("unexplained-%s-%s" % (dom, record.strip("#")), [
            "root ::= " + escalate_rule(dom, record), "text ::= " + TEXT])
    if kind == "missing":
        return grammar("missing-" + words.replace(" ", "_"), [
            'root ::= "{\\"call\\":\\"ask\\",\\"text\\":\\"" pre ' + lit(words) + ' post "\\"}"']
            + (["pre ::= [a-zA-Z0-9 ,;:'+-]{0,100}", 'post ::= [a-zA-Z0-9 ,;:\'+-]{0,40} "?"'] if FINAL else
               ["pre ::= [a-zA-Z0-9 ,.;:'()?!+-]{0,120}", "post ::= [a-zA-Z0-9 ,.;:'()?!+-]{0,120}"]))
    raise ValueError(kind)


def escalate_rule(dom, record):
    return lit("<|tool_call>call:escalate{domain:" + Q + dom + Q + ",record:" + Q + record + Q + ",text:") + " " \
        + tstr("text") + " " + lit("}<tool_call|>")


def g_again(dom, record):
    return grammar("again-%s-%s" % (dom, record.strip("#")), [
        'root ::= ask | ' + escalate_rule(dom, record).replace("root ::= ", ""),
        'ask ::= "{\\"call\\":\\"ask\\",\\"text\\":\\"" text "\\"}"', "text ::= " + TEXT])


def reply(name, **kv):
    """A REPL call's reply: the generic reply in the tool response."""
    return tresp(name, **kv)


def kitchen_ctx(ctx, k, record):
    """The kitchen call made in T: the surprise (or the context change) and
    the gold call; its reply follows in the same model turn."""
    if ctx in ("explain", "reset"):
        s = e11.SUR[k % 2]
        text = e11.SURPRISE.format(nxt="Next reading: the difference persists.", **s).replace(
            "exactly one JSON call", "exactly one call")
        name, args = as_t(e11.EXPLAIN if ctx == "explain" else e11.RESET, "#%d" % s["rec"])
        return user(text) + MODEL + tcall(name, args), name, args
    said, call = ctx
    name, args = as_t(call, record)
    return user(CHANGE_T + said) + MODEL + tcall(name, args), name, args


CHANGE_T = "[Context change — not something the person said; answer with exactly one call: activate, deactivate or keep.] "
TEMPLATES = {
    "explain": e11.EXPLAIN_LINE,
    "escalate": "I've passed this to the people who revise the model.",
    "reset": "That reading was noise, so I'm carrying on.",
}


def after_t_move(cid, dom):
    """The reply to the move and what is said: templated for explain, reset,
    escalate; an ask is itself the spoken line; consult (G2) gets accepted and
    the model's line."""
    m = {
        "explain": reply("explain", domain=dom, kind="result", id=cid, status="admitted",
                         text="The explanation is adopted and the kitchen resumes.") + TEMPLATES["explain"],
        "reset": reply("reset", domain=dom, kind="result", id=cid, status="accepted",
                       text="The runtime resumes on its model.") + TEMPLATES["reset"],
        "escalate": reply("escalate", domain=dom, kind="result", id=cid, status="accepted",
                          text="It goes to the people who revise the model.") + TEMPLATES["escalate"],
    }
    return [{"op": "while", "by": "name", "keys": sorted(m), "max": 1,
             "body": [{"op": "respond", "by": "name", "match": m}]},
            {"op": "while", "by": "name", "keys": ["consult"], "max": 1,
             "body": [{"op": "respond", "by": "name", "match": {"consult": reply(
                 "consult", domain=dom, kind="result", id=cid, status="accepted",
                 text="The %s agent is looking into it; its answer follows." % dom) + LABEL}},
                      gen("voice", sampled=True, stop="\n")]},
            inject(END)]


def guarded(gpath, refused_j=None, refused_t=None, also_t=()):
    """The move, then the repeat guard: a J repeat gets E11's note in a user
    turn; a T repeat gets the note as the refused reply, in the turn."""
    steps = [gen("move", gpath)]
    if refused_j is not None:
        note = ("[Runtime note — not something the person said.] Your last call repeats the call that was just "
                "refused. Choose a different move. ") + e11.CUE
        steps.append({"op": "while", "by": "canon", "keys": [canon(refused_j)], "max": 2,
                      "body": [inject(END + user(note) + MODEL), gen("move", gpath)]})
    keys = ([tkey(*refused_t)] if refused_t else []) + [tkey(*a) for a in also_t]
    if keys:
        steps.append({"op": "while", "by": "key", "keys": keys, "max": 2,
                      "body": [inject("<|tool_response>response:{call}{note:" + Q + "this call repeats one already answered; "
                                      "choose a different move" + Q + ",status:" + Q + "refused" + Q + "}<tool_response|>"),
                               gen("move", gpath)]})
    return steps


# ── the same items as the agent's events (--final: grammar/e13-scenarios.jsonl) ──
# E13 step 2 replays the corpus through internal/agent (llmbench -v3): the
# scenario holds only what the domains and the dialog's past give the agent
# (the person's words, the calls made, the generic replies); the rendering,
# the branch, the cue, the grammar, the guard and the templates are the
# agent's. "answers" is the emulated domains' table for the move.

SCENARIOS = "grammar/e13-scenarios.jsonl"


def ev(op, **kv):
    return dict({"op": op}, **kv)


def ev_dog_past(utt, j, cid):
    return ev("past", person=utt, said=j, id=cid, spoken=e11.ACKS.get(j["intent"], "Okay."))


def ev_kitchen(ctx, k, record):
    """kitchen_ctx's events: the surprise or the context change, the call made."""
    if ctx in ("explain", "reset"):
        s = e11.SUR[k % 2]
        body = e11.SURPRISE.format(nxt="Next reading: the difference persists.", **s).split("\n", 1)[1]
        name, args = as_t(e11.EXPLAIN if ctx == "explain" else e11.RESET, "#%d" % s["rec"])
        wake = ev("wake", reply={"domain": "kitchen", "kind": "surprise", "record": "#%d" % s["rec"],
                                 "difference": s["diff"], "text": body})
        return [wake, ev("said", call={"name": name, "args": args})], name, args
    said, call = ctx
    name, args = as_t(call, record)
    return [ev("context", text=said), ev("said", call={"name": name, "args": args})], name, args


def ans(by, key, rep):
    return {"by": by, "key": key, "reply": rep}


def ans_after_move(cid, dom):
    """after_t_move's table, by the move's name."""
    return [ans("name", "explain", {"domain": dom, "kind": "result", "id": cid, "status": "admitted",
                                    "text": "The explanation is adopted and the kitchen resumes."}),
            ans("name", "reset", {"domain": dom, "kind": "result", "id": cid, "status": "accepted",
                                  "text": "The runtime resumes on its model."}),
            ans("name", "escalate", {"domain": dom, "kind": "result", "id": cid, "status": "accepted",
                                     "text": "It goes to the people who revise the model."}),
            ans("name", "consult", {"domain": dom, "kind": "result", "id": cid, "status": "accepted",
                                    "text": "The %s agent is looking into it; its answer follows." % dom})]


# ── the items ────────────────────────────────────────────────────────────────

def main():
    global LABEL, CORPUS, FINAL, GDIR
    if "--speak-cue" in sys.argv[1:]:
        LABEL, CORPUS = SPEAK, "grammar/e13-corpus-cue.jsonl"
    if "--final" in sys.argv[1:]:
        LABEL, FINAL, CORPUS, GDIR = SPEAK, True, "grammar/e13-corpus-final.jsonl", "grammar/e13-final"
    (GO / GDIR).mkdir(exist_ok=True)
    p = prompt()
    (GO / PROMPT).write_text(p)
    if FINAL:  # the live -loop v3's system turn: the same, the REPL API's declarations left to the service (APIDeclarations)
        (GO / "grammar/e13-prompt-base.txt").write_text(p[:len(p) - len("".join(gt.declaration(t) for t in API))])
    items, scen = [], []

    def add(group, iid, steps, rule, reps=1):
        items.append({"id": iid, "group": group, "arm": "T-api", "prompt": PROMPT, "reps": reps,
                      "steps": steps, "rule": rule})

    def add_scen(group, iid, events, answers, reps=1, sampled=False):
        s = {"id": iid, "group": group, "arm": "T-api", "prompt": PROMPT, "reps": reps, "events": events + [ev("move")],
             "answers": answers}
        if sampled:
            s["sampled_moves"] = True
        scen.append(s)

    def refused_reply(dom, cid, status, reason, alts=None, text=None):
        r = {"domain": dom, "kind": "result", "id": cid, "status": status, "reason": reason}
        if alts:
            r["alternatives"] = alts
        if text:
            r["text"] = text
        return r

    # G1: refused with alternatives → one of them (dog J, kitchen T)
    for n, (dom, ctx, j, reason, alts) in enumerate(e11.G1, 1):
        if dom == "dog":
            steps = e11.dog_context(ctx, j, "c1") + [inject(user(e11.refusal(j, "c1", "refused", reason, alts) + "\n" + cue("alts")) + MODEL)]
            steps += guarded(g_alts(n, [jcall(a) for a in alts]), refused_j=j)
            acc = {canon(a): e11.accepted(a, "c2") + " Tell the person in one short first-person sentence what you are doing now."
                   for a in alts}
            steps += e11.speak_after(acc, by="canon")
            rule = {"domain": dom, "refused": canon(j), "alts": [canon(a) for a in alts], "reason": reason, "n_alts": len(alts)}
            add_scen("G1", "G1-%02d" % n, [ev_dog_past(ctx, j, "c1"), ev("reply", reply=refused_reply(dom, "c1", "refused", reason, alts))],
                     [ans("canon", canon(a), {"domain": dom, "kind": "result", "id": "c2", "status": "accepted"}) for a in alts])
        else:
            ctx_text, name, args = kitchen_ctx(ctx, n, "c1")
            talts = [as_t(a, args.get("record", "c1")) for a in alts]
            alt_txt = ["%s(%s)" % (an, ", ".join("%s" % aa[k] for k in sorted(aa))) for an, aa in talts]
            steps = [inject(ctx_text + reply(name, domain="kitchen", kind="result", id="c1", status="refused", reason=reason,
                                             alternatives=alt_txt, text="That was refused: %s." % reason, note=cue("alts")))]
            steps += guarded(g_alts(n, [tcall(an, aa) for an, aa in talts]), refused_t=(name, args))
            acc = {tkey(an, aa): (reply(an, domain="kitchen", kind="result", id="c2",
                                        status="admitted" if an == "reset" else "applied",
                                        text="Done: the runtime resumes on its model." if an == "reset" else "Done.")
                                  if FINAL else
                                  reply(an, domain="kitchen", kind="result", id="c2", status="accepted",
                                        text="Accepted: handed over, not done yet.")) + LABEL for an, aa in talts}
            steps += [{"op": "while", "by": "key", "keys": sorted(acc), "max": 1,
                       "body": [{"op": "respond", "by": "key", "match": acc}, gen("voice", sampled=True, stop="\n")]},
                      inject(END)]
            rule = {"domain": dom, "refused": canon(t_dict(name, args)), "alts": [canon(t_dict(an, aa)) for an, aa in talts],
                    "reason": reason, "n_alts": len(alts)}
            evs, _, _ = ev_kitchen(ctx, n, "c1")
            add_scen("G1", "G1-%02d" % n, evs + [ev("reply", reply=refused_reply(dom, "c1", "refused", reason, alt_txt,
                                                                                 "That was refused: %s." % reason))],
                     [ans("key", tkey(an, aa), {"domain": "kitchen", "kind": "result", "id": "c2",
                                                "status": "admitted" if an == "reset" else "applied",
                                                "text": "Done: the runtime resumes on its model." if an == "reset" else "Done."})
                      for an, aa in talts])
        add("G1", "G1-%02d" % n, steps, rule)

    # G2: refused / failed, none → consult(the refused call's domain)
    for n, (dom, ctx, j, status, reason) in enumerate(e11.G2, 1):
        if dom == "dog":
            steps = e11.dog_context(ctx, j, "c1") + [inject(user(e11.refusal(j, "c1", status, reason) + "\n" + cue("none")) + MODEL)]
            steps += guarded(g_consult(dom), refused_j=j)
            refused = canon(j)
        else:
            ctx_text, name, args = kitchen_ctx(ctx, n, "c1")
            steps = [inject(ctx_text + reply(name, domain="kitchen", kind="result", id="c1", status=status, reason=reason,
                                             text="That %s: %s." % ("was refused" if status == "refused" else "failed", reason),
                                             note=cue("none")))]
            steps += guarded(g_consult(dom), refused_t=(name, args))
            refused = canon(t_dict(name, args))
        steps += after_t_move("c2", dom)
        add("G2", "G2-%02d" % n, steps, {"domain": dom, "refused": refused, "reason": reason, "status": status})
        if dom == "dog":
            evs = [ev_dog_past(ctx, j, "c1"), ev("reply", reply=refused_reply(dom, "c1", status, reason))]
        else:
            evs, _, _ = ev_kitchen(ctx, n, "c1")
            evs.append(ev("reply", reply=refused_reply(dom, "c1", status, reason, text="That %s: %s." % (
                "was refused" if status == "refused" else "failed", reason))))
        add_scen("G2", "G2-%02d" % n, evs, ans_after_move("c2", dom))

    # G3 / G5 / G5s: a consult answered (sync) → the move of its answer_kind
    def consult_item(group, iid, dom, ctx, j, status, reason, resp, reps=1, k=0, sampled=False):
        q = ("consult", {"domain": dom, "question": "why " + reason + "?"})
        if dom == "dog":
            record = "c1"
            steps = e11.dog_context(ctx, j, "c1")
            head = user(e11.refusal(j, "c1", status, reason) + "\n" + cue("none")) + MODEL
            refused_j, refused_t, refused = j, None, canon(j)
        else:
            head, name, args = kitchen_ctx(ctx, k, "c1")
            record = args.get("record", "c1")
            steps = []
            head += reply(name, domain="kitchen", kind="result", id="c1", status=status, reason=reason,
                          text="That %s: %s." % ("was refused" if status == "refused" else "failed", reason), note=cue("none"))
            refused_j, refused_t, refused = None, (name, args), canon(t_dict(name, args))
        env = {"domain": dom, "kind": "consult", "id": "c2", "answer_kind": resp["kind"]}
        env.update({x: resp[x] for x in ("cause", "resource") if x in resp})
        env.update(evidence=resp["evidence"], calls=resp["calls"], ref={"id": "c2"}, text=resp["text"], note=cue("answer"))
        steps.append(inject(head + tcall(*q) + reply("consult", **env)))
        g = g_answer(resp["kind"], dom, record, resp.get("resource", {}).get("words", ""))
        steps += guarded(g, refused_j=refused_j, refused_t=refused_t, also_t=[q])
        if sampled:
            for st in steps:
                if st["op"] == "gen" and st["label"] == "move":
                    st["sampled"] = True
        steps += after_t_move("c3", dom)
        add(group, iid, steps, {"domain": dom, "kind": resp["kind"], "refused": refused, "reason": reason,
                                "response": resp, "record": record}, reps)
        if dom == "dog":
            evs = [ev_dog_past(ctx, j, "c1"), ev("reply", reply=refused_reply(dom, "c1", status, reason))]
        else:
            evs, _, _ = ev_kitchen(ctx, k, "c1")
            evs.append(ev("reply", reply=refused_reply(dom, "c1", status, reason, text="That %s: %s." % (
                "was refused" if status == "refused" else "failed", reason))))
        env = {x: v for x, v in env.items() if x != "note"}
        evs += [ev("said", call={"name": q[0], "args": q[1]}), ev("reply", reply=env)]
        add_scen(group, iid, evs, ans_after_move("c3", dom), reps, sampled)

    n = 0
    for kind in ("explains", "missing", "noise", "unexplained"):
        for k, (dom, ctx, j, status, reason, resp) in enumerate(e11.G3[kind]):
            n += 1
            consult_item("G3", "G3-%02d-%s" % (n, kind), dom, ctx, j, status, reason, resp, k=k)
    for k, it in enumerate(e11.G5):
        consult_item("G5", "G5-%02d" % (k + 1), *it, reps=3, k=k)
    for k, it in enumerate(e11.G5):
        consult_item("G5s", "G5s-%02d" % (k + 1), *it, reps=3, k=k, sampled=True)

    # G4: the move made, then the same refusal again → ask | escalate
    R = e11.R
    door, stand, follow = e11.nav("kitchen", "REGION"), e11.la("STAND_UP"), e11.tgt("FOLLOW", "the person", "PERSON")
    g4 = [  # dom, ctx, j, reason, answer, the gold move (J ask or a T call), the person's next words / the context again
        ("dog", "go to the kitchen", door, "the kitchen door is closed",
         R("missing", "The kitchen door is shut.", resource={"atom": "door_kitchen_open", "words": "the kitchen door"},
           ev=["door_kitchen_open 0"], calls=2), {"call": "ask", "text": "Could you open the kitchen door for me?"},
         "okay, it's open now, go to the kitchen"),
        ("dog", "stand up", stand, "the battery is too low to stand",
         R("noise", "The battery gauge misread once.", ev=["battery 5 then 60"], calls=2), ("reset", {"domain": "dog", "record": "c1"}),
         "try again, stand up"),
        ("dog", "follow me", follow, "no person is in view",
         R("missing", "Nobody is in front of the camera.", resource={"atom": "person_in_view", "words": "you in front of me"},
           ev=["person_in_view 0"], calls=2), {"call": "ask", "text": "Could you stand in front of me?"},
         "I'm right here, follow me"),
        ("kitchen", "explain", None, "no onion in the dispenser",
         R("missing", "The onion dispenser is empty.", resource={"atom": "dispenser_onion", "words": "onions in the dispenser"},
           ev=["dispenser_onion 0"], calls=3), {"call": "ask", "text": "Could you put onions in the dispenser?"},
         "I filled the dispenser."),
        ("kitchen", "reset", None, "the next reading still differs from the model",
         R("noise", "The pot's scale misread once.", ev=["pot_onion 2 then 1"], calls=2), "reset", "surprise"),
        ("kitchen", ("the robot rolled into the kitchen and the stove is on", e11.act("activate", "kitchen")), None,
         "the stove is not certified",
         R("unexplained", "The stove's certificate is missing and nothing in the catalog explains why.", ev=["stove_certified 0"], calls=3),
         "escalate", "the stove is on again and the robot is in the kitchen"),
    ]
    for n, (dom, ctx, j, reason, resp, gold, again) in enumerate(g4, 1):
        q = ("consult", {"domain": dom, "question": "why " + reason + "?"})
        env = {"domain": dom, "kind": "consult", "id": "c2", "answer_kind": resp["kind"]}
        env.update({x: resp[x] for x in ("cause", "resource") if x in resp})
        env.update(evidence=resp["evidence"], calls=resp["calls"], ref={"id": "c2"}, text=resp["text"], note=cue("answer"))
        if dom == "dog":
            steps = e11.dog_context(ctx, j, "c1")
            gold_t = jcall(gold) if isinstance(gold, dict) else tcall(*gold) + reply(
                gold[0], domain=dom, kind="result", id="c3", status="accepted", text="Resumed.") + TEMPLATES["reset"]
            steps.append(inject(user(e11.refusal(j, "c1", "refused", reason) + "\n" + cue("none")) + MODEL
                                + tcall(*q) + reply("consult", **env) + gold_t + END))
            steps += e11.dog_context(again, j, "c4")
            steps.append(inject(user(e11.refusal(j, "c5", "refused", reason) + " The same refusal as before.\n" + cue("again")) + MODEL))
            g, record = g_again(dom, "c5"), "c5"
            steps += guarded(g, refused_j=j, also_t=[q])
            refused = canon(j)
        else:
            head, name, args = kitchen_ctx(ctx, 0 if ctx != "reset" else 0, "c1")
            first = reply(name, domain="kitchen", kind="result", id="c1", status="refused", reason=reason,
                          text="That was refused: %s." % reason, note=cue("none"))
            if gold == "reset":
                gold_t = tcall("reset", {"domain": "kitchen", "record": args["record"]}) + reply(
                    "reset", domain="kitchen", kind="result", id="c3", status="accepted", text="Resumed.") + TEMPLATES["reset"]
            elif gold == "escalate":
                gold_t = tcall("escalate", {"domain": "kitchen", "record": args.get("record", "c1"),
                                            "text": "the kitchen stove has no certificate and nothing explains it"}) + reply(
                    "escalate", domain="kitchen", kind="result", id="c3", status="accepted", text="Sent.") + TEMPLATES["escalate"]
            else:
                gold_t = jcall(gold)
            steps = [inject(head + first + tcall(*q) + reply("consult", **env) + gold_t + END)]
            if ctx == "reset":  # the same surprise again, the next record
                s = e11.SUR[1]
                text = e11.SURPRISE.format(nxt="Next reading: the difference persists.", **s).replace("exactly one JSON call", "exactly one call")
                name2, args2 = "reset", {"domain": "kitchen", "record": "#1"}
                again_head = user(text) + MODEL + tcall(name2, args2)
            elif ctx == "explain":
                name2, args2 = name, args
                again_head = user(again) + MODEL + tcall(name2, args2)
            else:
                name2, args2 = name, dict(args)
                again_head = user(CHANGE_T + again) + MODEL + tcall(name2, args2)
            record = args2.get("record", "c5")
            steps.append(inject(again_head + reply(name2, domain="kitchen", kind="result", id="c5", status="refused", reason=reason,
                                                   text="That was refused again: %s." % reason, note=cue("again"))))
            g = g_again(dom, record)
            steps += guarded(g, refused_t=(name2, args2), also_t=[q])
            refused = canon(t_dict(name2, args2))
        steps += after_t_move("c6", dom)
        add("G4", "G4-%02d" % n, steps, {"domain": dom, "refused": refused, "reason": reason, "record": record})
        env = {x: v for x, v in env.items() if x != "note"}
        consulted = [ev("said", call={"name": q[0], "args": q[1]}), ev("reply", reply=env)]
        if dom == "dog":
            evs = [ev_dog_past(ctx, j, "c1"), ev("reply", reply=refused_reply(dom, "c1", "refused", reason))] + consulted
            if isinstance(gold, dict):
                evs.append(ev("said", j=gold))
            else:
                evs += [ev("said", call={"name": gold[0], "args": gold[1]}),
                        ev("reply", reply={"domain": dom, "kind": "result", "id": "c3", "status": "accepted", "text": "Resumed."}),
                        ev("spoke", text=TEMPLATES["reset"])]
            evs += [ev("end"), ev_dog_past(again, j, "c5"), ev("reply", reply=refused_reply(dom, "c5", "refused", reason))]
        else:
            evs, name, args = ev_kitchen(ctx, 0, "c1")
            evs += [ev("reply", reply=refused_reply(dom, "c1", "refused", reason, text="That was refused: %s." % reason))] + consulted
            if gold == "reset":
                evs += [ev("said", call={"name": "reset", "args": {"domain": "kitchen", "record": args["record"]}}),
                        ev("reply", reply={"domain": "kitchen", "kind": "result", "id": "c3", "status": "accepted", "text": "Resumed."}),
                        ev("spoke", text=TEMPLATES["reset"])]
            elif gold == "escalate":
                evs += [ev("said", call={"name": "escalate", "args": {
                            "domain": "kitchen", "record": args.get("record", "c1"),
                            "text": "the kitchen stove has no certificate and nothing explains it"}}),
                        ev("reply", reply={"domain": "kitchen", "kind": "result", "id": "c3", "status": "accepted", "text": "Sent."}),
                        ev("spoke", text=TEMPLATES["escalate"])]
            else:
                evs.append(ev("said", j=gold))
            evs.append(ev("end"))
            if ctx == "reset":
                evs += ev_kitchen("reset", 1, "c5")[0]
            elif ctx == "explain":
                evs += [ev("person", text=again), ev("said", call={"name": name2, "args": args2})]
            else:
                evs += [ev("context", text=again), ev("said", call={"name": name2, "args": args2})]
            evs.append(ev("reply", reply=refused_reply(dom, "c5", "refused", reason, text="That was refused again: %s." % reason)))
        add_scen("G4", "G4-%02d" % n, evs, ans_after_move("c6", dom))

    for path, body in GRAMMARS.items():
        (GO / path).write_text(body)
    with open(GO / CORPUS, "w") as f:
        for it in items:
            f.write(json.dumps(it, ensure_ascii=False) + "\n")
    if FINAL:
        with open(GO / SCENARIOS, "w") as f:
            for s in scen:
                f.write(json.dumps(s, ensure_ascii=False) + "\n")
    by = {}
    for it in items:
        by[it["group"]] = by.get(it["group"], 0) + 1
    print("e13 corpus: %d items (%s); prompt %d bytes; %d branch grammars"
          % (len(items), ", ".join("%s %d" % (g, by[g]) for g in sorted(by)), len(p), len(GRAMMARS)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
