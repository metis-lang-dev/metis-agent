// Package kernel is the runtime adapter of runtime-wire step 2
// (INTEGRATION.md §2–3): the harness spawns the runtime process
// (`metis --json --host SPEC CATALOG`, metispy), opens one `watch`, and
// reads its signal stream unpolled. Signals become engine events (the
// UI's events pane sees all of them, the dialog narrates the few that
// matter); intents go down as wire ops and their `result` signal comes
// back as the task outcome of a pending command, so the dialog agent
// hears a runtime intent the way it hears a robot command — the
// command/task pair of package wire, with this process as the host.
//
// Wire (one JSON object per line, both ways):
//
//	→ runtime  {"op":"watch","arg":"every=0.5"} · {"op":"intent","arg":"reset 0"} · {"op":"ctx","arg":"--mirror"}
//	← runtime  answers, in op order: {"ok":…} · signals, interleaved: {"signal":"event|quiescence|surprise|result",…}
package kernel

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Config is the `kernel` section of the harness config.
type Config struct {
	Enabled bool     `yaml:"enabled"`
	Cmd     []string `yaml:"cmd"`     // the runtime process (default: python3 -m metis.lang.repl)
	Dir     string   `yaml:"dir"`     // its working directory (metispy)
	Env     []string `yaml:"env"`     // extra KEY=VALUE for it (e.g. METIS_KITCHEN_EPISODE)
	Host    string   `yaml:"host"`    // --host SPEC (the PortPlugin host, e.g. the kitchen)
	Catalog string   `yaml:"catalog"` // the served catalog file
	Watch   string   `yaml:"watch"`   // watch arguments (default "every=0.5")
	// Narrate: which signals the dialog says out loud — "surprise",
	// "consistent" (a surprised reading agrees again), "episode_over".
	// Everything else reaches the UI only. Default: all three.
	Narrate []string `yaml:"narrate"`
	// Triage: the model closes every surprise with one tool call (step 3);
	// unset = on (needs the gemma4 template), false = surprises stay open
	Triage *bool `yaml:"triage"`
	// SettleBeats: how long triage waits for the next reading before it
	// asks the model (beats; the first re-read lands up to two beats after
	// the surprise). Unset = 2.5.
	SettleBeats float64 `yaml:"settle_beats"`
	// Path names the runtime's domain (rack: the tape it feeds); unset =
	// the watch answer's path.
	Path string `yaml:"path"`
	// Display: the domain in the person's words (RAG §9.1), set on its
	// card unless the cards op gives it (kernel.Name).
	Display string `yaml:"display"`
	// Runtimes (rack phase 1b, docs/RAG.md §5): N runtimes attached to the
	// one narrator, each its own process and watch. An entry's set fields
	// override this config's; empty = this config is the one runtime.
	Runtimes []Config `yaml:"runtimes"`
	// Rack: the per-domain tapes and their flat insertion (RAG §2, §10).
	Rack RackConfig `yaml:"rack"`
	// Narr: narration streaming from the script RAG (RAG §11); needs rack.
	Narr NarrConfig `yaml:"narr"`
}

// NarrConfig turns narration streaming on: each runtime's script (the
// `script` op) in the RAG, every fragment narrated as it appears by the
// per-agent narrator (a second context on the same model), then passed to
// the dialog — the orchestrator — in a form.
// Every script sits resident in the narrator's system turn, thinking off
// (RAG §12).
type NarrConfig struct {
	Enabled bool   `yaml:"enabled"`
	Form    string `yaml:"form"`  // policy (default: progress N at arrival, surprise/notice/result R+N, ask N) | N-all | R-all
	NCtx    int    `yaml:"n_ctx"` // the narrator's context (default 4096)
}

// RackConfig turns the rack on: narration and the person's questions are
// answered from the retriever's insertion instead of the bare pieces.
type RackConfig struct {
	Enabled bool  `yaml:"enabled"`
	Budget  int   `yaml:"budget"` // tokens per insertion (default 2500)
	Wrap    *bool `yaml:"wrap"`   // ⟪path: text⟫ fragments (default true, RAG §4)
	Ask     *bool `yaml:"ask"`    // the person's chitchat/unknown turns answered from ask() (default true)
	// K: the last K records of every mounted tape per insertion, in
	// arrival order (RAG §10; default 12)
	K int `yaml:"k"`
}

// Each returns the runtimes to attach: the entries merged over this
// config (set fields win), or this config alone.
func (c Config) Each() []Config {
	if len(c.Runtimes) == 0 {
		one := c
		one.Runtimes = nil
		return []Config{one}
	}
	var out []Config
	for _, r := range c.Runtimes {
		m := c
		m.Runtimes = nil
		if r.Cmd != nil {
			m.Cmd = r.Cmd
		}
		if r.Dir != "" {
			m.Dir = r.Dir
		}
		if r.Env != nil {
			m.Env = r.Env
		}
		if r.Host != "" {
			m.Host = r.Host
		}
		if r.Catalog != "" {
			m.Catalog = r.Catalog
		}
		if r.Watch != "" {
			m.Watch = r.Watch
		}
		if r.Narrate != nil {
			m.Narrate = r.Narrate
		}
		if r.Triage != nil {
			m.Triage = r.Triage
		}
		if r.SettleBeats != 0 {
			m.SettleBeats = r.SettleBeats
		}
		m.Path, m.Display = r.Path, r.Display
		out = append(out, m)
	}
	return out
}

