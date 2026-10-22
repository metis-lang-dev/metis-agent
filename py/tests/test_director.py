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
