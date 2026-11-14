"""The road's intent vocabulary: the task package's intent material, read as the service reads any domain's
(intent.yaml + its generated intent/<name>-prompt.txt and <name>.gbnf), and the J → typed-intent step of DESIGN.md
("{id, domain, verb, args}"). CHITCHAT and UNKNOWN are the sidecar's own: they never cross the gateway."""
import json
import os
from dataclasses import dataclass, field
from pathlib import Path

import yaml

LOCAL = ("CHITCHAT", "UNKNOWN")      # answered here, never sent


def default_task_dir():
    """$ROAD_TASK_DIR, else metis-worlds/road/domains/task beside this repo (perotheia-packages/)."""
    if os.environ.get("ROAD_TASK_DIR"):
        return Path(os.environ["ROAD_TASK_DIR"])
    here = Path(__file__).resolve()
    for up in here.parents:
        cand = up / "metis-worlds" / "road" / "domains" / "task"
        if cand.is_dir():
            return cand
    raise FileNotFoundError("road task package not found: set ROAD_TASK_DIR")


@dataclass
class Vocab:
    dir: Path
    domain: str
    prompt: str
    grammar: str
    verbs: dict                     # verb → meaning
    slots: dict                     # verb → [slot names]
    values: dict                    # slot → {ENUM: atom value}
    examples: list = field(default_factory=list)

    @classmethod
    def load(cls, d=None):
        d = Path(d or default_task_dir())
        man = yaml.safe_load((d / "domain.yaml").read_text()) or {}
        spec = yaml.safe_load((d / (man.get("intent") or {}).get("spec", "intent.yaml")).read_text())
        name = d.name
        verbs, slots = {}, {}
        for v in spec["verbs"]:
            for vb in (v["verb"] if isinstance(v["verb"], list) else [v["verb"]]):
                verbs[vb] = v["meaning"]
                slots[vb] = list(v.get("slots") or [])
        ex = [e for e in spec.get("examples") or [] if isinstance(e, list)]
        return cls(d, name, (d / "intent" / (name + "-prompt.txt")).read_text(),
                   (d / "intent" / (name + ".gbnf")).read_text(), verbs, slots, spec.get("values") or {}, ex)

    def parse(self, j):
        """The intent pass's JSON text → (verb, args); None when it is not one of ours."""
        try:
            o = json.loads(j) if isinstance(j, str) else dict(j)
        except (ValueError, TypeError):
            return None
        verb = o.pop("intent", None)
        if verb not in self.verbs:
            return None
        args = {k: v for k, v in o.items() if k in self.slots.get(verb, []) or k in self.values}
        return verb, args

    def typed(self, j, iid):
        """J → DESIGN.md's typed intent, or None (not ours, or a local verb)."""
        p = self.parse(j)
        if p is None or p[0] in LOCAL:
            return None
        return {"id": iid, "domain": self.domain, "verb": p[0], "args": p[1]}

    def words(self, verb, args):
        """The intent in the person's terms, for the outcome line (DESIGN: the outcome leads with the task's words)."""
        a = {k: str(v).lower() for k, v in (args or {}).items()}
        return {
            "PACE": "an %s pace" % a.get("pace", "other") if a.get("pace") == "easy" else "a %s pace" % a.get("pace", "other"),
            "LANE": "the %s lane" % a.get("side", "other"),
            "TURN": "%s at the next junction" % ("straight on" if a.get("way") == "straight" else a.get("way", "a way")),
            "STOP": "stopping", "GO": "driving on", "PASS": "passing the car ahead",
            "FOLLOW": "staying behind the car ahead", "RESUME": "driving as usual", "AROUND": "what is around",
        }.get(verb, verb.lower())
