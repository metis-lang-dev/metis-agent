"""metis_agent.director (alf-hierarchy step 3): the frames that turn a director's effects into the
agent's verbs. Parity with the world's driver: every forward of a recorded 20-game director run
(metis-worlds alfworld experiments/hier.py --director, 20/20 won) sends the goal the driver sent;
the detour and the escalation, which no game of the family produces, by hand."""
import json
from pathlib import Path

from metis_agent.director import Frames, director_facts, effects

RUN = json.loads((Path(__file__).parent / "data" / "director-run.json").read_text())
FACTS = director_facts(RUN["facts"])


def test_the_instance_facts_are_read():
    assert FACTS["planner"] == {"task"} and FACTS["specialist"] == {"nav", "access", "grasp", "place"}
    assert ("nav", "goto") in FACTS["handles"] and ("task", "reached") in FACTS["handles"]
    assert FACTS["detour"] == {"elsewhere": "goto"} and FACTS["escalate"] == {"elsewhere": "refused"}
    text = "\n".join(f + "." for f in RUN["facts"])            # the same from an instance's text
    assert director_facts(text) == FACTS


def test_every_forward_of_the_recorded_run_sends_the_drivers_goal():
    n = 0
    for g in RUN["games"]:
        f = Frames(FACTS)
        goals = {a["k"]: (a["spec"], a["goal"]) for a in g["goals"]}
        for b in g["beats"]:
            line = f.arrive(b["arrival"])
            assert line == ("+%s" % b["arrival"] if b["arrival"].startswith("idle(") else "+seen(%s)" % b["arrival"].split("(")[0])
            for verb, args in effects(b["effects"]):
                if verb == "forward":
                    d, k = args
                    assert goals[b["k"]] == (d, f.goal(d, k)), (g["game"], b)
                    n += 1
    assert n == sum(len(g["goals"]) for g in RUN["games"]) == 260       # 13 a game: six specialists, seven task beats


def test_the_detour_and_the_escalation_rename_from_the_facts():
    f = Frames(FACTS)
    f.arrive("open(fridge1)")                                            # a person's goal from elsewhere
    assert f.goal("access", "open") == "open(fridge1)"
    f.arrive("elsewhere(fridge1)")                                       # access: not there
    assert f.goal("nav", "goto") == "goto(fridge1)"                      # detour(elsewhere, goto)
    f.arrive("reached(fridge1)")
    assert f.goal("access", "open") == "open(fridge1)"                   # resume: the kept goal
    f.arrive("elsewhere(fridge1)")                                       # a second time while rerouted
    assert f.goal("task", "refused") == "refused(fridge1)"               # escalate(elsewhere, refused)
    assert effects(["wake(nav)", "seen(goto)", "forward(nav,goto)", "ended(done)"]) == [
        ("wake", ("nav",)), ("forward", ("nav", "goto")), ("ended", ("done",))]


def test_the_loop_replays_the_recorded_run():
    """DirectorLoop over fakes of the agent: the director's beat answers with the recorded effects
    of that arrival; a forward re-instances the domain, which then produces the recorded results of
    that activation. The loop queues the same arrivals in the same order, sends the same goals and
    ends where the run ended (the director's end line spoken)."""
    from metis_agent.director import DirectorLoop
    kinds = sorted({b["arrival"].split("(")[0] for g in RUN["games"] for b in g["beats"]} - {"idle"})
    for g in RUN["games"]:
        beats, acts, verbs = list(g["beats"]), list(g["goals"]), []

        class Ops:
            def beat(self, name, lines):
                b = beats.pop(0)
                end = any(e.startswith("ended(") for e in b["effects"])
                return [{"fired": [{"kind": "clause", "produce": b["effects"], "line": "The task is done." if end else None}]}]

            def instance(self, d, goal):
                a = acts.pop(0)
                verbs.append(("instance", d, goal))
                loop.produced(d, {"fired": [{"kind": "clause", "produce": a["result"]}]})   # the domain's run

            def activate(self, d):
                verbs.append(("activate", d))

            def deactivate(self, d):
                verbs.append(("deactivate", d))

            def say(self, text):
                verbs.append(("say", text))
        loop = DirectorLoop("director", FACTS, kinds, Ops())
        loop.arrive(g["beats"][0]["arrival"])
        loop.step()
        assert [b["arrival"] for b in loop.log] == [b["arrival"] for b in g["beats"]], g["game"]
        assert [(d, goal) for b in loop.log for d, goal in b["goals"]] == [(a["spec"], a["goal"]) for a in g["goals"]]
        assert loop.outcome == "ended(done)" and g["outcome"] == "done" and verbs[-1] == ("say", "The task is done.")
        woke = [v[1] for v in verbs if v[0] == "activate"]
        assert woke == [a["spec"] for a in g["goals"] if a["spec"] != "task"]      # each specialist woken for its goal


def test_an_atom_put_in_from_outside_does_not_arrive():
    """The person's FETCH enters the attended task as an exo row: the task already has it; only what
    a rule produces arrives (else the director re-instances the task and its start runs twice)."""
    from metis_agent.director import DirectorLoop
    loop = DirectorLoop("director", FACTS, ["fetch", "goto"], None)
    loop.produced("task", {"fired": [{"kind": "exo", "produce": ["fetch(candle1,cabinet1)"]},
                                     {"kind": "clause", "produce": ["start", "goto(countertop1)"]}]})
    assert loop.queue == ["goto(countertop1)"]
