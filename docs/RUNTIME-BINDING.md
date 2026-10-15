# The runtime binding — the C++ IR runtime under agent/go

runtime-parity s-c39746ca step 3 (opus, 2026-10-15): the detailed design,
for the architect's review. No runtime code before it.

Inputs:
- the RUN rows of docs/RUNTIME-PARITY.md (the re-cut);
- the gates verified green (step 2.1);
- brief v2: we can do it; agent parity on the dog, the plant and the
  kitchen pair, not on every corpus.

agent/go is the embedded solution: a small LLM with a text or voice
interface on resource-constrained, offline devices. This binding is what
lets it run a domain model without Python.

## 1. The shape: llama.cpp's, applied to a header-only runtime

`go/internal/llm` binds llama.cpp like this:
- llama.cpp is built in its own checkout (CMake) into `libllama` /
  `libggml`, with a C API in `include/llama.h`;
- `build.sh` points cgo at it (`CGO_CFLAGS -I…/include`, `CGO_LDFLAGS
  -L…/build/bin -Wl,-rpath,…`, classic DT_RPATH so the shared libraries
  find each other);
- one Go package wraps the C calls in Go types (`llm.Load`, `Gen`,
  `Sibling` …).

The metis runtime differs in one way: it is **header-only C++17 with no C
API**, so the binding gives it one, in its own repo, and builds it the same
way:

| | llama.cpp | the metis runtime |
|---|---|---|
| the library | `libllama.so` (CMake, llama.cpp/) | **`libmetis_rt.so`** (CMake, metis-lang/runtime/): the header-only kernel plus one translation unit `metis_rt.cpp` that implements the C ABI |
| the C API | `include/llama.h` | **`runtime/c/metis_rt.h`**: opaque handles, POD event structs, error strings — the same ABI a C or C++ device host links |
| build.sh | `LLAMA_DIR`, `CGO_CFLAGS`, `CGO_LDFLAGS`, rpath | **`METIS_LANG_DIR`** (default: the sibling metis-lang), `-I$METIS_LANG_DIR/runtime/c`, `-L$METIS_LANG_DIR/runtime/build -lmetis_rt`, the same rpath |
| the Go package | `go/internal/llm` | **`go/internal/rt`** |
| CI | the Go job builds llama.cpp at a pinned commit, cached | the Go job checks out metis-lang at a pinned commit and runs `cmake --build runtime/build`, cached on the commit |

**Rehomed from archive/metis, not designed from zero:**
- the Bazel `cc_library metis_kernel` (archive/metis `cpp/BUILD.bazel`)
  becomes metis-lang `runtime/BUILD.bazel`, unchanged, for the C++
  consumers;
- **CMake becomes the primary build**: an INTERFACE target `metis_kernel`,
  the shared `metis_rt`, the six tools, and `ctest`;
- **the MetisCtrl conformance seam** (`Eval(golden path) → parity{ok, total,
  cases, first_fail}`, `MetisCtrl_handlers.cc:48-61` over
  `metis::run_goldens`) becomes `metis_rt_conformance(path)` in the C ABI.
  It is **the binding's first test**: the committed goldens through cgo
  must give the same IR PARITY 46/46 that `ci/check.sh` gives natively.

## 2. The artifact: one directory, one loader, one Program

metispy compiles (the cover, per the ruling). A new metispy emitter writes,
per domain, **the served program after its packs are merged** (the
`apply_packs` union, so the key is the union's):

```
domains/<n>/build/<n>.metis/
  ground.wire       N/E/I/S/B — the existing product ground wire (kernel/wire.py emit_artifact): events c/o/p, stages, init, steps, the B port manifest
  sidecar.json      what the RUN rows need beyond the wire (below)
  program.ir        the product IR (kernel/ir.py) — OPTIONAL: marginals and the planner (IrEvaluator); no RUN row needs it
```

`sidecar.json`, keyed to the wire by event index:

```json
{"format": "metis-sidecar/1",
 "key": "<program key of the union>",              // row 2: records, replies, the KV prefix cache
 "packs": ["visitor.llp"],                         // row 3: what was merged, in order
 "stages": ["main", "…"],                          // the wire's stage indices by name
 "events": [{"i": 0, "name": "dry_out", "kind": "clause", "line": "The sun has been on me for a while; my soil dries out."}],
                                                   // row 4: each event's %% line (the rule's doc, _words applied)
 "cards": [{"name": "kitchen_shared", "relation": "sibling",
            "moves": [{"name": "partner_load", "consume": {"onion_held": 1}, "produce": {"pot_onion": 1}, "persist": {}}]}],
                                                   // row 6: what explain may adopt
 "queries": ["dry", "moist"],                      // row 7: the mirror's #query atoms
 "script": "…"}                                    // row 5: cards.script(), the narrator's and the consult's text
```

**One loader.** `rt.Load(dir)` loads `ground.wire` through the C ABI (the
existing `metis::load_program`, `loader.hpp:40`) and parses `sidecar.json`
in Go. It returns one `*rt.Program`: the runtime handle, the key, the lines
by event, the cards, the queries and the script. `program.ir` is loaded
only when asked for (`rt.LoadIR`).

The wire grammar does not change. `loader.hpp` stops at an unknown record
(its expectation tail), so nothing new is added to it. The sidecar is the
one new format, and metispy writes it with a golden.

**Why not in the wire:** the C++ runtime never needs the lines, the cards
or the script. Only the Go harness reads them, `explain` included
(section 3, "explain needs no K").

## 3. What the C++ runtime grows (K), each against its reference

The live state is new: one object, `metis::Live` in `live.hpp`. It holds
the loaded program, the counts, the stage, the rng and the fired length.
Every K item is a method on it. The references:
- the sampler is spec 06's, **OCaml-authoritative** (`metisc --sample`, the
  `sample_goldens`), and metispy's live REPL already samples by it
  (`repl.py:747`: `SplitMix64`, the left-to-right sum, `threshold < acc`
  over `_candidates()`);
