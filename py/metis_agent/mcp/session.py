"""The session: the ONE workspace the server is rooted in (metispy's Workspace), how it was found, and its switch.
Resolution at start: --root, else $METIS_WORKSPACE, else the workspace Claude Code's cwd stands in (metispy's
find: .metis/ or a bundle, walking up), else none (the workspace tools say how to set one). No hard-coded default."""
import os
from pathlib import Path

from metis.catalog import workspace as W


class Session:
    def __init__(self):
        self.ws = None
        self.how = None                  # "--root" | "$METIS_WORKSPACE" | "cwd" | "workspace_open" | "workspace_create"
        self.taken = set()               # surprises this session took (released at exit unless ruled)
        self.last_member = ""            # the member of the surprise taken last (rule_text's default)
        self.repl = None                 # the open REPL (tools/repl.py)
        self.domain = None               # the open domain agent (tools/domain.py)
        self.on_switch = []              # callbacks after a switch (the server's list_changed)
        from .iface import Hub
        self.hub = Hub(self)             # the surprise interface: inbox, outbox, the runtime connection, the /metis-ui pairing

    def start(self, root=None):
        if root:
            return self.open(root, how="--root")
        env = os.environ.get("METIS_WORKSPACE")
        if env:
            return self.open(env, how="$METIS_WORKSPACE")
        ws = W.find(os.getcwd())
        if ws is not None:
            self.ws, self.how = ws, "cwd"
            os.environ["METIS_CATALOG"] = str(ws.root)  # as open(): metispy's catalog root for every verb run from here
        return self.ws

    HOME0 = os.environ.get("METIS_HOME")    # the family the server was started with (the default of every workspace)

    def open(self, path, how="workspace_open", home=None):
        """Root the session in the workspace at PATH (or the one PATH stands in): refused when there is none. HOME: the
        family's units for this workspace (METIS_HOME: a workspace may pin its own version of a family); none: the
        server's own."""
        p = Path(os.path.expanduser(str(path))).resolve()
        if not p.is_dir():
            raise ValueError("%s: no such directory" % p)
        ws = W.find(p) if not ((p / ".metis").is_dir() or (p / "domains").is_dir()) else W.Workspace(p)
        if ws is None or not ((ws.root / ".metis").is_dir() or ws._composition() is not None):
            raise ValueError("%s is not a Metis workspace (no .metis/ and no domains/composition.yaml in it or above): "
                             "workspace_create makes one" % p)
        if home:
            h = Path(os.path.expanduser(str(home))).resolve()
            if not h.is_dir():
                raise ValueError("home %s: no such directory (a family's units: METIS_HOME)" % h)
        self.release()
        self.ws, self.how = ws, how
        if home:
            os.environ["METIS_HOME"] = str(h)
        elif self.HOME0 is not None:
            os.environ["METIS_HOME"] = self.HOME0
        else:
            os.environ.pop("METIS_HOME", None)
        os.environ["METIS_CATALOG"] = str(ws.root)      # metispy's catalog root for every verb run from here
        self.hub.switched()
        for cb in self.on_switch:
            cb()
        return ws

    def need(self):
        if self.ws is None:
            raise ValueError("no workspace: start Claude Code in one, or /metis-workspace <path> (workspace_open)")
        return self.ws

    def release(self):
        """The takes this session holds and did not rule go back (the surprise reads open again)."""
        if self.ws is None:
            return
        for sid in list(self.taken):
            try:
                if self.ws.surprise(sid)["status"] == "taken":
                    self.ws.path("taken", id=sid).unlink(missing_ok=True)
                    self.ws.journal("claude", "release", id=sid)
            except Exception:  # noqa: BLE001
                pass
        self.taken.clear()
        try:                                     # the interface: the runtime connection and the /metis-ui endpoint are the workspace's
            self.hub.disconnect()
            from .tools.bridge import _B
            if "b" in _B:
                _B["b"].stop()
        except Exception:  # noqa: BLE001
            pass
        for attr in ("repl", "domain"):
            child = getattr(self, attr, None)
            if child is not None:
                try:
                    child.close()
                except Exception:  # noqa: BLE001
                    pass
                setattr(self, attr, None)


SESSION = Session()
