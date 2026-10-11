# LLM-CLM-LL system design

Oct 10, 2026 · @Chief

A small local LLM becomes useful for long-running work when a metis runtime owns state, control and probability, and the LLM only narrates, selects domains and judges surprises. The LLM decides how soon the system resumes, never whether it is safe. This tab replaces the baseline, which had the two systems the other way round.

## Roles

The runtime takes over everything a small LLM does badly, and leaves it the four things it does well.

| Concern | Owner | The LLM's part |
| --- | --- | --- |
| State over a long run | Runtime | Reads a mirror of it |
| Step-by-step procedure | Domain rules | Picks which domain applies |
| Uncertainty | Runtime, by exact inference | Puts the result into words |
| Safety and legality | Orchestrator; observe-only mode on surprise | None; it cannot override |
| When to think | Signals from the orchestrator | Wakes when signalled |
| Changing the rules | Revision loop, offline | Describes what went wrong |
| Language and intent | LLM | Turns a request into one typed intent |
| Judging a surprise | LLM proposes, runtime checks | Suggests which domain explains it |

In Kahneman's terms the runtime is System 1: fast, at about 10 ms per evaluation, automatic and always on. The LLM in thinking mode is System 2, woken only by signals. Narration is the LLM in single-pass mode, a voice for System 1 with no authority.

Trust runs the opposite way from the human case. Here the fast side is certified and the slow side is fallible, so the slow side gets narrow authority and the fast side checks it.

Improvements ship as catalog changes, not model weights. A deployed device gets smarter without retraining its LLM.

## Architecture

The LLM sits above the runtime and talks to one node only, the orchestrator.

&#91;embedded content: system architecture · LLM above a metis domain tree, with catalog and revision loop\]

Signals climb the tree and each level may handle them. Only what the orchestrator cannot handle wakes the LLM. Intents are the LLM's sole way to act, and the orchestrator consumes each one exactly once or rejects it.

## One shape, three hosts

Nothing in the architecture is specific to robots; only the ports and the catalog change. The entries below are illustrations.

| Part | Robot | Cooking machine | Cloud agent |
| --- | --- | --- | --- |
| Always-on domain | Locomotion | Thermal and safety limits | Permissions and spend limits |
| Task domain | Navigation | Progress through a recipe | The workflow in hand |
| On-demand domains | Interactions: grasp, hand over, open a door | Techniques: knead, sear, rest | Procedures: refund, deploy, triage a ticket |
| Ports | Sensors and actuators | Heaters, motors, probes | Tool calls and their results |
| Observe-only mode | Stop acting, keep sensing | Heat off, keep sensing | No writes or sends, keep reading |
| Who runs revision | Fleet engineer | Product team | Operations owner |

The cloud-agent column is the general case for local LLM deployments. The small model stops being the agent and becomes the supervisor of one.

## Orchestrator contract

The orchestrator is defined by a contract, not an implementation, so it can be a metis domain or a ROS behavior tree.

Three signals go up. Each carries the path of the domain that raised it, and any ancestor may handle it first.

| Signal | Meaning | LLM mode it triggers |
| --- | --- | --- |
| Event | A domain made progress or changed a belief | Narrate |
| Quiescence | A domain has nothing left it can do | Decide, if a goal is still pending |
| Surprise | An observation the domain gives zero probability; the system is now observe-only | Decide |

Intents go down. This starting vocabulary is a proposal; the orchestrator's own signature is the authority.

| Intent | Effect |
| --- | --- |
| `activate(domain, bindings)` | Attach a catalog domain at a node of the tree, with its grounding |
| `deactivate(domain)` | Detach it |
| `reset(domain)` | Restart a domain from fresh inputs, for a transient fault |
| `resume(domain)` | Leave observe-only mode once a surprise is explained |
| `ask(question)` | Put a clarifying question to the user |
| `escalate(record)` | Send a surprise to the revision loop |

Four rules hold for any implementation:

- **Typed.** Intents are decoded under a grammar built from the signature, so the LLM cannot emit a malformed one.
- **Exactly once.** An intent is consumed by one rule or rejected with a reason, and the rejection returns as a signal.
- **Checked.** The orchestrator decides legality and cost. An activation is admitted only if the resulting tree is a certified configuration.
- **No deadlines.** If the LLM is slow or absent, the system stays in observe-only mode. Safety never depends on a reply.