- what has no OCaml reference (do-lines, the host, the watch's projection)
  is checked against metispy's live REPL, the reference agent/py runs on.

| K | the method (C++), the C ABI | semantics, exactly | reference, and the parity test |
|---|---|---|---|
| **K1** the live sampling core | `Live::seed(u64)`, `Live::step(Event&) → Fired\|Quiet\|Done`, `Live::fire(i, Event&)` · `metis_rt_seed`, `metis_rt_step`, `metis_rt_fire` | **the firing tier** is the current stage's enabled clauses (`choice_weight > 0`), port-priced, else its links (`repl.py:690-707`). **The selection:** `threshold = unit()·Σw` (left-to-right sum), the first `i` with `threshold < acc`, else the last (`repl.py:757-767`). **Fire:** consume, produce, drop zeros, `stage = post` on a link (`repl.py:731-737`). **Event** returned: index, kind, stage, post, name, c/o/p, p. `SplitMix64` moves from `tools/sample_run.cpp:62-76` into `sampler.hpp`, and `sample_run.cpp` then uses the library's | OCaml `metisc --sample` → `corpus/goldens/sample_goldens.txt`. `sample_run` rebuilt on the library must keep **SAMPLE PARITY 43/43**. **New golden:** metispy's live REPL, seeded, on the dog's, the plant's and the kitchen pair's ground wires with scripted do-lines, dumps the fired sequence and the counts per step; `Live` reproduces them byte for byte |
| **K2** the enabled tier and do-line deltas | `Live::enabled(vec<(i,w)>)`, `Live::apply(delta, name, Event&)` · `metis_rt_enabled`, `metis_rt_do` | `enabled` is the tier above, with weights (the beat boundary, "no rule took the command"). `apply` is one exo row: `+atom n` / `-atom n`, clamped at 0 (`fw/plugin.py` `parse_do_line`, `exo_event`). It returns an Event of kind `exo` with its ops | metispy `Repl` with a plugin's do-lines (the same golden as K1, whose scripts carry them) |
| **K3** snapshot / restore | `Live::snapshot() → bytes`, `Live::restore(bytes)` · `metis_rt_snapshot`, `metis_rt_restore` | the counts, the stage, the rng state (one u64) and the fired length. **Portable**: atom names, not ids, so a snapshot outlives a reload of the same key | **continuity:** snapshot at step k, run on, restore, rerun gives the same events as the uninterrupted trace (metispy's `fork`/`back` contract, `repl.py:978, 1061`). **Round-trip:** across a reload with the same key |
| **K4** projection from a given state | `ForwardProjection(prog, State from, steps, policy)` (a constructor beside the init one) · `metis_rt_likelihood(rt, from_snapshot, steps, observed)` | P(observed \| the program, from the checkpoint state, over the steps since): what `surprise_record` computes (`kernel/record.py:39-71`), so the watch's surprise test and the record's likelihood. **From `init` it is the existing projection exactly** | the identity, `from = init` equals today's projection (IR PARITY unchanged). **New golden:** metispy's likelihoods on the recorded surprises, the plant's visitor (moist +1, dry −1) and the kitchen's `partner_load`, from their records' `init` and `steps` |
| (B) weights | `metis_rt_bind_weight(rt, port, fn, user)` | the existing `Registry` weight binding (`registry.hpp:181`), a C function pointer. Go binds through one `//export` trampoline. A device host in C++ binds its `std::function` directly | metispy's `PortPricer` on the kitchen pair (`w_partner`): the priced tier equals |
| (B) the event observer | `metis_rt_on_event(rt, fn, user)` | called after every fired or applied row with the Event: **the trace callback, with the event as its payload**, beside the slice `Observer` (`kernel.hpp:116`), which is unchanged | the K1 golden, observed rather than polled |
| (B) reload | `metis_rt_reload(rt, dir)` | the hot swap with degrade-to-last-good (`Evaluator::reload`'s contract, `evaluator.hpp:68-97`): a new artifact or an error, the old one serving on. The state is not carried (agent/py does not carry it either, `Process.reload`) | the old one answers after a failed reload; the key moves |

**Explain needs no K (the step-2 list's K5 drops out).** Reading metispy's
`explain` closely, it has two parts, both harness work over K1/K2:
- **The witness** (`watch.py` `_witness` :629) is a deterministic check, not
  a projection. The card's move is inserted at each position of the fired
  path since the checkpoint. The path is applied event by event (`_apply`
  :614: enabled when every consume and persist is held; then consume,
  produce, drop zeros), and the result must equal the reading on every
  observed atom. The inputs are the fired events' c/o/p (which K1 returns)
  and the move's c/o/p (the sidecar's cards).
- **The adoption** (`_adopt` :513) takes **the host's reading diff** as one
  exo row, named for the move: that is `Do` (K2) with a name.

So K is **K1–K4**. The Go port of `_apply`/`_witness`/`_adopt` is checked
against metispy's `explain` on the kitchen's surprise (`kitchen_shared`,
`partner_load`) and the plant's (the visitor card): the same path, the same
adopted row, the same counts after.

**Not in the runtime:** the watch, the record, the sink, the host's tick
and the surprise verbs, `explain` included, are the Go harness's
(go-domain-agent). Masks, `why`, `necessity`, recover and the consult tools
stay DESIGN.

## 4. The Go surface (`go/internal/rt`)

```go
package rt

type Program struct { /* the handle + the sidecar */ }
func Load(dir string) (*Program, error)           // ground.wire via the C ABI, sidecar.json in Go
func (p *Program) Key() string
func (p *Program) Line(i int) string              // the %% line of event i
func (p *Program) Cards() []Card                  // the moves explain may adopt
func (p *Program) Queries() []string
func (p *Program) Script() string
func (p *Program) Reload(dir string) error        // degrade-to-last-good

type Event struct {
	Index                   int
	Kind, Stage, Post, Name string               // kind: clause | link | exo | move
	Consume, Produce, Persist map[string]int
	P                       float64
	Line                    string               // filled from the sidecar
}

type Step int // Fired | Quiet | Done

func (p *Program) Seed(seed uint64)
func (p *Program) Enabled() []Candidate                     // K2
func (p *Program) Step() (Event, Step)                      // K1
func (p *Program) Do(lines []DoLine) (Event, error)         // K2: one exo row
func (p *Program) Effect(card, move string) (Event, bool) // the move's c/o/p from the sidecar (the witness)
func (p *Program) Counts() map[string]int
func (p *Program) Stage() string
func (p *Program) Snapshot() []byte                         // K3
func (p *Program) Restore(b []byte) error
func (p *Program) Likelihood(from []byte, steps int, observed map[string]int) float64 // K4
func (p *Program) BindWeight(port string, fn func(counts map[string]int) float64)
func (p *Program) OnEvent(fn func(Event))                   // the trace callback → the journal, the narrator

// The host faces (agent/py's PortPlugin), a Go interface; Tick is the harness's.
type Host interface {
	Tick(counts map[string]int) (lines []DoLine, end bool) // the TIME face: commands, world facts, retractions
	OnEvent(name string, args []string)                    // the OUTPUT face: actuation
	Observe(counts map[string]int) map[string]int          // the SENSOR face: the reading the watch compares
	Snapshot() (json.RawMessage, error)                    // the neutral JSON env (row 22): the record's env, the fork
	Restore(json.RawMessage) error
}
func (p *Program) Tick(h Host) (Event, bool) // quiet → the host ticks → its lines as one exo row (repl.py _tick)
```

**For a device host in C++, the C ABI note:** the same five faces as a C
struct of function pointers (`metis_rt_host`), driven by
`metis_rt_tick(rt, host)` in the C ABI. A host written in C++ on the device
(a kitchen appliance's controller) then needs no Go at all. The Go
interface is the same contract seen from Go.

## 5. Parity: what the binding must show, and against what

**At the runtime level (runtime-binding).** Byte-equal fired sequences and
counts against metispy's live REPL on the three domains, seeded, with
scripted do-lines (K1/K2). On top of that: K3's continuity, K4's
likelihoods on the recorded surprises; explain's witness and adopted row (Go, go-domain-agent). Through cgo, the
goldens give 46/46 and the sample goldens 43/43. **Not the corpus**: the
language-level parity (metis-lang ↔ metispy) is already gated (step 2.1),
and this does not repeat it.

**At the agent level (go-domain-agent).** The reference is **agent/py with
the template consult seat** (the ruling: the device's consult is the
template plus the cards' moves). On the dog, the plant and the kitchen pair:
- without the model: the domain agent's generic reply stream, dumped by py
  on the scripted days (`replay` case), and Go's identical to it;
- with the model: the service's `dlg-*` journals and `go/tools/live-reads.py`
  on the same sessions, and the timelines' counts for these three, holding
  with py's.

**The switch.** The Go service config gets `v3.kernel: py | cgo` (and
`-kernel`):
- `py`: today's py domain agent processes. It stays the default and the
  reference;
- `cgo`: the Go domain agent in-process over `go/internal/rt`, on the
  artifacts built from the same catalog.

The journals of both modes are diffed in CI on the scripted days.

## 6. The build in CI

- **metis-lang CI** (public): `cmake -S runtime -B runtime/build && cmake
  --build runtime/build && ctest`. That covers:
  - the goldens through `run_goldens`, and through `metis_rt_conformance`,
    the C ABI;
  - the smokes;
  - `sample_run` on the library;
  - the new K1–K4 goldens.

  `ci/check.sh` is unchanged and must stay ALL GATES GREEN.
- **metispy CI**: the emitter's golden (the three domains' artifact
  directories re-emitted, diffed clean) and the K-golden generators.
- **metis-agent CI** (the Go job, as llama.cpp is pinned):
  - check out metis-lang at a pinned commit;
  - build `libmetis_rt`, cached on the commit;
  - `go vet`, `go test ./internal/rt`: the conformance through cgo and
    the K goldens through Go.

## 7. The step lists (proposed)

**runtime-binding** (opus; the architect reviews):
1. **Rehome and build.**
   - metis-lang `runtime/CMakeLists.txt`: INTERFACE `metis_kernel`,
     `metis_rt` (an empty C ABI so far), the six tools, ctest with the
     goldens and the smokes on fixtures metispy generates;
   - `runtime/BUILD.bazel` from the archive;
   - metis-lang CI builds it.

   Gate: `ci/check.sh` ALL GATES GREEN, ctest green.
2. **The artifact.** metispy's emitter (`python -m metis.kernel.artifact
   <name> -o <dir>`: ground.wire + sidecar.json + program.ir, the packs
   merged), its golden on the dog, the plant and the kitchen pair, and
   `domains/<n>/build/` git-ignored in metis-catalog.
3. **K1 + K2.**
   - `metis::Live` with the normative sampler moved into `sampler.hpp`,
     `enabled`, `fire`, `apply`;
   - `sample_run` on the library (43/43);
   - the K1/K2 golden: metispy's seeded live traces with do-lines on the
     three domains, byte-equal.
4. **K3 + K4.** Snapshot/restore (continuity), projection from a state
   (the identity, and the recorded surprises' likelihoods).
5. **The C ABI and the Go package.**
   - `metis_rt.h` / `metis_rt.cpp` with conformance, load, seed, step,
     enabled, do, snapshot, restore, likelihood, bind_weight, on_event,
     reload;
   - `go/internal/rt` with `build.sh` `METIS_LANG_DIR` and the metis-agent
     CI job.

   Gate: 46/46 and 43/43 through cgo, and the K goldens through Go.

**go-domain-agent** (opus; nina reviews parity):
1. **The neutral JSON env on both stacks.** py first: the host's snapshot
   as JSON data in the records; metispy's `record`/`replay` read it; the
   pickle goes.
2. **The host contract.** The Go `Host` interface plus the C ABI host
   struct; the plant's and the dog's hosts ported to Go; the kitchen pair's
   shared env.
3. **The generic domain agent in Go** over `rt`:
   - the mapper and replier from `intent.yaml`, the AgentHost queues;
   - the watch (checkpoint = `Snapshot`, the surprise test = `Observe`
     against `Likelihood`, the record, the open gate) and the surprise
     verbs (explain = the witness over `Effect` + `Do` named for the move,
     checked against metispy's on the kitchen and the plant);
   - the sink with the JSON env, the ports reply, the pair scene;
   - the narrator fed by `OnEvent`, the template consult.
4. **Parity without a model.** py's reply streams with the template seat on
   the scripted days, diffed against Go's.
5. **The switch and parity on the model.** `v3.kernel: py | cgo`; the
   `dlg-*` journals, `live-reads.py` and the timelines' counts on the dog,
   the plant and the kitchen pair, cgo against py.
6. **The one-model shape.** One LLM, the KV swap per role, the RAG of the
   `%%` lines and the script by key, measured on the same reads at the
   device's footprint.
