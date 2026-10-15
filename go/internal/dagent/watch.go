package dagent

// The runtime's watch over rt (metispy metis/loop/watch.py Watch): the
// driver of one watched episode. Step advances the world by one thing — a
// fired intent, an entry into quiescence, or one env beat — and returns the
// signals it raised; a reading that differs from the model is scored from
// the last consistent checkpoint (K4) and logged as a surprise record; an
// unmodeled one turns the session observe-only until an intent (explain,
// reset, escalate) or the operator closes it.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/metis-lang-dev/metis-agent/go/internal/rt"
)

// Signal is one watch signal (py's dict).
type Signal map[string]any

// SideCard is a sidecar card: a world that could explain a surprise.
type SideCard struct {
	Name     string   `json:"name"`
	Text     string   `json:"text"`
	Relation string   `json:"relation"`
	Moves    []string `json:"moves"`
	Effects  []struct {
		Name    string         `json:"name"`
		Consume map[string]int `json:"consume"`
		Produce map[string]int `json:"produce"`
		Persist map[string]int `json:"persist"`
	} `json:"effects"`
	Slots []string `json:"slots"`
}

type checkpoint struct {
	stage  string
	counts map[string]int
	fired  int
	snap   string // the runtime's state there (K3): the record's projection start
}

type sig struct {
	path string
	diff string // the sorted diff, as py's tuple(sorted(diff.items()))
}

type cardMove struct{ card, move string }

// Watch is py's Watch over a Shell.
type Watch struct {
	Sh          *Shell
	Path        string
	Beats       int // env beats before stopping; < 0: none
	RareRatio   float64
	Blind       bool
	Operator    int // beats until an escalation is resumed; < 0: none
	Scope       string
	NoticeAfter int
	Body        string // the bundle's canonical body (ProgramKey)
	Cards       []SideCard

	Tasks   int
	Beat    int
	Seq     int
	Quiet   bool
	Done    bool
	Open    int // the open surprise's record id; -1: none
	Outbox  []Signal
	Results []Signal
	Intents int

	said      map[string]int
	streak    map[cardMove][2]int
	resetSigs map[sig][2]int
	explained map[sig]cardMove
	cp        checkpoint
}

// NewWatch is py's Watch(shell) with its defaults (rare_ratio 0.05, scope
// "order", the repeat notice after 2 orders, no operator, no beat limit).
func NewWatch(sh *Shell, body string, cards []SideCard) (*Watch, error) {
	w := &Watch{Sh: sh, Path: sh.Path, Beats: -1, RareRatio: 0.05, Operator: -1, Scope: "order",
		NoticeAfter: 2, Body: body, Cards: cards, Open: -1,
		said: map[string]int{}, streak: map[cardMove][2]int{}, resetSigs: map[sig][2]int{},
		explained: map[sig]cardMove{}}
	return w, w.checkpoint()
}

// ObserveOnly: a surprise is open (and the arm is not blind).
func (w *Watch) ObserveOnly() bool { return w.Open >= 0 && !w.Blind }

// Step is py Watch.step.
func (w *Watch) Step() ([]Signal, error) {
	sh := w.Sh
	if w.Done {
		return nil, nil
	}
	if !w.ObserveOnly() {
		cs, err := sh.Candidates()
		if err != nil {
			return nil, err
		}
		if len(cs) > 0 {
			w.Quiet = false
			if _, _, err := sh.Sample(); err != nil {
				return nil, err
			}
			out := []Signal{w.rowSignal()}
			rd, err := w.read()
			return append(out, rd...), err
		}
		if !w.Quiet {
			w.Quiet = true
			w.taskDone()
			if sh.Plugin == nil {
				w.Done = true
			}
			return []Signal{w.signal("quiescence", "stage", sh.Stage, "fired", len(sh.Fired),
				"episode_over", sh.Plugin == nil)}, nil
		}
	}
	if sh.Plugin == nil || (w.Beats >= 0 && w.Beat >= w.Beats) {
		w.Done = true
		return []Signal{w.signal("quiescence", "stage", sh.Stage, "fired", len(sh.Fired),
			"episode_over", true, "observe_only", w.ObserveOnly())}, nil
	}
	var out []Signal
	if w.ObserveOnly() {
		rd, err := w.read()
		if err != nil {
			return nil, err
		}
		op, err := w.operator()
		if err != nil {
			return nil, err
		}
		out = append(append(out, rd...), op...)
	}
	name, ok, err := sh.Tick(false)
	if err != nil {
		return nil, err
	}
	if !ok {
		if sh.Stage != Done {
			cs, err := sh.Candidates()
			if err != nil {
				return nil, err
			}
			if len(cs) == 0 {
				if err := sh.LatchDone(); err != nil {
					return nil, err
				}
			}
		}
		w.Done = true
		return append(out, w.signal("quiescence", "stage", sh.Stage, "fired", len(sh.Fired),
			"episode_over", true, "observe_only", w.ObserveOnly())), nil
	}
	w.Beat++
	if name != "" { // input arrived: maybe work again
		w.Quiet = false
		out = append(out, w.rowSignal())
	}
	if !w.ObserveOnly() {
		if name != "" {
			// input arrived: the program answers it first; the reading comes
			// after its row, as for any fired row
			if err := w.checkpoint(); err != nil {
				return nil, err
			}
		} else {
			// nothing arrived, nothing fires: the env moved on its own; a
			// quiet domain is read on every beat
			rd, err := w.read()
			if err != nil {
				return nil, err
			}
			out = append(out, rd...)
		}
	}
	return out, nil
}

