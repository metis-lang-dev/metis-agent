"""The world page's frames from the service (alf-demo step 2, metis-worlds alfworld/docs/DEMO.md §4–§5):
a director beat as world/hierarchy, the task card's chain and result lines as world/delta task, an ask's
cornered place, and every served host's own world/* frames (a host's frames() face), on a stand-in for
the service (the two methods read only these attributes)."""
from types import SimpleNamespace

from metis_agent import service as S
from metis_agent.director import director_facts

FACTS = director_facts(["planner(task).", "specialist(nav).", "handles(nav, goto).", "result(reached).", "end(wrong_dest)."])


def stand_in(outcome=None, hosts=None):
    frames, said = [], []
    director = SimpleNamespace(name="director", facts=FACTS, outcome=outcome)
    agents = {"director": SimpleNamespace(repl=SimpleNamespace(counts={"active(task)": 1, "inactive(nav)": 1, "gone": 0}))}
    for name, h in (hosts or {}).items():
        agents[name] = SimpleNamespace(host=SimpleNamespace(inner=h))
    lv = SimpleNamespace(director=director, agents=SimpleNamespace(agents=agents), task_view={"chain": [], "results": []},
                         emit=lambda ev, f: frames.append((ev, f)), on_say=lambda kind, line: said.append((kind, line)))
    lv.spoken = lambda text, domain=None: S.Live.spoken(lv, text, domain)
    lv.ask_now, lv.status = None, None
    lv.set_status = lambda state, text, **kw: S.Live.set_status(lv, state, text, **kw)
    lv.world_over = lambda: S.Live.world_over(lv)
    lv.goal_said = lambda goal, domain: S.Live.goal_said(lv, goal, domain)
    return lv, frames, said


def test_a_beat_is_world_hierarchy_and_the_chain_follows_the_results():
    lv, frames, said = stand_in()
    S.Live.world_beat(lv, 0, {"arrival": "goto(fridge1)", "effects": ["wake(nav)", "forward(nav,goto)"], "goals": [["nav", "goto(fridge1)"]]})
    S.Live.world_beat(lv, 1, {"arrival": "reached(fridge1)", "effects": ["forward(task,reached)"], "goals": [],
                              "from": "nav", "said": "I am at the fridge."})
    S.Live.world_beat(lv, 2, {"arrival": "idle(nav)", "effects": ["sleep(nav)"], "goals": [], "from": "nav"})
    hier = [f for ev, f in frames if ev == "world/hierarchy"]
    assert [h["beat"] for h in hier] == [0, 1, 2] and hier[0]["state"] == ["active(task)", "inactive(nav)"]
    views = [f["task"] for ev, f in frames if ev == "world/delta"]
    assert views[-1] == {"chain": [{"domain": "nav", "goal": "goto(fridge1)", "result": "reached(fridge1)"}],
                         "results": ["I am at the fridge."]}
    assert said == [("line", "I am at the fridge.")] and len(views) == 2          # the idle beat changes nothing


def test_an_ask_names_its_cornered_place():
    lv, frames, _ = stand_in(outcome="ended(wrong_dest)")
    S.Live.world_beat(lv, 0, {"arrival": "wrong_dest(fridge1)", "effects": ["ended(wrong_dest)", "ask(wrong_dest)"], "goals": []})
    assert [f.get("cornered") for ev, f in frames if ev == "world/delta"] == ["fridge1"]


def test_every_hosts_world_frames_are_sent_with_their_domain():
    class Host:
        def __init__(self):
            self.q = [{"ev": "world/delta", "agent": {"at": "fridge1"}}, {"ev": "not-world", "x": 1}]

        def frames(self):
            out, self.q = self.q, []
            return out
    lv, frames, _ = stand_in(hosts={"nav": Host(), "task": SimpleNamespace()})
    S.Live.world_frames(lv)
    S.Live.world_frames(lv)
    assert frames == [("world/delta", {"agent": {"at": "fridge1"}, "domain": "nav"})]


def test_a_planners_refusal_that_asks_is_a_result_line_said_once():
    lv, frames, said = stand_in(outcome="ended(wrong_dest)")
    S.Live.world_beat(lv, 0, {"arrival": "wrong_dest(fridge1)", "effects": ["ended(wrong_dest)", "ask(wrong_dest)"], "goals": [],
                              "from": "task", "said": "Fridge 1 is not where this task wants tomato 1.", "said_as_ask": True})
    view = [f for ev, f in frames if ev == "world/delta"][-1]
    assert view["cornered"] == "fridge1" and view["task"]["results"] == ["Fridge 1 is not where this task wants tomato 1."]


