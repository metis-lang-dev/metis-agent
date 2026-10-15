// Package rt binds the metis runtime (metis-lang's libmetis_rt, the C ABI
// runtime/c/metis_rt.h) the way internal/llm binds llama.cpp: one cgo
// package wrapping the C calls in Go types (runtime-binding step 5,
// docs/RUNTIME-BINDING.md §4).
//
// A Runtime is one live handle (metis::Live): K1 Seed/Step/Fire, K2
// Enabled/Do, K3 Snapshot/Restore, K4 Likelihood, the weight ports bound
// by Go functions, an event observer, Reload with degrade-to-last-good.
// Load (artifact.go) opens an artifact directory: ground.wire through the
// C ABI, sidecar.json parsed here.
//
// Build: go/build.sh points cgo at METIS_LANG_DIR (default: the sibling
// metis-lang checkout) and builds runtime/build/libmetis_rt.so there.
package rt

/*
#cgo LDFLAGS: -lmetis_rt
#include <stdlib.h>
#include <metis_rt.h>

// the Go trampolines (callbacks.go); a handle is a cgo.Handle, passed as an
// integer so no Go pointer crosses
extern double goMetisWeight(void* user, char** args, size_t n_args, metis_rt_view* counts);
extern void goMetisEvent(void* user, metis_rt_event* ev);

static metis_rt_status rt_bind_weight(metis_rt* rt, const char* port, uintptr_t h) {
    return metis_rt_bind_weight(rt, port, (metis_rt_weight_fn)goMetisWeight, (void*)h);
}
static metis_rt_status rt_on_event(metis_rt* rt, uintptr_t h) {
    return metis_rt_on_event(rt, h ? (metis_rt_event_fn)goMetisEvent : NULL, (void*)h);
}
*/
import "C"

import (
	"fmt"
	"runtime/cgo"
	"sort"
	"unsafe"
)

// ABIVersion is the C ABI this package was built against.
const ABIVersion = int(C.METIS_RT_ABI_VERSION)

// LibABIVersion is the C ABI the loaded libmetis_rt was built with.
func LibABIVersion() int { return int(C.metis_rt_abi_version()) }

// Status is a metis_rt_status.
type Status int

const (
	OK          Status = C.METIS_RT_OK
	EIO         Status = C.METIS_RT_EIO
	EParity     Status = C.METIS_RT_EPARITY
	EArg        Status = C.METIS_RT_EARG
	EInternal   Status = C.METIS_RT_EINTERNAL
	ENoProgram  Status = C.METIS_RT_ENOPROGRAM
	EUnbound    Status = C.METIS_RT_EUNBOUND
	ENotEnabled Status = C.METIS_RT_ENOTENABLED
	ELoad       Status = C.METIS_RT_ELOAD
)

// Error is a failed call: its status and the handle's text.
type Error struct {
	Status Status
	Text   string
}

func (e *Error) Error() string { return fmt.Sprintf("metis_rt %d: %s", e.Status, e.Text) }

// Done is the stage index of a latched world.
const Done = int(C.METIS_RT_DONE)

// Parity is one golden corpus replayed (metis_rt_conformance).
type Parity struct {
	OK, Total, Cases int
	Corpus           string // "ground" | "ir" | "sample"
	FirstFail        string
}

// Conformance replays a committed golden corpus through the runtime's own
// engines (the archive's MetisCtrl Eval): nil only when ok == total > 0.
func Conformance(path string) (Parity, error) {
	cp := C.CString(path)
	defer C.free(unsafe.Pointer(cp))
	var p C.metis_rt_parity
	s := Status(C.metis_rt_conformance(cp, &p))
	out := Parity{OK: int(p.ok), Total: int(p.total), Cases: int(p.cases),
		Corpus:    [...]string{"ground", "ir", "sample"}[p.corpus],
		FirstFail: C.GoString(&p.first_fail[0])}
	if s != OK {
		return out, &Error{s, out.FirstFail}
	}
	return out, nil
}

// Count is one atom's count.
type Count struct {
	Atom string
	N    int
}

// Event is one fired or applied row (metis_rt_event).
type Event struct {
	Index, Canon int    // wire index, normative index; -1 for an exo row
	Kind         string // "clause" | "link" | "exo"
	Stage, Post  int
	Name         string // exo: "do +a -b 2" | "tick"
	Consume      []Count
	Produce      []Count
	Persist      []Count
	Ops          []string
	P            float64  // its probability in its tier; 0 for exo
	Args         []string // the ground args, "go[P=tag3]" -> [tag3]: a host's OnEvent input
}

