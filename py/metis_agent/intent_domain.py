"""metis_agent.intent_domain — metispy's metis.domain.package (moved there, dagent-fixtures s-86bbd1e7
step 2): the generic domain built from the catalog's data. The agent's one addition: a host is
attached as the agent's own module, metis_agent.domains.<name>, unless the caller names another."""
from metis.domain.package import (_host_module, alternatives_of, bare, card, catalog_domains,  # noqa: F401
                                  handle_names, host_key, reports)
from metis.domain.package import build as _build
from metis.domain.agent_host import AgentHost  # noqa: F401


def build(name, display=None, voice="", root=None, module=None, fresh=False, dir=None, host=None):
    """metis.domain.package.build, the plugin's module defaulting to metis_agent.domains.<name>."""
    return _build(name, display=display, voice=voice, root=root, module=module or "metis_agent.domains." + name,
                  fresh=fresh, dir=dir, host=host)
