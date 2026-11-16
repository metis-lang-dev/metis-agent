"""metis_agent.runtime.host — the DOMAIN's face to the generic runtime (plugin/docs/RUNTIME-INTERFACE.md §3): ONE small
class per domain, handed to runtime.run.Run by the domain's scenario runner (or loaded by load('module:Class')). Every method has a default; a domain overrides what it needs. No server, no client, no wire here.

    class RoadHost(RuntimeHost): name = "road"; def findings(self, live, k): ...

What a RUN needs from the domain (the user: scenario running is separate from the runtime: how a scenario becomes a
Live config is the domain's scenario runner's, not this face's).

A finding is {kind, k, words, slug?, member?, k0?, k1?, ...}: k (and k0, k1) in the TRACE's ticks; its window is
[k0, k1] when given, else `window` around k (clamped to the run by the runtime)."""
import importlib
import os
import sys
from pathlib import Path


class NoModel:
    """The Live's model when a run needs none (the members are Metis rules; no LLM in a runtime's run)."""
    BOOK = ("begin_dialog", "inject")

    def sibling(self, n):
        return self

    def __getattr__(self, name):
        if name in self.BOOK:
            return lambda *a, **k: 0
        raise RuntimeError("the runtime's run has no model loaded (%s)" % name)


class RuntimeHost:
    name = "runtime"                  # the runtime's name (discovery file, `from: runtime:<name>`)
    domain = None                     # the bundle's domain, for hello
    window = (40, 10)                 # ticks before / after a finding's k: its kept window

    def prepare(self, cfg, run, trace_dir):
        """In the run's child, just before the Live is made: the run's name and trace dir are known now (road: the run
        spec file its world reads, naming the trace dir for its truth column). CFG may be changed in place."""

    def model(self):
        return NoModel()

    def start(self, live, cfg):
        """After the Live is made (road: its members' watch made blind, its world found)."""

    def findings(self, live, k):
        """Per tick: the domain's findings a kernel cannot see. → [finding]"""
        return []

    def done(self, live, k):
        """The run ended by itself."""
        return False

    def summary(self, live):
        """run.ended's summary."""
        return {}

    def end_findings(self, live, summary):
        """The run's own measures, after its last tick. → [finding]"""
        return []

    def close(self, live):
        """The world's processes shut."""


def load(ref, workspace=None):
    """'module:Class' → an instance; the workspace (and its runtime/ dir) first on the python path."""
    mod, _, cls = str(ref).partition(":")
    if not mod or not cls:
        raise ValueError("a host is module:Class, not %r" % ref)
    for p in ([Path(workspace) / "runtime", Path(workspace)] if workspace else []):
        if p.is_dir() and str(p) not in sys.path:
            sys.path.insert(0, str(p))
    for p in os.environ.get("METIS_RUNTIME_PATH", "").split(os.pathsep):
        if p and p not in sys.path:
            sys.path.insert(0, p)
    obj = getattr(importlib.import_module(mod), cls)
    return obj() if isinstance(obj, type) else obj
