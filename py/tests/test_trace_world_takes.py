"""The composition replay feeds a world line's take (metis-plugin s-df4ff191, step 6's blocker): a page's world line (the road page's
run: of a new scenario, its watching) makes the live host's reading move at once, and the agent's sync after it is recorded as a seq
item {take, by: 'world'}. The replay must serve the line again on its domain and its trace host must answer that sync with the take;
before, the replay skipped world lines and every page run whose line moved a reading diverged ('why: want change, got clock')."""
import os

import pytest

from metis_agent import trace as TR


class Agent:
    def __init__(self, host):
        self.host = host
        self.synced = []


class Live:
    """The stand-in: serve_line(world) does what the domain agent does with a world line: host.world(lines), then its sync."""
    def __init__(self, hosts):
        self.agents = type("A", (), {})()
        self.agents.agents = {d: Agent(h) for d, h in hosts.items()}
        self.served = []

    def serve_line(self, msg, kind):
        self.served.append((kind, msg))
        ag = self.agents.agents[msg["domain"]]
        ag.host.world(msg["world"])
        ag.synced.append(ag.host.sync({}))
        return []


def test_a_world_line_is_served_again_and_its_take_answers_the_sync():
    feed = TR.Feed.__new__(TR.Feed)
    feed.copies, feed.beats, feed.whys, feed.delivered = {}, {"responder": [["+beat_line"]]}, {"responder": "change"}, {}
    host = TR.TraceHost("responder", feed)
    lv = Live({"responder": host})
    rec = {"k": 176, "members": {"responder": {"why": "change", "seq": [{"take": ["+safe_lat(a1)", "-role_empty(a1)"], "by": "world"},
                                                                        {"beat": [], "fired": []}]}}}
    TR._external(lv, {"line": {"world": ["run:card:StaticCutIn@Town03#42:1"], "domain": "responder"}, "kind": "world"}, rec)
    assert lv.served == [("world", {"world": ["run:card:StaticCutIn@Town03#42:1"], "domain": "responder"})]
    assert lv.agents.agents["responder"].synced == [["+safe_lat(a1)", "-role_empty(a1)"]]    # the world's take, not the beat's lines
    assert host.sync({}) == ["+beat_line"]                                                   # the beat's own sync is untouched


def test_a_world_line_with_no_take_takes_nothing():
    """'watching' on most ticks: the live sync after it found nothing new; the replay's answers nothing too."""
    feed = TR.Feed.__new__(TR.Feed)
    feed.copies, feed.beats, feed.whys, feed.delivered = {}, {}, {}, {}
    host = TR.TraceHost("responder", feed)
    lv = Live({"responder": host})
    TR._external(lv, {"line": {"world": ["watching"], "domain": "responder"}, "kind": "world"}, {"k": 5, "members": {"responder": {"why": "clock"}}})
    assert lv.agents.agents["responder"].synced == [[]]


def test_a_world_line_to_a_live_host_is_not_served():
    """A domain whose host is not a trace host (the replay keeps a real one) is left alone: its world is not the record's."""
    lv = Live({"responder": object()})
    TR._external(lv, {"line": {"world": ["watching"], "domain": "responder"}, "kind": "world"}, {"k": 1, "members": {}})
    assert lv.served == []


KEPT = "/bigdata/work/bond/plugin/e2e/metis-worlds/road/.metis/traces/Town03-1-20261116T133826-20261116T141005-23999d"


@pytest.mark.skipif(not os.path.isdir(KEPT), reason="the step-6 page trace (bond's e2e clone) is not on this host")
def test_the_page_traces_window_replays_across_a_checkpoint(monkeypatch):
    """bond's repro: the kept window k336..469 of the StaticCutIn page run (two segments, the k400 checkpoint between) diverged at k206
    (responder why: want change, got clock); with the world takes fed it replays whole."""
    import yaml
    root = KEPT.split("/.metis/")[0]
    monkeypatch.setenv("METIS_CATALOG", root)
    monkeypatch.setenv("METIS_HOME", "/bigdata/work/bond/plugin/e2e/metis-catalog/domains/road")
    monkeypatch.chdir(root)
    r = TR.replay_composition(KEPT, yaml.safe_load(open(KEPT + "/config.yaml")))
    assert r.ok, r.divergence
    assert r.checkpoints >= 1 and r.ticks >= 300
