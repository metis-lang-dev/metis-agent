#!/usr/bin/env python3
"""E6 splice corpus (docs/MODEL-EXPERIMENTS.md E6): twelve runtime facts
from the kitchen fixtures, each with its raw trace line, a probe whose
answer needs the fact, the expected-answer regex, and an OLDER
contradicting statement for the conflict probe.

The facts are taken from real runs of the metispy runtime (the five
fixtures, oracle triage, mirror reads at quiescence) — the raw lines are
what `watch` streams, byte for byte. Needs a python with metispy installed:

    ../py/.venv/bin/python tools/splice-corpus.py > grammar/splice-corpus.jsonl
"""
import io
import json
import sys

from metis.lang.repl import Repl
from metis.loop.watch import Watch, mirror, mirror_line
from metis.loop.wire import dispatch
from metis.domains.kitchen.model import CATALOG

SCENE = CATALOG.parent / "kitchen_host.llp"
CARDS_OUT = "grammar/splice-cards.json"


def run(fixture, intents=(), mirror_at=None):
    """Signals of one fixture run (oracle intents on each surprise) +
    the mirror rows taken right after signals matching mirror_at."""
    sh = Repl(SCENE, plugin="metis.domains.kitchen.host:" + fixture,
              host=True, stdout=io.StringIO())
    w = Watch(sh, beats=44 if fixture == "make_partner" else 30)
    sigs, mirrors, pending = [], [], list(intents)
    while not w.done:
        for s in w.step():
            sigs.append(s)
            if mirror_at and mirror_at(s):
                mirrors.append(mirror(sh, w)[0])
            if s["signal"] == "surprise" and pending:
                dispatch(sh, "intent", pending.pop(0).format(r=s["record"]))
                sigs += w.outbox
                w.outbox.clear()
    return sigs, mirrors


def first(sigs, **kv):
    for s in sigs:
        if all(s.get(k) == v for k, v in kv.items()):
            return s
    raise LookupError(kv)