// Result of a Step.
type Result int

const (
	Fired   Result = C.METIS_RT_FIRED
	Quiet   Result = C.METIS_RT_QUIET   // nothing enabled: the host's tick, or LatchDone
	Stopped Result = C.METIS_RT_STOPPED // the world is latched
)

// Candidate is one event of the firing tier.
type Candidate struct {
	Index  int
	Weight float64
}

// Counts reads atom counts: the runtime's during a weight call (View), or a
// plain map (MapCounts) — a weight port prices from it.
type Counts interface{ Count(atom string) int }

// MapCounts is Counts over a map.
type MapCounts map[string]int

// Count is the atom's count (0 when absent).
func (m MapCounts) Count(atom string) int { return m[atom] }

// WeightFunc prices one ground event of a weight port: its resolved args,
// the counts now.
type WeightFunc func(args []string, counts Counts) float64

// View reads the runtime's counts during a WeightFunc call.
type View struct{ v *C.metis_rt_view }

// Count is the atom's count now.
func (v View) Count(atom string) int {
	ca := C.CString(atom)
	defer C.free(unsafe.Pointer(ca))
	return int(C.metis_rt_view_count(v.v, ca))
}

// Runtime is one live handle. Not safe for concurrent use.
type Runtime struct {
	p        *C.metis_rt
	weights  map[string]cgo.Handle
	observer cgo.Handle
}

// New makes an empty handle; Close frees it.
func New() *Runtime {
	return &Runtime{p: C.metis_rt_new(), weights: map[string]cgo.Handle{}}
}

// Close frees the handle and its Go callbacks.
func (r *Runtime) Close() {
	if r.p != nil {
		C.metis_rt_free(r.p)
		r.p = nil
	}
	for _, h := range r.weights {
		h.Delete()
	}
	r.weights = map[string]cgo.Handle{}
	if r.observer != 0 {
		r.observer.Delete()
		r.observer = 0
	}
}

func (r *Runtime) check(s C.metis_rt_status) error {
	if Status(s) == OK {
		return nil
	}
	return &Error{Status(s), C.GoString(C.metis_rt_error(r.p))}
}

// wireArgs holds the C copies of Load's arguments.
type wireArgs struct {
	path   *C.char
	canon  []C.int
	stages []*C.char
}

func newWireArgs(path string, canon []int, stages []string) *wireArgs {
	w := &wireArgs{path: C.CString(path)}
	for _, c := range canon {
		w.canon = append(w.canon, C.int(c))
	}
	for _, s := range stages {
		w.stages = append(w.stages, C.CString(s))
	}
	return w
}

func (w *wireArgs) free() {
	C.free(unsafe.Pointer(w.path))
	for _, s := range w.stages {
		C.free(unsafe.Pointer(s))
	}
}

// call runs a load-like C function on pinned argument arrays.
func (w *wireArgs) call(f func(path *C.char, canon *C.int, nc C.size_t, stages **C.char, ns C.size_t) C.metis_rt_status) C.metis_rt_status {
	var cp *C.int
	if len(w.canon) > 0 {
		cp = &w.canon[0]
	}
	// the stage pointers live in C memory: the array of them is copied
	// to C as well, so no Go memory holds C pointers across the call
	var sp **C.char
	if len(w.stages) > 0 {
		n := len(w.stages)
		arr := (**C.char)(C.malloc(C.size_t(n) * C.size_t(unsafe.Sizeof(uintptr(0)))))
		defer C.free(unsafe.Pointer(arr))
		copy(unsafe.Slice(arr, n), w.stages)
		sp = arr
	}
	return f(w.path, cp, C.size_t(len(w.canon)), sp, C.size_t(len(w.stages)))
}

// LoadWire serves a ground wire: canon = the sidecar's events[i].canon
// (nil: the wire's order), stages = the sidecar's stage names (nil: the
// wire's unnamed ones). The seed and the bound weights carry over.
func (r *Runtime) LoadWire(path string, canon []int, stages []string) error {
	w := newWireArgs(path, canon, stages)
	defer w.free()
	return r.check(w.call(func(p *C.char, c *C.int, nc C.size_t, s **C.char, ns C.size_t) C.metis_rt_status {
		return C.metis_rt_load(r.p, p, c, nc, s, ns)
	}))
}