// Signal is one pushed line: its kind, sequence number and the whole object.
type Signal struct {
	Kind string
	Seq  int
	Path string
	F    map[string]any
	Raw  json.RawMessage
}

// Client owns the runtime process: one writer, one reader goroutine.
// Answers come back in op order between the signals, so a FIFO of
// waiters pairs each answer with its op.
type Client struct {
	cmd   *exec.Cmd
	in    io.WriteCloser
	mu    sync.Mutex // serializes writes and the waiter queue
	wait  []chan map[string]any
	done  chan struct{}
	Stats Stats
}

// Stats is the quiet-cost ledger: what the runtime pushed, by kind.
type Stats struct {
	mu      sync.Mutex
	Lines   int
	Bytes   int
	ByKind  map[string]int
	Started time.Time
}

func (s *Stats) add(kind string, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Lines++
	s.Bytes += n
	s.ByKind[kind]++
}

// Snapshot returns lines, bytes and the per-kind counts so far.
func (s *Stats) Snapshot() (int, int, map[string]int, time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := make(map[string]int, len(s.ByKind))
	for k, v := range s.ByKind {
		m[k] = v
	}
	return s.Lines, s.Bytes, m, time.Since(s.Started)
}

// Start spawns the runtime and starts reading it; onSignal runs on the
// reader goroutine for every pushed line, in order.
func Start(ctx context.Context, cfg Config, onSignal func(Signal)) (*Client, error) {
	argv := cfg.Cmd
	if len(argv) == 0 {
		argv = []string{"python3", "-m", "metis.lang.repl"}
	}
	args := append(append([]string{}, argv[1:]...), "--json")
	if cfg.Host != "" {
		args = append(args, "--host", cfg.Host)
	}
	if cfg.Catalog != "" {
		args = append(args, cfg.Catalog)
	}
	cmd := exec.CommandContext(ctx, argv[0], args...)
	cmd.Dir = cfg.Dir
	if len(cfg.Env) > 0 {
		cmd.Env = append(os.Environ(), cfg.Env...)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = logWriter{}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("kernel: start %v: %w", argv, err)
	}
	c := &Client{cmd: cmd, in: in, done: make(chan struct{}),
		Stats: Stats{ByKind: map[string]int{}, Started: time.Now()}}
	go c.read(out, onSignal)
	log.Printf("[kernel] runtime pid %d: %v %v (dir %s)", cmd.Process.Pid, argv[0], args, cfg.Dir)
	return c, nil
}

func (c *Client) read(r io.Reader, onSignal func(Signal)) {
	defer close(c.done)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		line := append([]byte(nil), sc.Bytes()...)
		var f map[string]any
		if err := json.Unmarshal(line, &f); err != nil {
			log.Printf("[kernel] not JSON: %.120s", line)
			continue
		}
		if kind, ok := f["signal"].(string); ok {
			c.Stats.add(kind, len(line)+1)
			seq, _ := f["seq"].(float64)
			path, _ := f["path"].(string)
			if onSignal != nil {
				onSignal(Signal{Kind: kind, Seq: int(seq), Path: path, F: f, Raw: line})
			}
			continue
		}
		c.Stats.add("answer", len(line)+1)
		c.mu.Lock()
		if len(c.wait) == 0 {
			c.mu.Unlock()
			log.Printf("[kernel] unpaired answer: %.160s", line)
			continue
		}
		w := c.wait[0]
		c.wait = c.wait[1:]
		c.mu.Unlock()
		w <- f
	}
	c.mu.Lock()
	for _, w := range c.wait {
		close(w)
	}
	c.wait = nil
	c.mu.Unlock()
	log.Printf("[kernel] runtime stream closed")
}

// Op sends one op and waits for its answer (signals keep flowing meanwhile).
func (c *Client) Op(op, arg string, timeout time.Duration) (map[string]any, error) {
	b, _ := json.Marshal(map[string]string{"op": op, "arg": arg})
	w := make(chan map[string]any, 1)
	c.mu.Lock()
	select {
	case <-c.done:
		c.mu.Unlock()
		return nil, errors.New("kernel: runtime gone")
	default:
	}
	c.wait = append(c.wait, w)
	_, err := c.in.Write(append(b, '\n'))
	c.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("kernel: write %s: %w", op, err)
	}
	select {
	case ans, ok := <-w:
		if !ok {
			return nil, errors.New("kernel: runtime gone before answering " + op)
		}
		return ans, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("kernel: no answer to %s within %s", op, timeout)
	}
}

// Close ends the runtime: stdin closed (the REPL's EOF), then the process.
func (c *Client) Close() error {
	_ = c.in.Close()
	select {
	case <-c.done:
	case <-time.After(2 * time.Second):
		_ = c.cmd.Process.Kill()
	}
	return c.cmd.Wait()
}

type logWriter struct{}

func (logWriter) Write(p []byte) (int, error) {
	log.Printf("[kernel] stderr: %s", bytesTrim(p))
	return len(p), nil
}

func bytesTrim(p []byte) string {
	for len(p) > 0 && (p[len(p)-1] == '\n' || p[len(p)-1] == '\r') {
		p = p[:len(p)-1]
	}
	return string(p)
}