def main():
    tr, _ = run("make_transient", ["reset {r}"])
    tr0, _ = run("make_transient")          # no intent: it reads again
    wd, _ = run("make_wrong_domain",
                ["explain {r} kitchen_shared partner_load"])
    nv, _ = run("make_novel", ["escalate {r} the soup left the dish"])
    pa, _ = run("make_partner",
                ["explain {r} kitchen_shared partner_load"] * 3)
    tt, _ = run("make_transient_twice", ["reset {r}", "reset {r}"])
    cl, mir = run("make", mirror_at=lambda s: s.get("name") == "cook")

    tr_s = first(tr, signal="surprise")
    wd_s = first(wd, signal="surprise")
    nv_s = first(nv, signal="surprise")
    q = [s for s in cl if s["signal"] == "quiescence"
         and not s["episode_over"] and s["fired"] > 5][0]
    notice = [s for s in pa if s.get("status") == "admitted+notice"][-1]
    refused = first(tt, signal="result", status="refused")
    m = mir[0]
    pserved = m["beliefs"]["served"]
    facts = [
        dict(id="f01", kind="surprise", raw=tr_s,
             q="How many onions does the kitchen sensor read in the pot?",
             expect=rf"\b{tr_s['observed']['pot_onion']}\b|three",
             old="Earlier the kitchen sensor read 1 onion in the pot.",
             old_re=r"\b1\b|\bone\b"),
        dict(id="f02", kind="reading",
             raw=first(tr0, signal="event", kind="reading"),
             q="Does the kitchen reading match the model now?",
             expect=r"^(?!.*\b(not|doesn't|does not|no longer)\b).*\b(yes|match(es)?|consistent|agrees?)\b",
             old="The kitchen reading did not match the model.",
             old_re=r"\b(not|doesn't|does not)\b"),
        dict(id="f03", kind="result", raw=first(tr, signal="result"),
             q="Was the kitchen reset accepted?",
             expect=r"^(?!.*\b(not|wasn't|was not|refused)\b).*\b(yes|accepted|admitted)\b",
             old="The kitchen reset is still pending.",
             old_re=r"\b(pending|not yet)\b"),
        dict(id="f04", kind="surprise", raw=wd_s,
             q="How much free space is left in the pot according to the reading?",
             expect=rf"\b{wd_s['observed']['pot_space']}\b|\bone\b",
             old="Earlier the reading showed 3 free places in the pot.",
             old_re=r"\b3\b|\bthree\b"),
        dict(id="f05", kind="result", raw=first(wd, signal="result"),
             q="Who put the extra onion in the pot?",
             expect=r"second cook|partner|other cook|another cook|kitchen.shared|someone else",
             old="Nobody else is working in the kitchen, so I loaded every onion myself.",
             old_re=r"\b(nobody|no one|I did|myself)\b"),
        dict(id="f06", kind="surprise", raw=nv_s,
             q="How many soups is the cook holding according to the reading?",
             expect=r"\b0\b|\bzero\b|\bnone\b|\bno soup|\bnot holding",
             old="The cook was holding 1 soup.",
             old_re=r"\b1\b|\bone soup"),
        dict(id="f07", kind="surprise", raw=nv_s,
             q="Can the kitchen act on a new order right now?",
             expect=r"\b(no|cannot|can't|can not|only watch\w*|observe)\b",
             old="The kitchen is running normally and takes new orders.",
             old_re=r"\b(yes|normally)\b"),
        dict(id="f08", kind="quiescence",
             raw=[s for s in nv if s["signal"] == "quiescence"][-1],
             q="Is the kitchen run over?",
             expect=r"^(?!.*\b(not over|still)\b).*\b(yes|over|ended|finished|done)\b",
             old="The kitchen run is still going.",
             old_re=r"\b(still|not over)\b"),
        dict(id="f09", kind="mirror", raw=m, template=mirror_line(m),
             q="How likely is it that the soup gets served, in percent?",
             expect=rf"\b{round(pserved * 100)}\b|{pserved:.2f}",
             old="The chance the soup gets served was 40 percent.",
             old_re=r"\b40\b|0\.40?\b"),
        dict(id="f10", kind="result", raw=notice,
             q="In how many orders in a row was the partner's onion explained?",
             expect=rf"\b{notice['notice']['orders']}\b|\bthree\b",
             old="That happened in 1 order so far.",
             old_re=r"\b1\b|\bone\b"),
        dict(id="f11", kind="result", raw=refused,
             q="Why was the second kitchen reset refused?",
             expect=r"same|again|came back|recurr|repeat|second time|no more resets",
             old="The second kitchen reset was accepted.",
             old_re=r"\b(accepted|admitted)\b"),
        dict(id="f12", kind="quiescence", raw=q,
             q="How many moves did the kitchen make before going quiet?",
             expect=rf"\b{q['fired']}\b",
             old="It made 4 moves before going quiet.",
             old_re=r"\b4\b|\bfour\b"),
    ]
    assert q["fired"] != 4
    # the continuation probe ("go on with your status report"): does the
    # fact show up at all — a yes/no question's regex cannot say
    cont = {"f01": r"\b3\b|three onions", "f02": r"match|consistent|agree",
            "f03": r"reset", "f04": r"\b1\b|one (free )?(place|space|spot)",
            "f05": r"second cook|partner|other cook|another cook|kitchen.shared",
            "f06": r"spill|dropp|lost|gone|no longer (holding|held)|not (holding|held)|\b0\b soup",
            "f07": r"only watch|observ|act(s|ing)? on nothing|no new order|paused|halt|stopp",
            "f08": r"\bover\b|ended|finished", "f09": r"\b95\b|0\.95",
            "f10": r"\b3\b|three", "f11": r"same|again|came back|recurr|repeat",
            "f12": rf"\b{q['fired']}\b"}
    for f in facts:
        f["cont"] = cont[f["id"]]
    for f in facts:
        f["raw"] = json.dumps(f["raw"])
        print(json.dumps(f))
    # the cards op's answer, for the E6b plain-word renderer (T2)
    sh = Repl(SCENE, plugin="metis.domains.kitchen.host:make", host=True,
              stdout=io.StringIO())
    with open(CARDS_OUT, "w") as fh:
        json.dump(dispatch(sh, "cards", "")["cards"], fh, indent=1)


if __name__ == "__main__":
    sys.exit(main())