// ReloadWire is the hot swap: a new program, or an error and the old one
// serving on with its state untouched (a port the new program prices that
// is not bound here refuses it). The state is not carried.
func (r *Runtime) ReloadWire(path string, canon []int, stages []string) error {
	w := newWireArgs(path, canon, stages)
	defer w.free()
	return r.check(w.call(func(p *C.char, c *C.int, nc C.size_t, s **C.char, ns C.size_t) C.metis_rt_status {
		return C.metis_rt_reload(r.p, p, c, nc, s, ns)
	}))
}

// BindWeight binds (or rebinds) a weight port; it persists across loads.
func (r *Runtime) BindWeight(port string, fn WeightFunc) error {
	cp := C.CString(port)
	defer C.free(unsafe.Pointer(cp))
	h := cgo.NewHandle(fn)
	if err := r.check(C.rt_bind_weight(r.p, cp, C.uintptr_t(h))); err != nil {
		h.Delete()
		return err
	}
	if old, ok := r.weights[port]; ok {
		old.Delete()
	}
	r.weights[port] = h
	return nil
}

// OnEvent sets the observer, called after every fired or applied row
// (nil removes it).
func (r *Runtime) OnEvent(fn func(Event)) error {
	var h cgo.Handle
	if fn != nil {
		h = cgo.NewHandle(fn)
	}
	if err := r.check(C.rt_on_event(r.p, C.uintptr_t(h))); err != nil {
		if h != 0 {
			h.Delete()
		}
		return err
	}
	if r.observer != 0 {
		r.observer.Delete()
	}
	r.observer = h
	return nil
}

// Seed sets the normative rng (and the seed a load or reload starts from).
func (r *Runtime) Seed(seed uint64) error { return r.check(C.metis_rt_seed(r.p, C.uint64_t(seed))) }

// Step is one normative selection and its firing (K1).
func (r *Runtime) Step() (Result, Event, error) {
	var res C.metis_rt_step_result
	var ev C.metis_rt_event
	if err := r.check(C.metis_rt_step(r.p, &res, &ev)); err != nil {
		return 0, Event{}, err
	}
	if Result(res) != Fired {
		return Result(res), Event{}, nil
	}
	return Fired, goEvent(&ev), nil
}

// Fire fires wire event i if it is in the tier now (a forced row).
func (r *Runtime) Fire(i int) (Event, error) {
	var ev C.metis_rt_event
	if err := r.check(C.metis_rt_fire(r.p, C.int(i), &ev)); err != nil {
		return Event{}, err
	}
	return goEvent(&ev), nil
}

// Enabled is the firing tier in the normative order (K2).
func (r *Runtime) Enabled() ([]Candidate, error) {
	var n C.size_t
	if err := r.check(C.metis_rt_enabled(r.p, nil, nil, 0, &n)); err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, nil
	}
	idx := make([]C.int, n)
	w := make([]C.double, n)
	if err := r.check(C.metis_rt_enabled(r.p, &idx[0], &w[0], n, &n)); err != nil {
		return nil, err
	}
	out := make([]Candidate, len(idx))
	for k := range idx {
		out[k] = Candidate{int(idx[k]), float64(w[k])}
	}
	return out, nil
}

// Do applies one exogenous row from do-lines (K2); none is a "tick" row.
// A latched world revives.
func (r *Runtime) Do(lines ...string) (Event, error) {
	var arr **C.char
	if len(lines) > 0 {
		arr = (**C.char)(C.malloc(C.size_t(len(lines)) * C.size_t(unsafe.Sizeof(uintptr(0)))))
		defer C.free(unsafe.Pointer(arr))
		s := unsafe.Slice(arr, len(lines))
		for i, l := range lines {
			s[i] = C.CString(l)
		}
		defer func() {
			for _, p := range s {
				C.free(unsafe.Pointer(p))
			}
		}()
	}
	var ev C.metis_rt_event
	if err := r.check(C.metis_rt_do(r.p, arr, C.size_t(len(lines)), &ev)); err != nil {
		return Event{}, err
	}
	return goEvent(&ev), nil
}

// LatchDone latches the world: nothing can fire and the host's episode is over.
func (r *Runtime) LatchDone() error { return r.check(C.metis_rt_latch_done(r.p)) }

// Stage is the stage now (Done when latched) and its name.
func (r *Runtime) Stage() (int, string, error) {
	var s C.int
	var name *C.char
	if err := r.check(C.metis_rt_stage(r.p, &s, &name)); err != nil {
		return 0, "", err
	}
	return int(s), C.GoString(name), nil
}