A metis orchestrator gives exactly-once for free, because an intent is a linear resource, and it puts the LLM-facing surface inside the certified part. A behavior tree needs an adapter to enforce both.

## The three cases

Narration and requests are routine; surprise is where the LLM earns its place, and where it needs the tightest check.

**Case 1: narration.** Trigger: an event.

1. The harness updates the tree mirror in the context file. No LLM call is needed for this.
2. The LLM, in narrate mode, writes a sentence or two for the user. Numbers are copied from the signal, never generated.
3. No intent is produced. Narration may lag or drop events without affecting the runtime.

**Case 2: requested change.** Trigger: a user or external request.

1. The LLM, in decide mode, maps the request to one intent. It chooses among the cards of domains that can attach to the current tree.
2. The orchestrator admits the intent or rejects it with a reason.
3. On rejection or an unclear request, the LLM tells the user or asks.

**Case 3: surprise.** Trigger: an observation with zero probability.

1. The system enters observe-only mode by rule. The LLM is not involved.
2. The surprise climbs the tree. If an ancestor has a rule for it, that rule runs and the LLM only narrates.
3. Otherwise the LLM wakes in decide mode. The context file expands the path to the surprised domain and shows the surprise record.
4. The LLM proposes up to three explanations, each as an intent: reset the same domain, or activate a different one from the catalog.
5. The runtime tests each proposal against the observation. A proposal counts only if the observation has non-zero probability under it.
6. One of three things follows:
   - A proposal explains the observation. The intent is applied and the system resumes.
   - Nothing explains it. The LLM escalates the record with its own description, and the system stays observe-only.
   - The same surprise returns after a reset. No further resets are allowed; it escalates.

Step 5 is the guard against the LLM's main weakness here, which is producing a fluent explanation for anything. Accepting a surprise means a model explains it, not that the LLM says so.

The LLM's description travels with the surprise record. The revision loop already mines text for candidate rules, so on-device narration becomes its input.

## Context file

The LLM's context is a mirror of the runtime's domain tree, so "context" means the same thing on both sides.

| Region (in order) | Holds | Written by | Lifetime |
| --- | --- | --- | --- |
| Stable prefix | Role, intent grammar, journal rules | Harness | Per deployment |
| Catalog cards | One card per domain that can attach to the current tree: name, purpose, bindings | Harness | Refreshed when the tree changes |
| Journal | Requests received, intents sent and their results, surprises and outcomes | LLM appends and compacts | Rolling |
| Tree mirror | One line per active domain: status and the beliefs it reports | Harness, from signals | Overwritten in place |
| Inbox | The signal or request that woke the LLM | Orchestrator or user | One wake |

Four properties follow from this layout:

- **Activation is an edit.** Attaching a domain adds a mirror line and refreshes the cards. Detaching evicts both.
- **Size is bounded by the tree, not by time.** The file grows with the number of active domains. An hour of operation costs no more context than a minute.
- **Depth is handled by path expansion.** Each domain gets one line. On a surprise, the path from the root to that domain shows full detail, then collapses.
- **The hierarchy prunes the choices.** Only domains that can attach where the tree stands appear as cards, which keeps the option list short for a small model.

This narrows CLM as the paper defines it. There the model may rewrite the whole file. Here the harness renders everything except the journal, because the paper's own 9B results show small models rarely edit unprompted. The journal is where the paper's methods apply: steering by a skill document first, training later.

Regions are ordered from most stable to most volatile, so an ordinary prefix cache covers most of each wake.

## What the small LLM has to do

The LLM's whole job is four short tasks, and each decision is checked by the runtime before it takes effect.

| Task | Mode | It reads | It writes |
| --- | --- | --- | --- |
| Narrate an event | Single pass | One mirror line and the event | A sentence or two |
| Map a request to an intent | Thinking | The request and a short list of cards | One grammar-constrained intent |
| Propose explanations for a surprise | Thinking | The surprise record, the expanded path, the cards | Up to three intents |
| Describe a surprise for escalation | Single pass | The record and the journal | A short paragraph |

These are rewriting, classification with slot filling, and summarization. None needs long reasoning chains, and none needs the model to write LLP.