def test_a_held_result_is_voiced_when_the_director_took_nothing_from_it():
    """nina's N7: a director domain's result is held for the director's beats; voiced after all when
    they took no arrival from that domain (a refusal the director never answers is never silence)."""
    voiced = []
    lv, frames, said = stand_in()
    lv.director.log = [{"arrival": "goto(r)", "from": "task"}]
    lv.o = SimpleNamespace(handle=lambda inb, g, sw: voiced.append(inb.reply.domain))
    lv.gbnf, lv.stop_words = "", []
    lv.dir_held = [SimpleNamespace(reply=SimpleNamespace(domain="task")), SimpleNamespace(reply=SimpleNamespace(domain="nav"))]
    S.Live.release_held(lv, 0)
    assert voiced == ["nav"] and lv.dir_held == []
    assert said == []                                       # the director's say carries it, as the ask


def test_a_line_is_said_in_the_worlds_words():
    """nina's N3: the handles in a rule's line said as the world's names, by a host's speak() face."""
    lv, frames, said = stand_in(hosts={"nav": SimpleNamespace(speak=lambda t: t.replace("fridge1", "fridge 1"))})
    S.Live.world_beat(lv, 0, {"arrival": "reached(fridge1)", "effects": [], "goals": [], "from": "nav", "said": "I am at fridge1."})
    assert said == [("line", "I am at fridge 1.")]
    lv2, _, said2 = stand_in(hosts={"nav": SimpleNamespace(speak=lambda t: t.replace("fridge1", "fridge 1"))})
    S.Live.world_beat(lv2, 0, {"arrival": "reached(fridge1)", "effects": [], "goals": [], "from": "nav", "said": "fridge1 is here."})
    assert said2 == [("line", "Fridge 1 is here.")]                                  # nina's N6: capitalized


def test_an_asking_line_carries_its_answers():
    """alf-demo step 3b: `question | answer | answer` — the answers the agent takes, as the ask's options;
    none given, the stop words."""
    q, opts = S.split_options("Fridge 1 is not where this task wants tomato 1. Put it in microwave 1, or stop? | put tomato 1 in microwave 1 | stop")
    assert q.endswith("or stop?") and [o["say"] for o in opts] == ["put tomato 1 in microwave 1", "stop"]
    assert S.split_options("I stop and ask.") == ("I stop and ask.", [{"label": "stop", "say": "stop"}])


def test_the_status_follows_the_director_and_the_ask_is_a_frame():
    lv, frames, said = stand_in()
    S.Live.world_beat(lv, 0, {"arrival": "goto(fridge1)", "effects": ["wake(nav)", "forward(nav,goto)"], "goals": [["nav", "goto(fridge1)"]]})
    assert lv.status["state"] == "working" and lv.status["domain"] == "nav" and lv.status["text"] == "nav: Goto(fridge1)"
    lv.ask_now = {"question": "Fridge 1 is not where this task wants tomato 1.", "options": [{"label": "stop", "say": "stop"}]}
    S.Live.world_beat(lv, 1, {"arrival": "wrong_dest(fridge1)", "effects": ["ended(wrong_dest)", "ask(wrong_dest)"], "goals": [], "from": "task"})
    ask = [f for ev, f in frames if ev == "world/ask"][-1]
    assert ask["cornered"] == "fridge1" and ask["by"] == "task" and ask["options"][0]["say"] == "stop"
    assert lv.status["state"] == "asking" and lv.status["options"] == ask["options"]
    S.Live.world_beat(lv, 2, {"arrival": "placed", "effects": ["ended(placed)"], "goals": []})
    assert lv.status["state"] == "done" and [ev for ev, _ in frames].count("world/status") == 3


def test_a_won_world_is_over():
    lv, frames, _ = stand_in(hosts={"task": SimpleNamespace(won=True)})
    S.Live.world_beat(lv, 0, {"arrival": "placed", "effects": ["ended(placed)"], "goals": []})
    assert lv.status["state"] == "over" and "new game" in lv.status["text"]


