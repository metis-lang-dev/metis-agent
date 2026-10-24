"""One case of the alf demo's live check through the panel's backchannel (dialog-panel s-277c33e3 step 3):
the service on the demo's catalog (metis-worlds alfworld/experiments/demo.py setup), the world page in a
headless Chrome (world.html?live=1&host), and this test as the host — fsttm_web.chat.Chat at the service's
/host — typing as the person and reading the page's rows, events and state, instead of a CDP driver or a
socket posing as the page. The dialog is the page's (the floor, the turn, the rows as they are tagged).

Opt-in: it loads Gemma E2B on the GPU. METIS_LIVE_WORLD=1, ALFWORLD_DATA (else .worlds/alfworld-data), the
ALFWorld venv (METIS_ALF_PY, else .worlds/alf), a metis-worlds checkout (METIS_WORLDS_DIR, else the sibling),
Chrome (CHROME, else google-chrome)."""
import asyncio
import json
import os
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request
from pathlib import Path

import pytest

AGENT = Path(__file__).resolve().parents[2]


def _up(name):
    for d in AGENT.parents:
        if (d / name).exists():
            return d / name
    return None


WORLDS = Path(os.environ["METIS_WORLDS_DIR"]) if os.environ.get("METIS_WORLDS_DIR") else _up("metis-worlds")
DOT = _up(".worlds")
ALF_PY = os.environ.get("METIS_ALF_PY") or (str(DOT / "alf" / "bin" / "python") if DOT else None)
DATA = os.environ.get("ALFWORLD_DATA") or (str(DOT / "alfworld-data") if DOT else None)
CHROME = os.environ.get("CHROME") or shutil.which("google-chrome") or shutil.which("chromium")
GAMES = "json_2.1.1/train/pick_and_place_simple-Tomato-None-Microwave-13"     # demo.sh's default: the fixtures' kitchen

pytestmark = pytest.mark.skipif(
    os.environ.get("METIS_LIVE_WORLD") != "1" or not (WORLDS and ALF_PY and DATA and CHROME) or not Path(DATA, GAMES).is_dir(),
    reason="the live world case: METIS_LIVE_WORLD=1 (E2B on the GPU), ALFWorld's venv and data, metis-worlds, Chrome")


def _free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def test_the_person_wins_the_demo_through_the_page_from_the_host(tmp_path):
    from fsttm_web.chat import Chat

    port = _free_port()
    root, alf = tmp_path / "root", WORLDS / "alfworld"
    game = Path(DATA, GAMES)
    game = game / sorted(os.listdir(game))[0]
    env = dict(os.environ, ALFWORLD_DATA=DATA)
    out = subprocess.run([ALF_PY, "experiments/demo.py", "setup", str(game), str(root), "--port", str(port)],
                         cwd=alf, env=env, capture_output=True, text=True, timeout=600)
    assert out.returncode == 0, out.stderr[-2000:]
    task = json.loads(out.stdout.strip().splitlines()[-1])["task"]
    web = WORLDS / "web"
    log = open(tmp_path / "service.log", "w")
    svc = subprocess.Popen(["systemd-run", "--user", "--scope", "-q", "-p", "MemoryMax=6G", "-p", "MemorySwapMax=0", "nice",
                            sys.executable, "-m", "metis_agent.service", "--config", str(root / ".run" / "config.yaml"),
                            "--no-wait-viewer", "--ui-dir", str(web), "--lib-dir", str(web / "vendor"), "--addr", "127.0.0.1:%d" % port],
                           cwd=AGENT / "py", stdout=log, stderr=subprocess.STDOUT,
                           env=dict(env, METIS_CATALOG=str(root), METIS_ALF_PY=ALF_PY, PYTHONPATH=str(AGENT / "py")))
    prof, chrome = tempfile.mkdtemp(prefix="ma-host-chrome-"), None

    async def run():
        for _ in range(600):                          # the service's start: the domains' build, the model's load
            try:
                urllib.request.urlopen("http://127.0.0.1:%d/world.html" % port, timeout=1).read()
                break
            except OSError:
                assert svc.poll() is None, "the service exited: %s" % (tmp_path / "service.log")
                await asyncio.sleep(0.5)
        chat = await Chat.connect("ws://127.0.0.1:%d/host" % port)     # first: the host hears the page from its hello
        nonlocal chrome
        chrome = subprocess.Popen([CHROME, "--headless=new", "--user-data-dir=%s" % prof, "--no-first-run",
                                   "http://127.0.0.1:%d/world.html?live=1&host" % port],
                                  stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, start_new_session=True)
        try:
            # the page is there when the strip answers: a ctl comes back as its state
            for _ in range(120):
                m = chat.mark()
                await chat.ctl(barge_in="energy")
                try:
                    await chat.expect(lambda f: f.get("barge_in") == "energy", ev="state", since=m, timeout=1)
                    break
                except TimeoutError:
                    pass
            else:
                raise AssertionError("the page never answered the host: %s" % chat.frames[-3:])
            await chat.ctl(barge_in="words")

            # waiting: the page's status row names the game's task
            await chat.expect(lambda f: task in f["text"], ev="chat", who="note", since=0, timeout=30)

            # the person's words through the panel's one entry: the row, the intent, the director's lines, the win
            m = chat.mark()
            await chat.user("put the tomato in the microwave", id="q1")
            u = await chat.expect(ev="chat", who="user", host_id="q1", since=m, timeout=30)
            assert u["via"] == "host" and u["id"]
            j = await chat.expect(lambda f: "FETCH" in f["text"], ev="chat", who="intent", since=m, timeout=60)
            assert "tomato" in j["text"].lower() and "microwave" in j["text"].lower()
            await chat.expect(lambda f: "game is over" in f["text"].lower(), ev="chat", who="note", since=m, timeout=240)
            said = [f["text"] for f in chat.frames[m:] if f["ev"] == "chat" and f["who"] == "system"]
            assert any("microwave" in s.lower() for s in said), said
            kinds = {f["kind"] for f in chat.frames[m:] if f["ev"] == "event"}
            assert {"final", "llm", "fsttm"} <= kinds and "frame" not in kinds, kinds   # the dialog's events; never the frames

            # alf-demo 3c (a): after the win, words that set no task get the over line, said once
            m = chat.mark()
            await chat.user("what can you do?", id="q2")
            await chat.expect(ev="chat", who="user", host_id="q2", since=m, timeout=30)
            over = await chat.expect(lambda f: "game is over" in f["text"].lower(), ev="chat", who="system", since=m, timeout=60)
            await asyncio.sleep(2.0)
            rows = {f["row"] for f in chat.frames[m:] if f["ev"] == "chat" and f["who"] == "system" and "game is over" in f["text"].lower()}
            assert rows == {over["row"]}, rows                                        # one row: not shown (or said) twice
        finally:
            await chat.close()

    try:
        asyncio.run(run())
    finally:
        if chrome is not None:
            try:
                os.killpg(chrome.pid, signal.SIGTERM)
                chrome.wait(5)
            except (ProcessLookupError, subprocess.TimeoutExpired):
                pass
        svc.terminate()
        try:
            svc.wait(30)
        except subprocess.TimeoutExpired:
            svc.kill()
        log.close()
        shutil.rmtree(prof, ignore_errors=True)
        time.sleep(0.5)