func (w *Watch) taskDone() {
	w.Tasks++
	if w.Scope == "order" {
		w.resetSigs = map[sig][2]int{}
		w.explained = map[sig]cardMove{}
	}
}

func (w *Watch) checkpoint() error {
	snap, err := w.Sh.P.Snapshot()
	if err != nil {
		return err
	}
	w.cp = checkpoint{w.Sh.Stage, w.Sh.Counts.Map(), len(w.Sh.Fired), snap}
	return nil
}

// reading is the host's sensor face on the counts now (nil: none).
func (w *Watch) reading() map[string]int {
	if w.Sh.Plugin == nil {
		return nil
	}
	return w.Sh.Plugin.Observe(w.Sh.Counts.Map())
}

func diffOf(reading map[string]int, counts Counts) map[string]int {
	d := map[string]int{}
	for a, n := range reading {
		if m := counts.N[a]; n != m {
			d[a] = n - m
		}
	}
	return d
}

func sameDiff(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

func (w *Watch) read() ([]Signal, error) {
	sh := w.Sh
	reading := w.reading()
	if reading == nil {
		return nil, w.checkpoint()
	}
	model := map[string]int{}
	for a := range reading {
		model[a] = sh.Counts.N[a]
	}
	diff := diffOf(reading, sh.Counts)
	if w.Open >= 0 {
		// keep sensing; speak only when the reading changes
		if sameDiff(diff, w.said) {
			return nil, nil
		}
		w.said = diff
		return []Signal{w.signal("event", "kind", "reading", "consistent", len(diff) == 0, "diff", diff,
			"record", w.Open)}, nil
	}
	if len(diff) == 0 {
		return nil, w.checkpoint()
	}
	rec, err := w.record(reading)
	if err != nil {
		return nil, err
	}
	rec.Meta.Diff = diff
	switch rec.KindOf() {
	case "unmodeled":
		sh.Records = append(sh.Records, rec)
		w.Open = len(sh.Records) - 1
		w.said = diff
		if w.Blind {
			return []Signal{w.signal("event", "kind", "reading", "consistent", false, "diff", diff,
				"observed", reading, "model", model, "record", w.Open)}, nil
		}
		return []Signal{w.signal("surprise", "record", w.Open, "kind", rec.KindOf(),
			"likelihood", rec.Likelihood, "steps", rec.Steps, "observed", reading, "model", model,
			"diff", diff, "observe_only", true)}, nil
	case "rare":
		sh.Records = append(sh.Records, rec)
		return []Signal{w.signal("event", "kind", "rare", "record", len(sh.Records)-1,
			"likelihood", rec.Likelihood, "diff", diff)}, nil
	}
	// another path of the served world explains the reading: a belief
	// change, not a surprise
	return []Signal{w.signal("event", "kind", "reading", "consistent", false,
		"likelihood", rec.Likelihood, "diff", diff)}, nil
}

// record is py's _record: surprise_record from the checkpoint, over the
// events fired since (at least one step).
func (w *Watch) record(reading map[string]int) (*Record, error) {
	sh := w.Sh
	steps := max(1, len(sh.Fired)-w.cp.fired)
	like, err := sh.P.Likelihood(w.cp.snap, steps, reading)
	if err != nil {
		return nil, err
	}
	var kind *string
	if like == 0 {
		k := "unmodeled"
		kind = &k
	} else if like < w.RareRatio {
		k := "rare"
		kind = &k
	}
	var hostSpec *string
	if sh.PluginSpec != "" {
		hostSpec = &sh.PluginSpec
	}
	obs := map[string]int{}
	for a, n := range reading {
		obs[a] = n
	}
	rec := &Record{Format: "metis-surprise", Version: 1,
		ProgramKey: ProgramKey(w.Body, w.cp.stage, w.cp.counts),
		Steps:      steps, Init: w.cp.counts, Observed: obs, Likelihood: like, Kind: kind,
		Meta: Meta{Path: w.Path, Beat: w.Beat, Fired: len(sh.Fired), Stage: w.cp.stage,
			Since: append([]string{}, sh.Fired[w.cp.fired:]...), Host: hostSpec}}
	snap, err := sh.EnvSnap()
	if err != nil {
		return nil, err
	}
	if snap != nil {
		if rec.Env, err = envRecord(snap); err != nil {
			return nil, err
		}
	}
	return rec, nil
}

func (w *Watch) signal(kind string, kv ...any) Signal {
	w.Seq++
	s := Signal{"signal": kind, "seq": w.Seq, "path": w.Path, "beat": w.Beat}
	for i := 0; i+1 < len(kv); i += 2 {
		s[kv[i].(string)] = kv[i+1]
	}
	return s
}

func countMap(cs []rt.Count) map[string]int {
	m := make(map[string]int, len(cs))
	for _, c := range cs {
		m[c.Atom] = c.N
	}
	return m
}

func (w *Watch) rowSignal() Signal {
	sh := w.Sh
	r := sh.Rows[len(sh.Rows)-1]
	kv := []any{"kind", r.Kind, "name", r.Ev.Name, "fired", len(sh.Fired), "stage", sh.Stage}
	if r.Kind == "exo" {
		kv = append(kv, "ops", append([]string{}, r.Ev.Ops...))
	} else {
		kv = append(kv, "consume", countMap(r.Ev.Consume), "produce", countMap(r.Ev.Produce))
	}
	return w.signal("event", kv...)
}

// ── intents down: explain / reset / escalate ──

var watchVerbs = []string{"explain", "reset", "escalate"}

// Intent is py Watch.intent: each intent is consumed exactly once, its
// outcome ONE result signal in the outbox.
func (w *Watch) Intent(line string) map[string]any {
	w.Intents++
	n := w.Intents
	verb, rest, _ := strings.Cut(strings.TrimSpace(line), " ")
	rest = strings.TrimSpace(rest)
	var err error
	switch verb {
	case "explain":
		err = w.intentExplain(n, rest)
	case "reset":
		err = w.intentReset(n, rest)
	case "escalate":
		err = w.intentEscalate(n, rest)
	default:
		v := verb
		if v == "" {
			v = "?"
		}
		w.result(n, v, false, fmt.Sprintf("unknown intent '%s' — one of %s", verb, strings.Join(watchVerbs, ", ")))
	}
	if err != nil { // refused, never a crash
		w.result(n, verb, false, "Error: "+err.Error())
	}
	return map[string]any{"ok": true, "intent": n}
}

func (w *Watch) result(n int, verb string, admitted bool, reason string, kv ...any) {
	status := "refused"
	if admitted {
		status = "admitted"
	}
	s := w.signal("result", append([]any{"intent", n, "verb", verb, "status", status, "reason", reason}, kv...)...)
	w.Outbox = append(w.Outbox, s)
	w.Results = append(w.Results, s)
}

// pyInt is python's int() on a string, closely enough for record ids.
func pyInt(s string) (int, bool) {
	t := strings.ReplaceAll(strings.TrimSpace(s), "_", "")
	if t == "" || strings.HasPrefix(t, "_") {
		return 0, false
	}
	v, err := strconv.Atoi(t)
	return v, err == nil
}

func (w *Watch) openRecord(n int, verb, arg string) int {
	r, ok := pyInt(arg)
	if !ok {
		w.result(n, verb, false, fmt.Sprintf("want a record id, got '%s'", arg))
		return -1
	}
	recs := w.Sh.Records
	if r < 0 || r >= len(recs) {
		w.result(n, verb, false, fmt.Sprintf("no record %d", r))
		return -1
	}
	if r != w.Open {
		reason := fmt.Sprintf("record %d is not open", r)
		if res := recs[r].Resolution; res != nil {
			reason += fmt.Sprintf(" (%v at beat %v)", res["by"], res["beat"])
		}
		w.result(n, verb, false, reason, "record", r)
		return -1
	}
	return r
}

func signature(rec *Record) sig {
	keys := make([]string, 0, len(rec.Meta.Diff))
	for k := range rec.Meta.Diff {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s\x00%d\x00", k, rec.Meta.Diff[k])
	}
	return sig{rec.Meta.Path, b.String()}
}

// verdict is one intent's outcome: applied only when admitted (blind:
// always, the verdict riding along as shadow).
func (w *Watch) verdict(n int, verb string, ok bool, reason string, apply func() error, kv ...any) error {
	if ok || w.Blind {
		if err := apply(); err != nil {
			return err
		}
	}
	if w.Blind {
		shadow := "refused"
		if ok {
			shadow = "admitted"
		}
		s := w.signal("result", append([]any{"intent", n, "verb", verb, "status", "applied", "reason", reason,
			"shadow", shadow}, kv...)...)
		w.Outbox = append(w.Outbox, s)
		w.Results = append(w.Results, s)
		return nil
	}
	w.result(n, verb, ok, reason, kv...)
	return nil
}

func gapWords(diff map[string]int) string {
	keys := make([]string, 0, len(diff))
	for k := range diff {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s %+d", k, diff[k])
	}
	return strings.Join(parts, " ")
}

func (w *Watch) intentReset(n int, arg string) error {
	r := w.openRecord(n, "reset", arg)
	if r < 0 {
		return nil
	}
	rec := w.Sh.Records[r]
	sg := signature(rec)
	if prev, ok := w.resetSigs[sg]; ok {
		text := fmt.Sprintf("the same misreading (%s) recurred after a reset of record %d at beat %d: not a one-off",
			gapWords(rec.Meta.Diff), prev[0], prev[1])
		refuseFact(rec, "reset", text)
		return w.verdict(n, "reset", false, "the same surprise came back after a reset — no more resets; escalate",
			func() error { return w.close(r, "reset") }, "record", r, "mined_text", text)
	}
	return w.verdict(n, "reset", true, "the reading is set aside as noise; resumed on the model — the next reading checks it",
		func() error {
			w.resetSigs[sg] = [2]int{r, w.Beat}
			return w.close(r, "reset")
		}, "record", r)
}

func (w *Watch) intentExplain(n int, arg string) error {
	parts := strings.Fields(arg)
	if len(parts) != 2 && len(parts) != 3 {
		w.result(n, "explain", false, "usage: explain RECORD CARD [MOVE]")
		return nil
	}
	r := w.openRecord(n, "explain", parts[0])
	if r < 0 {
		return nil
	}
	rec := w.Sh.Records[r]
	var card *SideCard
	for i := range w.Cards {
		if w.Cards[i].Name == parts[1] {
			card = &w.Cards[i]
			break
		}
	}
	if card == nil {
		w.result(n, "explain", false, fmt.Sprintf("no card '%s' at this node", parts[1]), "record", r)
		return nil
	}
	var move any // python's None
	mv := ""
	if len(parts) == 3 {
		mv = parts[2]
		move = mv
	}
	since := append([]string{}, rec.Meta.Since...)
	k := rec.Steps
	var adopted map[string]int = map[string]int{}
	apply := func() error {
		a, err := w.adopt(fmt.Sprintf("%s:%s", card.Name, pyStr(move)))
		if err != nil {
			return err
		}
		adopted = a
		return w.close(r, "explain", "card", card.Name, "move", move)
	}
	if before, ok := w.explained[signature(rec)]; ok {
		text := fmt.Sprintf("%s (%s) explained this surprise before and it came back: the served world %s lacks %s's %s",
			before.card, before.move, rec.Meta.Path, before.card, before.move)
		refuseFact(rec, "explain", text)
		return w.verdict(n, "explain", false, fmt.Sprintf("the same surprise came back after %s explained it — the served world is not switched (activate is phase 2); escalate", before.card),
			apply, "record", r, "card", before.card, "move", before.move, "mined_text", text)
	}
	if card.Relation == "served" {
		return w.verdict(n, "explain", false, fmt.Sprintf("the served world is scored at exactly k=%d over its own fired path: the record's own verdict (unmodeled) — it may not invent its own moves", k),
			apply, "record", r, "card", card.Name, "horizon", map[string]int{"served": k})
	}
	if move == nil || !contains(card.Moves, mv) {
		offered := strings.Join(card.Moves, ", ")
		if offered == "" {
			offered = "none"
		}
		return w.verdict(n, "explain", false, fmt.Sprintf("a proposal names one move of %s beside the fired path (%s)", card.Name, offered),
			apply, "record", r, "card", card.Name)
	}
	path := w.witness(card, rec.Init, since, mv, rec.Observed)
	if path == nil {
		return w.verdict(n, "explain", false, fmt.Sprintf("%s with %s does not reach the reading from the record over its fired path %s (k+1=%d)",
			card.Name, mv, pyList(since), k+1), apply, "record", r, "card", card.Name, "move", mv,
			"horizon", map[string]int{"served": k, "card": k + 1})
	}
	var notice string
	admit := func() error {
		w.explained[signature(rec)] = cardMove{card.Name, mv}
		key := cardMove{card.Name, mv}
		last, run := -1, 0
		if x, ok := w.streak[key]; ok {
			last, run = x[0], x[1]
		}
		switch {
		case last == w.Tasks-1:
			run++
		case last == w.Tasks:
		default:
			run = 1
		}
		w.streak[key] = [2]int{w.Tasks, run}
		if w.NoticeAfter > 0 && run >= w.NoticeAfter {
			notice = fmt.Sprintf("%s (%s) explained this diff in %d consecutive orders: the served world %s lacks it; activate is phase 2",
				card.Name, mv, run, w.Path)
			rec.Notice = map[string]any{"card": card.Name, "move": mv, "orders": run}
			rec.MinedText = &notice
		}
		return apply()
	}
	if err := w.verdict(n, "explain", true, fmt.Sprintf("%s explains the reading: %s — the reading is adopted, the served world stays %s (activate is phase 2), resumed",
		card.Name, strings.Join(path, " "), w.Path), admit, "record", r, "card", card.Name, "move", mv,
		"witness", path, "horizon", map[string]int{"served": k, "card": k + 1}, "adopted", adopted); err != nil {
		return err
	}
	// the adopted edit is known only after apply(): patch the signal
	s := w.Outbox[len(w.Outbox)-1]
	s["adopted"] = adopted
	if notice != "" {
		if s["status"] == "admitted" {
			s["status"] = "admitted+notice"
		}
		s["notice"] = rec.Notice
		s["mined_text"] = notice
	}
	return nil
}

func (w *Watch) intentEscalate(n int, arg string) error {
	rid, text, _ := strings.Cut(arg, " ")
	r := w.openRecord(n, "escalate", rid)
	if r < 0 {
		return nil
	}
	text = strings.TrimSpace(text)
	if text == "" {
		w.result(n, "escalate", false, "escalate needs the description (mined_text)", "record", r)
		return nil
	}
	rec := w.Sh.Records[r]
	return w.verdict(n, "escalate", true, "stamped on the record for the revision loop; still observe-only",
		func() error {
			// the runtime's facts FIRST, then the model's description
			mt := strings.Join(append(append([]string{}, rec.Facts...), text), "\n")
			rec.MinedText = &mt
			rec.Escalated = map[string]int{"beat": w.Beat, "facts": len(rec.Facts)}
			return nil
		}, "record", r, "chars", len([]rune(text)))
}

// operator is `watch operator=N`: an escalated surprise still open N beats
// after its escalation is resumed by the operator.
func (w *Watch) operator() ([]Signal, error) {
	if w.Operator < 0 || w.Open < 0 {
		return nil, nil
	}
	rec := w.Sh.Records[w.Open]
	if rec.Escalated == nil || w.Beat-rec.Escalated["beat"] < w.Operator {
		return nil, nil
	}
	r := w.Open
	adopted, err := w.adopt("operator")
	if err != nil {
		return nil, err
	}
	if err := w.close(r, "operator"); err != nil {
		return nil, err
	}
	return []Signal{w.signal("event", "kind", "operator", "record", r, "adopted", adopted, "resumed", true)}, nil
}

// refuseFact keeps a bound's refusal on the record, ahead of whatever an
// escalate describes.
func refuseFact(rec *Record, verb, text string) {
	rec.Refusals = append(rec.Refusals, verb)
	if !contains(rec.Facts, text) {
		rec.Facts = append(rec.Facts, text)
	}
}

func (w *Watch) close(r int, by string, kv ...any) error {
	res := map[string]any{"by": by, "beat": w.Beat}
	for i := 0; i+1 < len(kv); i += 2 {
		res[kv[i].(string)] = kv[i+1]
	}
	w.Sh.Records[r].Resolution = res
	w.Open = -1
	w.said = map[string]int{}
	return w.checkpoint()
}

// adopt takes the host's reading as the state — ONE exo row named for the
// card move that explains it. -> the edit, {} when none.
func (w *Watch) adopt(by string) (map[string]int, error) {
	reading := w.reading()
	if reading == nil {
		return map[string]int{}, nil
	}
	diff := diffOf(reading, w.Sh.Counts)
	if len(diff) == 0 {
		return map[string]int{}, nil
	}
	if _, err := w.Sh.Adopt(by, diff); err != nil {
		return nil, err
	}
	w.Quiet = false
	return diff, nil
}

// witness is the horizon rule's test (py _witness): some interleaving of
// the fired path with ONE move (k+1 events), each enabled where it stands
// in the card's world, reaches the reading on every observed atom. The
// served events come from the rows they fired as; the move from the card.
func (w *Watch) witness(card *SideCard, init map[string]int, since []string, move string, observed map[string]int) []string {
	events := map[string]Ev{}
	for _, r := range w.Sh.Rows {
		if r.Kind == "exo" {
			continue
		}
		if _, ok := events[r.Ev.Name]; !ok {
			events[r.Ev.Name] = evOf(r.Ev)
		}
	}
	for _, e := range card.Effects {
		if e.Name == move {
			if _, ok := events[move]; !ok {
				events[move] = Ev{Name: move, Consume: kvs(e.Consume), Produce: e.Produce, Persist: e.Persist}
			}
		}
	}
	if _, ok := events[move]; !ok {
		return nil
	}
	for _, s := range since {
		if _, ok := events[s]; !ok {
			return nil
		}
	}
	for i := 0; i <= len(since); i++ {
		path := append(append(append([]string{}, since[:i]...), move), since[i:]...)
		counts := map[string]int{}
		for a, n := range init {
			counts[a] = n
		}
		ok := true
		for _, name := range path {
			if counts = applyEv(counts, events[name]); counts == nil {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		hit := true
		for a, m := range observed {
			if counts[a] != m {
				hit = false
				break
			}
		}
		if hit {
			return path
		}
	}
	return nil
}

func kvs(m map[string]int) []KV {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]KV, len(keys))
	for i, k := range keys {
		out[i] = KV{K: k, V: m[k]}
	}
	return out
}

func evOf(e rt.Event) Ev {
	cons := make([]KV, len(e.Consume))
	for i, c := range e.Consume {
		cons[i] = KV{K: c.Atom, V: c.N}
	}
	return Ev{Name: e.Name, Consume: cons, Produce: countMap(e.Produce), Persist: countMap(e.Persist)}
}

// applyEv is py's _apply: fire ev on counts, or nil if it is not enabled.
func applyEv(counts map[string]int, ev Ev) map[string]int {
	need := map[string]int{}
	for _, kv := range ev.Consume {
		need[kv.K] = kv.V.(int)
	}
	for a, m := range ev.Persist {
		need[a] = max(need[a], m)
	}
	for a, m := range need {
		if counts[a] < m {
			return nil
		}
	}
	out := map[string]int{}
	for a, n := range counts {
		out[a] = n
	}
	for _, kv := range ev.Consume {
		out[kv.K] -= kv.V.(int)
	}
	for a, m := range ev.Produce {
		out[a] += m
	}
	for a, n := range out {
		if n <= 0 {
			delete(out, a)
		}
	}
	return out
}

// State is py Watch.state.
func (w *Watch) State() map[string]any {
	var open any
	if w.Open >= 0 {
		open = w.Open
	}
	return map[string]any{"seq": w.Seq, "beat": w.Beat, "done": w.Done, "scope": w.Scope,
		"explain_repeat_notice": w.NoticeAfter, "observe_only": w.ObserveOnly(), "surprise": open}
}

// MarshalJSON keeps a signal's values as data.
func (s Signal) MarshalJSON() ([]byte, error) { return json.Marshal(map[string]any(s)) }
