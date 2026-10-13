"""The replier (DESIGN.md v3, "The domain agent"): a runtime signal or an
intent's outcome → the generic reply. No model: a table over the fired
events.

Binding an outcome to the intent it answers, from the runtime alone:

- the event that consumed a command atom answers that command's intent:
  an outcome token of the command's own act (done(go) for cmd_go) is its
  result;
- `progress(ACT)` opens a task: later outcomes of ACT that no command
  carries (arrive's done(go), stop_follow's cancelled(follow)) answer the
  task's intent and close it;
- an event that answers no intent is an unasked `event` with the
  mirror's counts.

reason = the fired rule's %% line after its colon (the whole line if it
has none); text = the %% line verbatim (step 1: the template the narrator
later replaces); ref = {seq, beat, event}, seq the event's number as
`log` numbers it.
"""
from .reply import reply


def doc_reason(doc):
    head, sep, tail = doc.partition(": ")
    return (tail if sep else doc).strip().rstrip(".")


def atom_args(atom):
    """'done(go)' → ('done', 'go'); 'idle' → ('idle', '')."""
    pred, _, rest = atom.partition("(")
    return pred, rest.rstrip(")")


class Replier:
    def __init__(self, domain, docs, outcomes, commands, alternatives=None, mirror=()):
        """docs: rule → %% line; outcomes: outcome predicate → result
        status; commands: the card's command predicates; alternatives:
        f(rule, counts) → list, or None; mirror: the atoms the
        mirror line counts (the catalog's #query)."""
        self.domain, self.docs, self.outcomes = domain, docs, outcomes
        self.commands, self.alternatives, self.mirror = commands, alternatives, tuple(mirror)
        self.pending = {}    # command atom -> [(id, act)], FIFO
        self.tasks = {}      # act -> id of the intent whose task runs

    def submitted(self, iid, atom, act):
        self.pending.setdefault(atom, []).append((iid, act))

    def counts(self, counts):
        return {a: counts.get(a, 0) for a in self.mirror}

    def fired(self, ev, seq, beat, counts, text=None):
        """One fired event → its replies (a list, possibly empty); text =
        the narrator's sentence for it, else the %% line."""
        rule = ev.name.partition("[")[0]
        base = rule.rsplit("-", 1)[0] if rule.rsplit("-", 1)[-1].isdigit() else rule
        doc = self.docs.get(base, self.docs.get(rule, ""))
        said = text or doc
        ref = {"seq": seq, "beat": beat, "event": ev.name}
        cmd = None
        for a in ev.consume:
            if atom_args(a)[0] in self.commands and self.pending.get(a):
                cmd = self.pending[a].pop(0)
                if not self.pending[a]:
                    del self.pending[a]
                break
        out = []
        for a in sorted(ev.produce):
            pred, act = atom_args(a)
            status = self.outcomes.get(pred)
            if status is None:
                continue
            if cmd and act == cmd[1]:
                iid = cmd[0]
            elif act in self.tasks:
                iid = self.tasks[act]
            else:
                continue
            if status == "progress":
                self.tasks[act] = iid
            elif self.tasks.get(act) == iid:
                del self.tasks[act]
            f = {"id": iid, "status": status, "ref": ref, "text": said}
            if status in ("refused", "expired", "failed", "cancelled"):
                f["reason"] = doc_reason(doc)
            if status == "refused":
                f["alternatives"] = list(self.alternatives(base, counts)) if self.alternatives else []
            out.append(reply(self.domain, "result", **f))
        if not out:
            out.append(reply(self.domain, "event", ref=ref, text=said, counts=self.counts(counts)))
        return out

    def untaken(self, counts, beat):
        """Commands still in the state after the runtime went quiet: no
        rule took them (a catalog gap) → result failed, and the atom is
        reported so the host can retract it."""
        out, gone = [], []
        for atom, ids in list(self.pending.items()):
            if counts.get(atom, 0) > 0:
                for iid, _ in ids:
                    out.append(reply(self.domain, "result", id=iid, status="failed",
                                     reason="no rule took the command", ref={"beat": beat},
                                     text="No rule took the command."))
                gone.append(atom)
                del self.pending[atom]
        return out, gone
