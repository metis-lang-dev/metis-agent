# The road CHAT sidecar (road-chat s-3b6bb035; session plan 2026-11-14 P4)

metis-agent's proposing side for the road domain, run OFF the vehicle: the person's words → one typed intent →
the vehicle's RoadDomain (bond, s-707ac5dd) through Theia com's mTLS gateway (bond001, s-6e27c559); the replies
spoken; a marked window narrated; a surprise asked about. Nothing it says changes the drive except an intent the
vehicle accepts. Code: `py/metis_agent/road_chat/`; tests: `py/tests/test_road_chat.py`; GPU tools: `py/tools/road/`.

```
 person / viz dialog panel ──HTTP+WS :8396──▶ road_chat ──mTLS gRPC :7720 (RoadDomainIf + RoadReplyFeed)──▶ RoadDomain (vehicle)
                                               │ page.py   mark · end mark · narrate/explain N · why… · stop   (no model)
                                               │ Mapper    Gemma 4 E2B under the task grammar → {"intent":…}  (≈270 ms)
                                               │ ChatAgent typed intent {id, domain:"task", verb, args} → Submit
                                               │           replies by id → spoken; surprise → ask the tester
                                               └ Narrator  events(k0..k1 | ns0..ns1) → bound %% lines → Gemma, held to the lines
```

**Vocabulary.** The road `task` domain, unchanged (metis-worlds `road/domains/task`: intent.yaml + its generated
prompt and grammar; bond loads the same package on the vehicle): PACE LANE TURN STOP GO PASS FOLLOW RESUME AROUND,
and CHITCHAT / UNKNOWN answered locally. TURN is refused on the vehicle (the route is the harness's RouteCmd).

**The gateway contract** (agreed on s-707ac5dd / s-6e27c559; bond001 types it in contracts as Road*):
`Submit(RoadIntent{id, domain, verb, args_json}) → RoadAck{id, status accepted|refused, reason}`;
`RoadReplyFeed` = DESIGN.md's generic reply as JSON, seq monotonic per run, `ref` carrying `k` (RoadDomain's 10 Hz
beat) and `sim_ns`; `Read(RoadRead{tool, domain, args_json}) → RoadReadOut{status, output}` with tool `state`,
`events` ({k0,k1} or {ns0,ns1} → rows `{k, sim_ns, member, rule, binding}`), `why`;
`Mark(RoadMark{n, edge, k0, k1, words})`. `gateway.MockGateway` speaks exactly this until the real one lands.

**The dialog panel's endpoint** (viz, bond005 s-db2a0494): see `server.py`'s docstring —
`GET /health`, `GET /rows?since=`, `POST /say {text}`, `POST /mark {k0,k1 | ns0,ns1, words}`,
`POST /narrate {…| n}`, `GET /ws` (hello with the last 200 rows, then every row; send `{"type":"say"|"mark"|"narrate",…}`).
A row: `{seq, t, who: you|agent|car, kind: said|intent|reply|narration|ask|mark|error|event|quiescence|notice, text, …}`.

**Floor rules kept from DESIGN.md:** stop is a keyword (no model); an accepted intent speaks nothing at the person's
turn (the vehicle's first reply speaks); a refusal or expiry is spoken with its reason; a surprise speaks and asks
"what did you see?": the next line that is not a command is the answer, passed on as `Mark(n, "once:surprise", …)`;
a new command closes the question.

**Narration:** the window's fired rows, runs collapsed, each rule's `%%` line from the catalog (the road family's
planner / responder / signals + task) bound to its firing; Gemma writes one to three sentences, checked: no number
the lines lack, every line's reason and action leave a content word, a finished sentence; else the template
(the lines themselves, joined) speaks. `source: model|template` on the row.

## Run

```bash
export LD_LIBRARY_PATH=$HOME/.local/cuda-12.9/targets/x86_64-linux/lib      # libcudart for the CUDA llama-cpp-python
py/.venv/bin/python -m metis_agent.road_chat --mock               # nice 19, Gemma on the GPU, http://<host>:8396/ (all interfaces: no login, by ruling)
py/.venv/bin/python -m metis_agent.road_chat --mock --no-model    # no GPU: the examples' table + the template narrator
py/.venv/bin/python py/tools/road/score.py                        # the road corpus (89 rows, held out from the examples)
py/.venv/bin/python py/tools/road/narr.py                         # narration over the mock's windows
```
The GPU is shared with CARLA: nothing on it during a CARLA timing run (bond003, s-ed11aa23).

## Results (2026-11-14, Gemma 4 E2B Q4_K_M, the 4090 laptop beside CARLA)

- corpus: **84/89 = 94.4 %** (page lines 15/15; model rows 67/72, mean 270 ms). Misses: "step on it" → UNKNOWN,
  "continue driving" → RESUME, "park the car in the garage" → STOP, "do a u-turn" → TURN LEFT, "drive at 200 km/h"
  → PACE BRISK. Candidates for the task intent.yaml's examples (its owner's change, not made here).
- narration: 5/5 windows by the model, each passing the faithfulness check, 0.3–1.1 s.
