"""The Metis runtime (plugin/docs/RUNTIME-INTERFACE.md): the feed one runtime serves (feed.RuntimeFeed), the client a
plugin or a UI backend attaches with (client.RuntimeClient), the headless driver (run.py) and the host face (host.py).
Domain-free: a domain brings only its RuntimeHost.

The headless runtime (serve.py, GAPS B8a): a nightly workbench records surprises with nobody attached and no UI.

    python -m metis_agent.runtime.serve --name N --workspace W [--follow-trace DIR --config CFG] [--until-file F | --linger S]

It writes, under W/.metis/ (made when missing): runtimes/<N>.json (the discovery file, while it serves; removed at the
stop), loop/journal.jsonl rows runtime.start / runtime.stop, and, following DIR (a stack's metis.trace, tailed as it is
written): traces/<run>/ (the mirror of DIR, with the config.yaml it was replayed on), and per surprise of a member's watch
(NOT blind, whatever DIR's header says) surprises/<id>.json with its window kept as traces/<run>-<key>/ (the loop's own
format, workspace.drop_window), each carrying fingerprint / layer / severity (fingerprint.py). At the stop (SIGTERM,
SIGINT, F there, S seconds; following: S seconds idle) it prints its summary and writes it to runtimes/<N>.summary.json:
{name, recording, surprises: [ids], findings, segments, errors, ...}. RECORDING means the runtime served for its whole
life and, following, its watch ran over the trace: every surprise in it is a record. recording: false names why in
errors (no config, another image, a trace it cannot replay); the command then exits 1."""