The candidates from Baseline (superseded) still apply. Qwen3.5-4B and Gemma 4 E4B switch thinking on and off, so one set of weights serves both modes.

## Build plan

Five phases; the first three need no training. Gate thresholds are to be set with the first measurements.

1. **Contract and harness.** Fix the orchestrator contract. Build the two LLM modes, grammar-constrained intents and the context file. Use a simulated host; the Overcooked kitchen in the metis catalog is a ready one. Gate: requests reliably end in an admitted intent or a correct rejection.
2. **Surprise path.** Add observe-only mode, the explanation test, bounded resets and the escalation record. Inject three kinds of surprise: transient, wrong domain, and novel. Gate: no resume without an explaining model, and triage beats always-escalate.
3. **Long runs.** Operate for hours with domains attaching and detaching. Gate: context size stays flat and narration stays faithful to the trace.
4. **Tuning.** Evolve the skill document for the decide prompt and the journal, with a stronger model as proposer. Then fit a LoRA on logged requests and surprises. The orchestrator's verdicts label that data for free.
5. **Second host.** Repeat on a cloud agent with real tools, to show the shape transfers.

## Evaluation

Four arms on the same host and tasks show what each part adds, and how much of the gap to a large model the system closes.

| Arm | Setup | Isolates |
| --- | --- | --- |
| A. Small LLM alone | Agent with tools and an append-only context | The floor |
| B. Small LLM with runtime | Mirror rebuilt at each wake, no journal | What the runtime adds (A vs B) |
| C. Full system | Mirror plus journal | What the journal adds (B vs C) |
| D. Large LLM alone | Arm A with a frontier model | The reference C is measured against |

Measures, per arm:

- Task success over long runs
- Illegal or unsafe actions executed; zero is expected in B and C by construction
- Intent validity: admitted or correctly rejected, over all intents
- Surprise triage accuracy on injected surprises, by kind
- False resumes: leaving observe-only mode without an explaining model; must be zero
- Narration faithfulness: share of stated facts that match the trace
- LLM wakes per hour, and latency per wake
- Context size over time

Stop conditions:

- B does not beat A. The premise fails.
- C matches B. Drop the journal; a stateless mirror is enough and nothing of CLM remains.
- Triage is no better than always-escalate. Keep the LLM as narrator and request mapper only.

## Open questions

Most of these are about what the metis runtime does today, so they are yours to answer.

- [ ] **Composition.** Is an active tree certified as a whole ahead of time, or do certificates compose when a domain attaches? This decides what `activate` may do on a device.
- [ ] **Scope of observe-only.** Does a surprise freeze the whole system or only the surprised subtree?
- [ ] **Explanation test.** Can the runtime score a stored observation under a candidate domain on the device? Case 3 depends on it.
- [ ] **Report set.** How does a domain declare which beliefs appear in its mirror line?
- [ ] **Soft surprise.** Should a low but non-zero probability wake the LLM, or only zero?
- [ ] **Silent wrong context.** A permissive domain never raises a surprise. Is shadow evaluation of sibling domains worth its cost?
- [ ] **Catalog size.** How many domains can attach at one node before cards need retrieval instead of listing?
- [ ] **First orchestrator.** Metis or a behavior tree for version one?

One constraint outside metis: the CLM reference code is CC BY-NC 4.0. The mirror and journal harness is small enough to write fresh.

## Sources

- Your account of metis in this conversation: evaluation in about 10 ms, I/O ports, the domain hierarchy with shared state, the orchestration domain, observe-only mode on surprise, and the license now being added.
- [Context Language Models, arXiv 2609.37725](https://arxiv.org/pdf/2609.37725): context as a file, the 9B editing results, steering by skill document.
- [metis-lang organization page](https://github.com/metis-lang-dev): the revision loop from surprise records to approved catalog changes, and the Overcooked catalog.
- [Behavior Programs as Measures over Proofs, draft 0.3](https://github.com/metis-lang-dev/metis-lang/blob/master/paper/metis.tex): zero probability as the surprise signal, admission-time certificates.
- Model cards and the full source list are in Baseline (superseded).

The contract, the intent vocabulary, the explanation test, the context layout and the evaluation plan are proposals of this document.
