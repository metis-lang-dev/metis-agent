"""The emulated domain agents (orchestrator-agent step 3), a port of
go/internal/domemu: scripted domains answering typed intents and REPL calls
with generic replies — several domains at once, with ids, delays, unasked
signals and the dog's one task slot. The py orchestrator's timeline replay
(E13 step 3's 14 timelines) runs against it, as llmbench -dialog does.

A script per domain:
  intents  rules: a verb and args (a subset, dotted paths) → the replies at
           beat offsets; "accepted" is said at once; the first unused
           matching rule wins (used once unless "repeat"); a reply's
           "of": "inflight" addresses the domain's other task in flight.
  tools    the REPL API's answers, final: a tool (and args subset) → its
           reply, and replies after it ("then": an explain's notice).
  signals  unasked replies at absolute beats.
  slot     a "task" rule holds the slot until its final outcome; while held
           a "task" rule is refused with busy_task, a "body" rule with
           busy_body; other rules need no slot.
"""
import copy
import json

FINAL = ("done", "refused", "failed", "expired", "cancelled")


def _sprint(v):
    """fmt.Sprint of a value json.Unmarshal put in an any."""
    if v is None:
        return "<nil>"
    if v is True or v is False:
        return "true" if v else "false"
    if isinstance(v, (int, float)):
        from .orchestrator import gofloat
        return gofloat(v)
    if isinstance(v, dict):
        return "map[" + " ".join("%s:%s" % (k, _sprint(v[k])) for k in sorted(v)) + "]"
    if isinstance(v, list):
        return "[" + " ".join(_sprint(x) for x in v) + "]"
    return str(v)


def subset(when, args):
    for k, want in (when or {}).items():
        v = args
        for p in k.split("."):
            if not isinstance(v, dict):
                return False
            v = v.get(p)
        if _sprint(v) != _sprint(want):
            return False
    return True


def final(r):
    return r.get("kind") == "result" and r.get("status") in FINAL


def with_domain(r, dom):
    out = dict(r or {})
    out.setdefault("domain", dom)
    return out


class Emu:
    def __init__(self, scripts):
        scripts = copy.deepcopy(list(scripts or []))     # the rules are used up as they fire
        self.doms, self.beat, self.seq, self.due = {}, 0, 0, []
        self.inflight, self.finished = {}, set()
        for s in scripts:
            for r in s.get("intents") or []:
                r["_used"] = False
            for r in s.get("tools") or []:
                r["_used"] = False
            self.doms[s["domain"]] = s
            for sg in s.get("signals") or []:
                self.push(sg.get("at", 0), with_domain(sg.get("reply"), s["domain"]))

    def push(self, at, r):
        self.seq += 1
        self.due.append((at, self.seq, r))

    def pending(self):
        return len(self.due)

    def intent(self, iid, domain, verb, args):
        s = self.doms.get(domain)
        if s is None:
            return {"domain": domain, "kind": "error", "id": iid, "error": "no such domain"}
        r = match(s.get("intents") or [], verb, "", args, False)
        if r is None:
            return {"domain": domain, "kind": "error", "id": iid, "error": "no row for %s" % verb,
                    "text": "I can't map that to anything I can do."}
        others = list(self.inflight.get(domain, []))
        busy = {"task": s.get("busy_task"), "body": s.get("busy_body")}.get(r.get("slot", ""))
        if others and busy is not None:
            rep = with_domain(busy, domain)
            rep["id"] = iid
            self.push(self.beat, rep)
            return {"domain": domain, "kind": "result", "id": iid, "status": "accepted"}
        match(s.get("intents") or [], verb, "", args, True)
        if r.get("slot") == "task":
            self.inflight.setdefault(domain, []).append(iid)
        for t in r.get("replies") or []:
            ids = others if t.get("of") == "inflight" else [iid]
            for x in ids:
                rep = with_domain(t.get("reply"), domain)
                rep["id"] = x
                self.push(self.beat + t.get("after", 0), rep)
                if t.get("after", 0) == 0 and final(rep):
                    self.free(domain, x)
        return {"domain": domain, "kind": "result", "id": iid, "status": "accepted"}

    def tool(self, domain, tool, args, iid=""):
        s = self.doms.get(domain)
        if s is None:
            return {"domain": domain, "kind": "error", "error": "no such domain"}
        r = match(s.get("tools") or [], "", tool, args, True)
        if r is None:
            return {"domain": domain, "kind": "error", "error": "no answer scripted for " + tool,
                    "text": "The " + domain + " agent has no answer for that."}
        out = with_domain(r.get("reply"), domain)
        if iid and "id" not in out:
            out["id"] = iid
        for t in r.get("replies") or []:
            self.push(self.beat + t.get("after", 0), with_domain(t.get("reply"), domain))
        return out

    def step(self):
        self.beat += 1
        return self.take_due()

    def now(self):
        return self.take_due()

    def take_due(self):
        self.due.sort(key=lambda d: (d[0], d[1]))
        out, keep = [], []
        for d in self.due:
            if d[0] <= self.beat:
                if not self.finish(d[2]):
                    out.append(d[2])
                continue
            keep.append(d)
        self.due = keep
        return out

    def finish(self, r):
        if r.get("kind") != "result":
            return False
        k = "%s/%s" % (r.get("domain") if isinstance(r.get("domain"), str) else "", r.get("id") if isinstance(r.get("id"), str) else "")
        if k in self.finished:
            return True
        if not final(r):
            return False
        self.finished.add(k)
        self.free(r.get("domain"), r.get("id"))
        return False

    def free(self, dom, iid):
        ids = self.inflight.get(dom, [])
        if iid in ids:
            ids.remove(iid)


def match(rules, verb, tool, args, use):
    for r in rules:
        if r.get("_used") or r.get("verb", "") != verb or r.get("tool", "") != tool or not subset(r.get("when"), args):
            continue
        if use and not r.get("repeat"):
            r["_used"] = True
        return r
    return None


def load_timelines(path):
    out = []
    with open(path) as fh:
        for ln in fh:
            ln = ln.strip()
            if ln and not ln.startswith("#"):
                out.append(json.loads(ln))
    return out
