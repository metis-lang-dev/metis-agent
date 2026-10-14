package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// DomainProc is a real domain agent behind the orchestrator (integration
// step 1, s-f82a578f): py/metis_agent/domain_agent.py spawned as a
// long-lived process, the domain agent's JSON lines on its stdin/stdout —
// the wire internal/domemu emulates. A request is one line; its replies
// come back until the request's ack ({"ack": kind} after an intent, a
// tool, a world line or a reset; {"clock": "beat", …} after a beat).
// Every request is timed: the wire's share of a turn's latency.
type DomainProc struct {
	Name string

	mu   sync.Mutex
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  *bufio.Scanner
	Took []Took // every request, in order
	// OnTook, when set, sees each request's round trip (the live journal's dlg-wire)
	OnTook func(Took)
}

// Took is one request's round trip.
type Took struct {
	Kind    string
	Replies int
	D       time.Duration
}

// StartDomain spawns the domain agent: argv[0] with argv[1:], its stderr
// to logw (nil: discarded); it waits for the agent's ready line.
func StartDomain(name string, argv []string, dir string, env []string, logw io.Writer) (*DomainProc, error) {
	c := exec.Command(argv[0], argv[1:]...)
	c.Dir = dir
	c.Env = append(os.Environ(), env...)
	if logw != nil {
		c.Stderr = logw
	}
	in, err := c.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := c.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := c.Start(); err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	p := &DomainProc{Name: name, cmd: c, in: in, out: sc}
	if _, err := p.readUntil(func(m map[string]any) bool { return m["clock"] == "ready" }); err != nil {
		_ = p.Close()
		return nil, fmt.Errorf("domain %s: no ready line: %w", name, err)
	}
	return p, nil
}

// Pid is the process's id (for its VRAM).
func (p *DomainProc) Pid() int { return p.cmd.Process.Pid }

func (p *DomainProc) readUntil(last func(map[string]any) bool) ([]map[string]any, error) {
	var out []map[string]any
	for p.out.Scan() {
		ln := strings.TrimSpace(p.out.Text())
		if ln == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			continue // a stray print on stdout is not a reply
		}
		if last(m) {
			return out, nil
		}
		out = append(out, m)
	}
	if err := p.out.Err(); err != nil {
		return out, err
	}
	return out, io.EOF
}

// Do sends one request and returns its replies (the ack line left out).
func (p *DomainProc) Do(req map[string]any) ([]map[string]any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	kind := "intent"
	switch {
	case req["clock"] != nil:
		kind = "clock"
	case req["tool"] != nil:
		kind = "tool"
	case req["world"] != nil:
		kind = "world"
	case req["reset"] != nil:
		kind = "reset"
	case req["ports"] != nil:
		kind = "ports"
	}
	t0 := time.Now()
	if _, err := p.in.Write(append(b, '\n')); err != nil {
		return nil, err
	}
	out, err := p.readUntil(func(m map[string]any) bool {
		switch kind {
		case "clock":
			return m["clock"] != nil
		case "reset": // the ready line, then the reset's ack: read to the ack, or the next request reads it as its own
			return m["ack"] != nil
		}
		return m["ack"] != nil
	})
	tk := Took{Kind: kind, Replies: len(out), D: time.Since(t0)}
	p.Took = append(p.Took, tk)
	if p.OnTook != nil {
		p.OnTook(tk)
	}
	return out, err
}

// Intent sends a typed intent {id, domain, verb, args}.
func (p *DomainProc) Intent(id, verb string, args map[string]any) ([]map[string]any, error) {
	return p.Do(map[string]any{"id": id, "domain": p.Name, "verb": verb, "args": args})
}

// Tool sends a REPL call {tool, args, id} to this process's domain.
func (p *DomainProc) Tool(tool string, args map[string]any, id string) ([]map[string]any, error) {
	return p.ToolTo(p.Name, tool, args, id)
}

// ToolTo sends a REPL call to one domain of a several-domain process
// (integration step 2: dog, kitchen, kitchen_shared on one model).
func (p *DomainProc) ToolTo(domain, tool string, args map[string]any, id string) ([]map[string]any, error) {
	req := map[string]any{"tool": tool, "args": args, "domain": domain}
	if id != "" {
		req["id"] = id
	}
	return p.Do(req)
}

// Beat steps the domain's clock one beat; what fell due comes back.
func (p *DomainProc) Beat() ([]map[string]any, error) {
	return p.Do(map[string]any{"clock": "beat"})
}

// BeatOf steps one domain's clock: n env beats of a pair (its cooks
// interleaved inside the process), one beat of a single domain.
func (p *DomainProc) BeatOf(domain string, n int) ([]map[string]any, error) {
	return p.Do(map[string]any{"clock": "beat", "domain": domain, "n": max(n, 1)})
}

// World changes the domain's world on its next tick (the timeline's
// person acting: a door opened).
func (p *DomainProc) World(lines []string) error {
	_, err := p.Do(map[string]any{"world": lines, "domain": p.Name})
	return err
}

// Reset gives the domain a fresh world (kernel, host, beat 0), the model
// kept.
func (p *DomainProc) Reset(args map[string]any) error {
	_, err := p.Do(map[string]any{"reset": args})
	return err
}

// Close ends the process (its stdin closed, then a wait).
func (p *DomainProc) Close() error {
	_ = p.in.Close()
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		_ = p.cmd.Process.Kill()
		return <-done
	}
}
