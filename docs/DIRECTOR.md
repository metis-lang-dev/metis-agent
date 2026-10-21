# The director: domain activation from context as a domain of its own (the architect, 2026-10-21)

The user: "domain activation from context is a hierarchy of domains: one domain handles the
context switch and the user's interaction, with ports connected to the specialist domains.
We discussed it before; it got lost in the details. It is the linchpin of a real-world
application, and we need the solution."

## What exists, and why it never closed

- The orchestrator (metis-agent) serves several intent domains as a UNION of their cards:
  the J names its domain and the model picks (DESIGN "Several intent domains"). The person
  activates and deactivates domains by hand (the wire's `activate` / `deactivate`); nothing
  activates one from what the world shows.
- The kitchen PAIR (`pair`, `partner_of` in domain.yaml) steps two domains inside one
  clock — a fixed coupling, not a choice.
- E12, the routing corpus: a small model routing words to a domain by itself was a NO-GO.
- console-nav's navigator (`ui_domain.llp`) is, without saying so, a domain whose port
  activates other domains: a pick in the agent console's catalog list sends `activate` on
  the wire; "focus in the REPL's widgets activates the REPL's card" is routing by RULE.

So the pieces exist and the symbolic answer was never written down: the routing that the
small model cannot do is a DOMAIN's job — rules over the observed context, with the model
only typing the person's words into that domain's or the active specialist's intents.

## The director

A **director** is a domain like any other, lifted (a base of rules; an instance of facts),
served by the same agent as its specialists, above them in one respect only: its output
port is the agent's activation.

**Facts (the instance's):** the specialists `specialist(D)`; what each is for —
`context(D, C)` (the contexts it handles: `kitchen`, `garden`, `night`…), `handles(D, A)`
(the atom kinds it understands: `at(kitchen)`, `plant_dry`, `lamp_on`; derived from the
specialist's declared ports and its card's verbs — a generator fills the table from the
manifests, as the intent generator fills grammars), `needs(D, E)` (D is stepped with E: the
pair, generalized), `exclusive(D, E)` (never both active: the GPU budget, a conflict of
effectors), `budget(N)` (at most N active).

**State (one token each):** `here(C)` the current context, `active(D)` / `inactive(D)` for
every specialist, `attention(D)` the specialist the person is talking to (exactly one or
`none`), `pending(D, I)` a person's intent waiting for a specialist that is not active yet.

**The input port (the host's world lines):**
- the context as observed: `+seen(A)` for atoms of the world that some specialist handles
  (a room entered, a thing in view, a sensor's report) — the host knows which atoms are
  context atoms from `handles/2`;
- the specialists' outcomes: `+refused(D, I)`, `+done(D, I)`, `+surprise(D)`, `+idle(D)`
  (the agent already journals these; the director reads them back);
- the person's words, typed by the model: into the DIRECTOR's own card when they are
  about the system ("switch to the kitchen", "what are you doing", "stop everything",
  "which domains are up") and into the ATTENDED specialist's card otherwise — the
  orchestrator serves the union of the director's card and the attended specialist's,
  never every specialist's; `handles/2` gives the director the words a specialist
  understands, so an utterance no active card takes becomes the director's `cmd_route(I)`
  with the candidate specialists as facts.

**The rules (the base's; every refusal with its %% line):**
- *activation by context*: `seen(A) * handles(D, A) * inactive(D) * $under_budget -o
  active(D) * wake(D)` — a context atom of an inactive specialist wakes it; `needs(D, E)`
  wakes E with it; `exclusive(D, E)` with E active → `conflict(D, E)`, an ask to the person
  or a precedence fact;
- *deactivation by absence*: a specialist whose context left (`-seen(A)` for all it
  handles, after a grace of N beats, `linger(D, N)`) and that is `idle(D)` goes `inactive`;
  a busy one stays until done;
- *routing a person's intent*: `cmd_route(I) * candidate(D, I) * active(D) -o
  forward(D, I)`; one inactive candidate → wake it and `pending(D, I)` until its clock says
  ready; several candidates → `ask(I)` ("the plant or the lamp?"); none → `refused(route)`
  with the words; `attention(D)` follows the last routed intent (so a bare "water it" goes
  to the plant while the plant has the attention);
- *escalation*: `refused(D, I)` with another candidate active → re-route once; a
  `surprise(D)` → the attention moves to D and the person is told (the surprise is the
  loop's signal, LEARNING.md);
- *the director's own commands*: switch to D (force attention, wake if needed), sleep D,
  stop all, status, help — the meta-verbs, in the director's card.

**The output port (the host's effects → the agent's wire):** `wake(D)` → `activate{D}`,
`sleep(D)` → `deactivate{D}`, `forward(D, I)` → the intent frame to D (`intent{domain, id,
j}`, the typed-intent frame console-nav added), `ask(I)` → the orchestrator's question,
`status` → the narration. The director never acts in the world: its effects are the
agent's own verbs, nothing else — which is why it is safe to let rules run it.

## Why a domain, not code

Everything above is what the orchestrator would otherwise do in Python by guesswork or by
the model's whim: it becomes rules the person can read, the loop can extend (a new
specialist is a row of facts; a new context, a fact), the explorer can check (one
consumer per intent per state, no two specialists fighting), and the surprise channel can
measure (a context the director did not expect — a room no specialist handles — is a
surprise record of the director's own). The hierarchy is one level and recursive by
construction: a director can be a specialist of another director (a house's director
under a building's), through the same ports.

## Where it runs first

- **The house** (LEARNING.md's third subject): ALFWorld's household IS a house with rooms
  and objects; the first director's instance has the specialists we have (plant, lamp,
  kitchen, dog) placed in ALFWorld's rooms by `context/2`, the room entered as the
  context, the person's words routed. The loop's learning world and the director's testbed
  are one world.
- **The agent console**: the catalog list's pick (console-nav's ui instance) becomes the
  director's `switch` — the navigator asks the director, the director activates — so the
  ui-nav domain and the director share one activation port and the recursion is explicit.

## What to decide before building

1. `handles/2` generation: from a specialist's `input`/`output` port declarations and its
   card's verbs, by the catalog's generator (a new verb of `tools/intents.py`, or the SDK).
2. The attention rule: last-routed vs the person's explicit switch only — the former by
   default, the latter when `exclusive` or a surprise is open.
3. The grace: `linger(D, N)` in beats, per specialist (the plant's hours vs the dog's
   seconds): a manifest key.
4. The orchestrator serves director + attended specialist only — a change to the union
   (metis-agent), and the E2B budget question answered by construction (two cards, not N).