def test_a_goal_is_said_in_the_worlds_words():
    """nina's 3b wire note: the working status says the goal as the host words it."""
    lv, _, _ = stand_in(hosts={"nav": SimpleNamespace(goal_text=lambda g: "going to fridge 1" if g == "goto(fridge1)" else None)})
    S.Live.world_beat(lv, 0, {"arrival": "goto(fridge1)", "effects": ["wake(nav)", "forward(nav,goto)"], "goals": [["nav", "goto(fridge1)"]]})
    assert lv.status["text"] == "Going to fridge 1" and lv.status["goal"] == "goto(fridge1)"
    S.Live.world_beat(lv, 1, {"arrival": "goto(cab1)", "effects": [], "goals": [["nav", "goto(cab1)"]]})
    assert lv.status["text"] == "nav: Goto(cab1)"                     # no words for it: the goal as it is


def test_a_specialists_result_hands_the_status_to_the_planner():
    """nina's W3: the working status does not stay on a specialist's goal once its result is in."""
    lv, _, _ = stand_in()
    S.Live.world_beat(lv, 0, {"arrival": "goto(fridge1)", "effects": ["wake(nav)", "forward(nav,goto)"], "goals": [["nav", "goto(fridge1)"]]})
    S.Live.world_beat(lv, 1, {"arrival": "reached(fridge1)", "effects": ["forward(task,reached)"], "goals": [], "from": "nav",
                              "said": "I am at the fridge."})
    assert lv.status["state"] == "working" and lv.status["text"] == "Choosing the next step" and lv.status["domain"] == "task"


def test_the_card_says_what_this_world_is_and_what_to_ask(tmp_path):
    """alf-demo 3b (the user's "put the pencil in microwave" → unknown, nothing to go on): one line from
    the map's card — its first about line, then two asks for example; nothing without a card."""
    import json
    lv, _, _ = stand_in()
    lv.world_map = str(tmp_path / "world-map.json")
    lv.card = lambda: S.Live.card(lv)
    asks = [{"say": "put a tomato in the microwave"}, {"say": "put the tomato in the microwave"},
            {"say": "put the apple on the countertop"}, {"say": "stop"}]
    (tmp_path / "world-map.json").write_text(json.dumps({"card": {"about": ["A kitchen.", "More."], "asks": asks}}))
    assert S.Live.card_line(lv) == 'A kitchen. Ask me, for example: "put a tomato in the microwave" or "put the apple on the countertop".'
    (tmp_path / "world-map.json").write_text(json.dumps({"task": {}}))
    assert S.Live.card_line(lv) == ""


def turn_stand_in(answer, won=False, card=""):
    """A stand-in for a person's turn: the model answers every utterance with `answer` (J, voice)."""
    lv, frames, said = stand_in(hosts={"task": SimpleNamespace(won=won)})
    lv.journal = S.Journal(emit=lambda ev, f, page=None: None)
    lv.o = SimpleNamespace(turn=lambda text, gbnf: answer)
    lv.gbnf, lv.stop_words, lv.held, lv.narr_seq, lv.answers = None, ["stop"], [], 0, []
    lv.room = lv.handle_due = lv.push_catalog = lambda *a: None
    lv.card_line = lambda: card
    lv.repeat_held = lambda *a, **kw: S.Live.repeat_held(lv, *a, **kw)
    return lv, frames


def test_a_finished_game_answers_every_utterance_with_the_over_line():
    """alf-demo 3c (a): after the win, words that set no task get the over line too — not the card line."""
    lv, frames = turn_stand_in(('{"intent":"UNKNOWN"}', "I don't know what you mean."), won=True, card="A kitchen.")
    j, voice, say = S.Live.utterance(lv, "Alf World the")
    assert voice == S.OVER_LINE and say and lv.status["state"] == "over"
    assert lv.status["options"] == list(S.NEW_GAME_OPTION)
    lv, _ = turn_stand_in(('{"intent":"CHITCHAT"}', "Hello."), won=False, card="A kitchen.")
    assert S.Live.utterance(lv, "hello")[1] == "A kitchen."          # a game in play: the card line as before


