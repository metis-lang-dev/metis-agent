"""The actor: act without a rule (world-minihack step 2a; LEARNING.md, "agent first, domain after").

A package whose program has no rule for the state it is in (a world before its domain is
extracted) can still act: its manifest says `actor: {max_steps: N, task: "…"}` (not under
`agent:`, which names an agent module: the loader's), and then, on every beat the program left quiet with nothing pending and the episode not over,
the model types ONE intent from the package's own card (intent.yaml: the prompt and the
grammar the intent pass generates), given

    the reading: the host's `describe()` of what it perceives when it has one (the world's words for
    its perception atoms), else the atoms; then the task line (the manifest's `task`) and the
    question "What one action do you take now, from where you stand?"; after the first step, the
    last action and whether the world took it

— never the world's own text or screen. The intent goes through the mapper as a person's would
(DomainAgent.intent); the host asserts its command on the next tick; no rule takes it, and the
domain agent hands it to the host's `act(atom)` instead of failing it ("no rule took the
command"): the world steps; the next reading is its answer. A host without `act` is not acted
for (the command fails as before). Nothing here knows a world.

The journal (one JSONL per episode; DomainAgent's records sink when there is one, the file
`agent-<session>.jsonl` beside the surprise records): per step
    {kind: "step", step, beat, reading (the atoms), text (what the model was given), events
     (the host's last-beat events), j (the intent the model typed), words (its spoken line),
     command (the mapped atom, or the mapper's error), result (what act() answered), ms}
and per episode one {kind: "episode", domain, session, steps, end (won | done | cap |
error), won, refusals (steps the world did not take), intents (the verbs' counts), cost: 0}.
"""
import json
import time
from collections import Counter
from pathlib import Path


class Actor:
    def __init__(self, agent, llm, cfg, card_dir, journal=None):
        from metis import intents
        self.agent, self.llm = agent, llm
        spec = intents.load_from(Path(card_dir))
        self.persona = intents.prompt(spec) + intents.spoken(spec)
        self.gbnf = intents.grammar(spec)
        self.max_steps = int(cfg.get("max_steps", 40))
        self.task = str(cfg.get("task") or "")
        self.journal = Path(journal) if journal else None
        self.rows, self.steps, self.pending, self.ended = [], 0, None, None
        self.intents = Counter()
        self.refusals = 0
        if hasattr(llm, "prime"):
            llm.prime(self.persona)

    def _inner(self):
        return getattr(self.agent.host, "inner", self.agent.host)

    def reading(self):
        """(the atoms, the text the model is given)."""
        inner = self._inner()
        atoms = sorted(a for a, n in (inner.reading() if hasattr(inner, "reading") else self.agent.repl.counts).items() if n > 0)
        text = inner.describe() if hasattr(inner, "describe") else "You perceive: " + ", ".join(atoms) + "."
        # the reading first, then the task and the question (a person's card maps an utterance: with the task
        # first, E2B typed the task's own verb every step — DOWN for "go down the stairs" — whatever it saw)
        last = self.rows[-1] if self.rows and self.rows[-1].get("kind") == "step" else None
        if last is not None:                       # the world's answer to the last action, said (a repeated message
            j = last.get("j") or {}                # is no new event: without this the model repeats a refused one)
            said = " ".join([str(j.get("intent", "?"))] + [str(v) for k, v in j.items() if k != "intent"])
            took = (last.get("result") or {}).get("acted", True)
            text += "\nYour last action: %s — %s." % (said, "done" if took else "the world did not take it: nothing changed")
        return atoms, text + ("\nYour task: %s." % self.task if self.task else "") + "\n" + self.QUESTION

    QUESTION = "What one action do you take now, from where you stand?"

    def _write(self, rec):
        self.rows.append(rec)
        if self.journal:
            self.journal.parent.mkdir(parents=True, exist_ok=True)
            with open(self.journal, "a") as f:            # appended and closed per row: a crash keeps the episode so far
                f.write(json.dumps(rec, default=str) + "\n")

    # ── the domain agent's calls ──
    def acted(self, atom):
        """The command no rule took: the host's act(atom), its answer journaled with the step that
        typed it. None when the host has no act face (the command fails as before)."""
        inner = self._inner()
        if not hasattr(inner, "act"):
            return None
        try:
            res = dict(inner.act(atom) or {})
        except Exception as e:                        # noqa: BLE001 — the world's error is the step's result
            res = {"acted": False, "error": "%s: %s" % (type(e).__name__, e)}
        if self.pending is not None:
            self.pending["result"] = res
            if not res.get("acted", True):
                self.refusals += 1
        return res

    def next(self):
        """After a quiet beat: journal the step that just finished; end the episode on the world's
        end or the cap; else type the next intent. → the domain agent's replies."""
        out = []
        if self.pending is not None:
            self._write(self.pending)
            self.pending = None
        if self.ended:
            return out
        inner = self._inner()
        if self.agent.over or getattr(inner, "done", False):
            return self.end("won" if getattr(inner, "won", False) else "done")
        if self.steps >= self.max_steps:
            self.agent.over = True
            return self.end("cap")
        atoms, text = self.reading()
        t0 = time.perf_counter()
        try:
            res = self.llm.two_pass(self.persona, text, self.gbnf)
            j = json.loads(res.get("json") or "{}")
        except Exception as e:                        # noqa: BLE001 — a model or grammar failure ends the episode, journaled
            self._write({"kind": "step", "step": self.steps + 1, "error": "%s: %s" % (type(e).__name__, e), "reading": atoms})
            return self.end("error")
        self.steps += 1
        verb = str(j.get("intent") or "UNKNOWN")
        self.intents[verb] += 1
        args = {k: v for k, v in j.items() if k != "intent"}
        iid = "act-%d" % self.steps
        replies = self.agent.intent({"id": iid, "domain": self.agent.name, "verb": verb, "args": args})
        m = self.agent.log[-1] if self.agent.log else {}
        self.pending = {"kind": "step", "step": self.steps, "beat": self.agent.beat_no(), "reading": atoms, "text": text,
                        "events": list(getattr(inner, "events", []) or []), "j": j, "words": res.get("voice", ""),
                        "command": m.get("atom") or {"error": m.get("error")}, "result": None,
                        "ms": round((time.perf_counter() - t0) * 1e3)}
        if "error" in m:                              # the mapper refused it: nothing will act, the step ends now
            self.pending["result"] = {"acted": False, "error": m["error"]}
            self.refusals += 1
        return out + [r for r in replies if r.get("kind") == "error"]

    def end(self, how):
        self.ended = how
        inner = self._inner()
        rec = {"kind": "episode", "domain": self.agent.name, "session": self.agent.session, "steps": self.steps, "end": how,
               "won": bool(getattr(inner, "won", False)), "refusals": self.refusals, "intents": dict(self.intents), "cost": 0}
        if hasattr(inner, "ports"):
            try:
                rec["world"] = inner.ports()
            except Exception:                         # noqa: BLE001
                pass
        self._write(rec)
        return []
