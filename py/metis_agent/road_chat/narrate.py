"""Narration of a window of the drive (accept item 3): the fired rows of k0..k1, each rule's %% line from the catalog
bound to its firing (the domain's own words, DESIGN: the narrator carries the bindings), collapsed into the changes,
then said in one to three sentences — by the model held to those lines (a number it did not get, and the template
speaks instead), or by the template alone.

    docs = load_docs(task_dir)              {member: {rule: line}} from the road family + the task package
    lines = window_lines(rows, docs, k0, hz)   [(seconds from k0, member, bound line)]
    Narrator(llm).say(lines) → (text, source: model|template)"""
import os
import re
from pathlib import Path

from .gateway import bind

MEMBERS = {"planner": "planner/planner.llp", "responder": "responder/responder.llp", "signals": "signals_domain.llp"}
MAX_LINES = 8
SYSTEM = """You narrate a self-driving car's drive to the person testing it. You speak as the car, in the first person.
You get the car's own rule lines for a stretch of the drive, in the order they fired. Each line is a reason, a colon, then what the car did.
Write one to three short sentences that tell, in that order, what the car did AND why: keep every line's reason and its action, in plain words.
Rules: never add a fact, a reason or a number the lines do not carry; no lists, no headings, no quotes.

Example.
Lines:
1. (the planner) At what I want: I keep my speed.
2. (the planner) A walker is at the crossing ahead: I stop before it.
3. (the planner) Below what I want, nothing slow ahead, room before the lead: I speed up.
Narration: I was keeping my speed when a walker came to the crossing ahead, so I stopped before it; once the way was clear and nothing slow was ahead, I sped up again."""


def default_family_dir():
    if os.environ.get("ROAD_FAMILY_DIR"):
        return Path(os.environ["ROAD_FAMILY_DIR"])
    for up in Path(__file__).resolve().parents:
        cand = up / "metis-catalog" / "domains" / "road"
        if cand.is_dir():
            return cand
    raise FileNotFoundError("the road family (metis-catalog domains/road) not found: set ROAD_FAMILY_DIR")


def load_docs(task_dir, family_dir=None):
    from metis.domain.package import rule_docs
    fam = Path(family_dir or default_family_dir())
    os.environ.setdefault("METIS_HOME", str(fam))
    docs = {"task": rule_docs(Path(task_dir) / "task.llp")}
    for m, rel in MEMBERS.items():
        p = fam / rel
        if p.exists():
            docs[m] = rule_docs(p)
    return docs


def window_lines(rows, docs, k0=None, hz=10):
    """The window's changes: a run of the same (member, rule, binding) is one line at its first k; a rule with no %%
    line is said by its name."""
    rows = sorted(rows, key=lambda r: r["k"])
    if not rows:
        return []
    k0 = rows[0]["k"] if k0 is None else k0
    out, last = [], {}
    for r in rows:
        key = (r["rule"], tuple(sorted((r.get("binding") or {}).items())))
        if last.get(r["member"]) == key:
            continue
        last[r["member"]] = key
        doc = docs.get(r["member"], {}).get(r["rule"]) or r["rule"].replace("_", " ")
        out.append((round((r["k"] - k0) / hz, 1), r["member"], bind(doc, r.get("binding"))))
    if len(out) > MAX_LINES:                      # the first and the last kept: what it began with, how it ended
        out = out[:MAX_LINES // 2] + out[-(MAX_LINES - MAX_LINES // 2):]
    return out


def template(lines):
    if not lines:
        return "Nothing fired in that stretch: I drove on as before."
    said = []
    for i, (_, _, w) in enumerate(lines):
        w = w.rstrip(".")
        said.append(w if i == 0 else ("Then " + w[0].lower() + w[1:] if not w.startswith("I ") else "Then " + w))
    return ". ".join(said) + "."


def numbers(s):
    return set(re.findall(r"\d+(?:\.\d+)?", s))


STOP = set("a an the and or but of to in on at by for with from as is are was were be been it its i me my we our us "
           "so then when once there this that these those not no nothing what want".split())


def stems(s):
    """content words, cut to five letters (stop/stopped, speed/sped are still apart: two forms each are listed)"""
    return {w[:5] for w in re.findall(r"[a-z]+", s.lower()) if w not in STOP and len(w) > 2}


ALIAS = {"sped": "speed", "stopp": "stop", "braki": "brake", "kept": "keep", "keepi": "keep", "took": "take", "takin": "take",
         "drove": "drive", "drivi": "drive", "gave": "give", "chang": "chang", "passe": "pass", "passi": "pass",
         "yield": "yield", "slowe": "slow", "eased": "ease", "easin": "ease", "saw": "see", "follo": "follo"}


def uncovered(lines, text):
    """The lines the text does not say: each line's reason and its action must each leave a content word in it."""
    got = stems(text) | {ALIAS.get(w, w) for w in stems(text)}
    miss = []
    for _, _, w in lines:
        reason, _, action = w.rpartition(":")
        for part in (reason, action):
            want = {ALIAS.get(x, x) for x in stems(part)}
            if want and not (want & got):
                miss.append(w)
                break
    return miss


class Narrator:
    def __init__(self, llm=None, max_tokens=200):
        self.llm, self.max_tokens = llm, max_tokens

    def user(self, lines):
        return "Lines:\n" + "\n".join("%d. (the %s) %s" % (i + 1, m, w) for i, (t, m, w) in enumerate(lines)) + "\nNarration:"

    def say(self, lines):
        if not lines or self.llm is None:
            return template(lines), "template"
        u = self.user(lines)
        try:
            text = generate(self.llm, SYSTEM, u, None, self.max_tokens, stop="\n")
        except Exception as e:
            print("[narrate] model failed: %r" % e)
            return template(lines), "template"
        text = " ".join(text.split())
        if text.lower().startswith("narration:"):
            text = text[len("narration:"):].strip()
        if not text or numbers(text) - numbers(u) or len(text) > 900 or not text.endswith((".", "!")) or uncovered(lines, text):
            return template(lines), "template"
        return text, "model"


def generate(llm, system, user, gbnf, max_tokens, stop=""):
    """stop: characters, any of which ends it. One turn on a context: the system prefix kept in the KV, the user turn after it, one generation (greedy)."""
    llm.begin_dialog(system)
    llm.decode(llm.after(llm.tail(user)))
    text, *_ = llm.gen(gbnf or "", False, max_tokens, stop)
    return llm.clean(text)
