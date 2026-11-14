"""metis_agent.road_chat — the road domain's CHAT sidecar (road-chat s-3b6bb035; session plan 2026-11-14 P4).

The proposing side of DESIGN.md for the road, off the vehicle: the person's words → one typed intent (local Gemma under
the road `task` domain's generated grammar), handed to the vehicle's RoadDomain through its gateway; the gateway's
generic replies spoken; a marked window of the drive narrated from the rules' own %% lines; a surprise turned into a
question to the tester. Nothing here decides: the vehicle's runtime does, and refuses with its reason.

    vocab.py     the intent material (the task package's intent.yaml + generated prompt/grammar), J → typed intent
    page.py      the deterministic lines (mark, end mark, narrate, explain, why…, stop): no model, before any model
    gateway.py   the gateway's face (Submit / Replies / Read / Mark) and MockGateway, the vehicle emulated
    narrate.py   a window's fired rows → bound %% lines → one short narration (the model, or the template)
    agent.py     ChatAgent: the dialog; rows out for the dialog panel
    server.py    aiohttp: /ws, POST /say, GET /rows, GET /health — viz's dialog panel (bond005, s-db2a0494)
    __main__.py  python -m metis_agent.road_chat [--mock] [--no-model] [--port 8396]
"""
