"""The domain modules, from the catalog (domain-catalog s-2a2bc758 step 2):
no domain file lives in metis-agent. A domain's module is built from its
manifest, metis-catalog domains/<…>/domain.yaml (metis_catalog):

  agent: agent.py[:variant]   the catalog's domain-agent module, executed
                              as metis_agent.domains.<name> (the variant's
                              namespace applied: agent.variant(name))
  intent.spec (no agent)      the generic agent (intent_domain.build: the
                              card, outcomes and alternatives of intent.yaml,
                              the host's world)

NAME, DISPLAY, VOICE and CATALOG (the program) come from the manifest unless
the module sets them. Each module is registered in sys.modules under
metis_agent.domains.<name>, so a plugin spec naming it (the kernel attaches
"metis_agent.domains.dog:make") and `from metis_agent.domains import dog`
both find it.

    from metis_agent import domains
    domains.load("dog")                 # the module (cached; fresh=True re-reads the files: reload)
"""
import importlib.abc
import importlib.util
import sys
from pathlib import Path

_SIBLING = Path(__file__).resolve().parents[4] / "metis-catalog"   # perotheia-packages/metis-catalog


def catalog():
    """metis_catalog, installed or from the sibling checkout."""
    try:
        import metis_catalog
    except ImportError:
        sys.path.insert(0, str(_SIBLING))
        import metis_catalog
    return metis_catalog


class _Loader(importlib.abc.Loader):
    """Builds metis_agent.domains.<name> from the domain's manifest."""

    def __init__(self, name, fresh=False):
        self.name, self.fresh = name, fresh

    def create_module(self, spec):
        return None

    def exec_module(self, mod):
        d = catalog().get(self.name)
        agent = d.agent
        mod.__dict__.update(NAME=d.name, DISPLAY=d.display, VOICE=d.manifest.get("voice", ""), CATALOG=d.program)
        if agent is not None:
            path, variant = agent
            mod.__file__ = str(path)
            exec(compile(Path(path).read_text(), str(path), "exec"), mod.__dict__)
            if variant:
                mod.__dict__.update(mod.variant(variant))
        elif d.intent.get("spec"):
            from ..intent_domain import build
            mod.__file__ = str(d.dir / "domain.yaml")
            mod.__dict__.update(build(d.name, display=d.display, voice=d.manifest.get("voice", ""), dir=d.dir,
                                      module=mod.__name__, fresh=self.fresh))
        else:
            raise ImportError("domain %r has neither an agent module nor an intent.spec (%s)" % (self.name, d.dir))
        mod.MANIFEST = d
        self.fresh = False


class _Finder(importlib.abc.MetaPathFinder):
    """metis_agent.domains.<name> for every domain the catalog has a manifest of."""

    def find_spec(self, fullname, path=None, target=None):
        head, _, name = fullname.rpartition(".")
        if head != __name__ or not name:
            return None
        try:
            d = catalog().get(name)
        except KeyError:
            return None
        return importlib.util.spec_from_loader(fullname, _Loader(name), origin=str(d.dir / "domain.yaml"))


if not any(isinstance(f, _Finder) for f in sys.meta_path):
    sys.meta_path.append(_Finder())


def load(name, fresh=False):
    """The domain's module; fresh: built again from the files on disk (a new
    module object, so a failed reload can put the old one back)."""
    key = "%s.%s" % (__name__, name)
    if not fresh:
        return importlib.import_module(key)
    sys.modules.pop(key, None)
    spec = importlib.util.spec_from_loader(key, _Loader(name, fresh=True), origin=None)
    mod = importlib.util.module_from_spec(spec)
    sys.modules[key] = mod
    try:
        spec.loader.exec_module(mod)
    except BaseException:
        sys.modules.pop(key, None)
        raise
    return mod


def __getattr__(name):
    if name.startswith("_"):
        raise AttributeError(name)
    try:
        return load(name)
    except (KeyError, ModuleNotFoundError) as e:
        raise AttributeError(str(e)) from None