def test_a_line_said_again_too_soon_is_shown_not_spoken(monkeypatch):
    """alf-demo 3c (b): the same answer within 20 s of the last say is a row (narration form R) with no
    voice and a dlg-say-held record, whatever woke it; the third within 60 s is held altogether; once
    the windows pass it is spoken again."""
    clock = [1000.0]
    monkeypatch.setattr(S.time, "time", lambda: clock[0])
    lv, frames = turn_stand_in(('{"intent":"UNKNOWN"}', "x"), card="A kitchen. Ask me.")
    assert S.Live.utterance(lv, "what can you do")[1] == "A kitchen. Ask me."
    clock[0] += 3.2                                     # the echo of the card line, heard back
    j, voice, say = S.Live.utterance(lv, "Alf World the")
    assert voice == "" and say == 0
    rows = [f for ev, f in frames if ev == "narration"]
    assert [(r["sentence"], r["kind"], r["form"]) for r in rows] == [("A kitchen. Ask me.", "v3-held", "R")]
    held = [r for r in lv.journal.recs if r["kind"] == "dlg-say-held"]
    assert held[-1]["held"] == "row" and held[-1]["text"] == "A kitchen. Ask me."
    clock[0] += 3.2                                     # the third: no row, no voice
    assert S.Live.utterance(lv, "Alf World")[1] == ""
    assert len([f for ev, f in frames if ev == "narration"]) == 1
    assert [r for r in lv.journal.recs if r["kind"] == "dlg-say-held"][-1]["held"] == "all"
    clock[0] += 61                                      # past both windows: said again
    assert S.Live.utterance(lv, "what can you do")[1] == "A kitchen. Ask me."


def test_the_repeat_guard_holds_a_turn_line_that_was_also_a_narration(monkeypatch):
    """A turn whose answer is a narration said in the same turn (the over line of a task in a finished
    game): the narration is the turn's own (v3-turn: its intent frame voices it), a row the page does not
    voice when held (v3-held, form R), dropped when held altogether."""
    clock = [1000.0]
    monkeypatch.setattr(S.time, "time", lambda: clock[0])
    lv, frames = turn_stand_in(('{"intent":"FETCH"}', S.OVER_LINE), won=True)

    def turn(text, gbnf):
        lv.held.append({"id": "n9", "kind": "v3-line", "form": "N", "sentence": S.OVER_LINE})
        lv.journal.say_rec("line", S.OVER_LINE)
        return '{"intent":"FETCH"}', S.OVER_LINE
    lv.o.turn = turn
    assert S.Live.utterance(lv, "put the tomato in the microwave")[1] == S.OVER_LINE
    clock[0] += 5
    assert S.Live.utterance(lv, "put the tomato in the microwave")[1] == ""
    clock[0] += 5
    S.Live.utterance(lv, "put the tomato in the microwave")
    forms = [(f["kind"], f["form"]) for ev, f in frames if ev == "narration"]
    assert forms == [("v3-turn", "N"), ("v3-held", "R")]


def test_a_pages_help_is_led_by_the_worlds_card_line(monkeypatch):
    """alf-demo 3c O2 (the architect's ruling): under --nav "what can you do?" is world_ui's HELP; the
    world's card line leads and the page's help follows, in the turn's one voice (its result line, said
    in the turn, is the turn's own: v3-turn); in a won game the card line, then the over line; no card:
    the help as it was; the status is left as it is."""
    help_line = "You are in the map. From here you can say: go to <widget>, help."
    lv, frames = turn_stand_in(('{"domain":"world_ui","intent":"HELP"}', "I am ready to assist you."), card="A kitchen. Ask me.")

    def handle_due(beat, due):
        if not lv.served:
            lv.served = True
            n = {"id": "n5", "kind": "v3-line", "form": "N", "sentence": help_line}
            (lv.held.append(n) if lv.in_turn else frames.append(("narration", n)))
    lv.served, lv.in_turn, lv.handle_due = False, False, handle_due
    j, voice, say = S.Live.utterance(lv, "what can you do?")
    assert voice == "A kitchen. Ask me. " + help_line and say and lv.status is None
    assert [(f["kind"], f["sentence"]) for ev, f in frames if ev == "narration"] == [("v3-turn", help_line)]
    lv, frames = turn_stand_in(('{"domain":"world_ui","intent":"HELP"}', S.OVER_LINE), won=True, card="A kitchen. Ask me.")
    assert S.Live.utterance(lv, "what can I ask?")[1] == "A kitchen. Ask me. " + S.OVER_LINE
    lv, frames = turn_stand_in(('{"domain":"world_ui","intent":"HELP"}', "I am ready to assist you."))
    assert S.Live.utterance(lv, "help")[1] == "I am ready to assist you."