// Counts are the counts now, sorted by atom name.
func (r *Runtime) Counts() ([]Count, error) {
	var p *C.metis_rt_count
	var n C.size_t
	if err := r.check(C.metis_rt_counts(r.p, &p, &n)); err != nil {
		return nil, err
	}
	return goCounts(p, n), nil
}

// Fired is the number of rows fired or applied.
func (r *Runtime) Fired() (int, error) {
	var n C.size_t
	err := r.check(C.metis_rt_fired(r.p, &n))
	return int(n), err
}

// NEvents is the number of the program's ground events.
func (r *Runtime) NEvents() (int, error) {
	var n C.size_t
	err := r.check(C.metis_rt_n_events(r.p, &n))
	return int(n), err
}

// Snapshot is the live state as portable text (K3).
func (r *Runtime) Snapshot() (string, error) {
	var n C.size_t
	if err := r.check(C.metis_rt_snapshot(r.p, nil, 0, &n)); err != nil {
		return "", err
	}
	buf := (*C.char)(C.malloc(n + 1))
	defer C.free(unsafe.Pointer(buf))
	if err := r.check(C.metis_rt_snapshot(r.p, buf, n+1, &n)); err != nil {
		return "", err
	}
	return C.GoStringN(buf, C.int(n)), nil
}

// Restore puts a snapshot's state back, all or nothing (K3).
func (r *Runtime) Restore(snapshot string) error {
	cs := C.CString(snapshot)
	defer C.free(unsafe.Pointer(cs))
	return r.check(C.metis_rt_restore(r.p, cs))
}

// Likelihood is P(each observed atom has exactly its count | the program,
// from the snapshot's state, over steps), priced as the tier is (K4).
func (r *Runtime) Likelihood(from string, steps int, observed map[string]int) (float64, error) {
	cf := C.CString(from)
	defer C.free(unsafe.Pointer(cf))
	atoms := make([]string, 0, len(observed))
	for a := range observed {
		atoms = append(atoms, a)
	}
	sort.Strings(atoms)
	n := len(atoms)
	var ca **C.char
	var cc *C.int
	if n > 0 {
		ca = (**C.char)(C.malloc(C.size_t(n) * C.size_t(unsafe.Sizeof(uintptr(0)))))
		defer C.free(unsafe.Pointer(ca))
		cc = (*C.int)(C.malloc(C.size_t(n) * C.size_t(unsafe.Sizeof(C.int(0)))))
		defer C.free(unsafe.Pointer(cc))
		as, cs := unsafe.Slice(ca, n), unsafe.Slice(cc, n)
		for i, a := range atoms {
			as[i] = C.CString(a)
			cs[i] = C.int(observed[a])
		}
		defer func() {
			for _, p := range as {
				C.free(unsafe.Pointer(p))
			}
		}()
	}
	var like C.double
	if err := r.check(C.metis_rt_likelihood(r.p, cf, C.int(steps), ca, cc, C.size_t(n), &like)); err != nil {
		return 0, err
	}
	return float64(like), nil
}

func goCounts(p *C.metis_rt_count, n C.size_t) []Count {
	if n == 0 {
		return nil
	}
	out := make([]Count, n)
	for i, c := range unsafe.Slice(p, n) {
		out[i] = Count{C.GoString(c.atom), int(c.n)}
	}
	return out
}

func goEvent(e *C.metis_rt_event) Event {
	ev := Event{Index: int(e.index), Canon: int(e.canon), Stage: int(e.stage), Post: int(e.post),
		Name: C.GoString(e.name), P: float64(e.p),
		Consume: goCounts(e.consume, e.n_consume),
		Produce: goCounts(e.produce, e.n_produce),
		Persist: goCounts(e.persist, e.n_persist)}
	switch e.kind {
	case C.METIS_RT_LINK:
		ev.Kind = "link"
	case C.METIS_RT_EXO:
		ev.Kind = "exo"
	default:
		ev.Kind = "clause"
	}
	if e.n_ops > 0 {
		for _, o := range unsafe.Slice(e.ops, e.n_ops) {
			ev.Ops = append(ev.Ops, C.GoString(o))
		}
	}
	if e.n_args > 0 {
		for _, a := range unsafe.Slice(e.args, e.n_args) {
			ev.Args = append(ev.Args, C.GoString(a))
		}
	}
	return ev
}
