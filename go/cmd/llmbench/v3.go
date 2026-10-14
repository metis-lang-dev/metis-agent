package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/agent"
	"github.com/metis-lang-dev/metis-agent/go/internal/llm"
)

// E13 step 2 (MODEL-EXPERIMENTS.md): E13's corpus replayed through the
// orchestrator agent (internal/agent Orch, -loop v3), not the bench's
// script. A scenario (tools/e13-corpus.py --final → grammar/e13-scenarios.jsonl)
// holds only what the domains and the dialog's past give the agent: the
// person's words, the calls made, the generic replies, and the emulated
// domains' answers to the move; the agent renders, branches, cues,
// constrains, guards and templates. The records are -e10's, so
// tools/e11-score.py scores them; with -v3-ruled off the agent is E13 t3's
// configuration and the generations must be step1-t3's, gen for gen.

type v3Event struct {
	Op     string          `json:"op"`
	Person string          `json:"person,omitempty"`
	Said   json.RawMessage `json:"said,omitempty"` // past: the intent
	ID     string          `json:"id,omitempty"`
	Spoken string          `json:"spoken,omitempty"`
	Text   string          `json:"text,omitempty"`
	Call   *agent.Call     `json:"call,omitempty"` // said: a T call
	J      json.RawMessage `json:"j,omitempty"`    // said: a J call
	Reply  *agent.Reply    `json:"reply,omitempty"`
}

type v3Answer struct {
	By    string      `json:"by"` // name | key | canon
	Key   string      `json:"key"`
	Reply agent.Reply `json:"reply"`
}

type v3Scenario struct {
	ID           string     `json:"id"`
	Group        string     `json:"group"`
	Arm          string     `json:"arm"`
	Prompt       string     `json:"prompt"`
	Reps         int        `json:"reps"`
	SampledMoves bool       `json:"sampled_moves"`
	Events       []v3Event  `json:"events"`
	Answers      []v3Answer `json:"answers"`
}

// E13's world: the catalog activate attaches from, the loaded domains, the
// kitchen's sibling card by its display name
var (
	v3Catalog = []string{"kitchen", "nav", "xtraffic", "sysadmin", "social", "votes"}
	v3Loaded  = []string{"dog", "kitchen"}
	v3Display = map[string]string{"kitchen_shared": "second cook"}
)

func readV3(path string) ([]v3Scenario, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []v3Scenario
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		ln := strings.TrimSpace(sc.Text())
		if ln == "" || ln[0] == '#' {
			continue
		}
		var s v3Scenario
		d := json.NewDecoder(strings.NewReader(ln))
		d.UseNumber()
		if err := d.Decode(&s); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out = append(out, s)
	}
	return out, sc.Err()
}

// v3Domains are the emulated domains: the scenario's answers to the move,
// matched by the call's name, key or canonical JSON (the bench's respond).
type v3Domains []v3Answer

func (d v3Domains) match(c agent.Call) (agent.Reply, bool) {
	text := c.Text()
	name, key := agent.CallOf(text)
	by := map[string]string{"name": name, "key": key, "canon": agent.CanonJSON(text)}
	for _, a := range d {
		if by[a.By] == a.Key {
			return a.Reply, true
		}
	}
	return agent.Reply{}, false
}

func (d v3Domains) Call(c agent.Call) (agent.Reply, bool)   { return d.match(c) }
func (d v3Domains) Intent(c agent.Call) (agent.Reply, bool) { return d.match(c) }

func v3Config(ruled bool) agent.V3Config {
	return agent.V3Config{Catalog: v3Catalog, Loaded: v3Loaded, Display: v3Display,
		AskCheck: ruled, AgainPick: ruled, EscalateTemplate: ruled}
}

// replayV3 runs one scenario's events through a fresh orchestrator.
func replayV3(l agent.DialogLLM, s v3Scenario, cfg agent.V3Config) error {
	cfg.SampleMoves = s.SampledMoves
	o := agent.NewOrch(cfg, l, v3Domains(s.Answers))
	for _, e := range s.Events {
		var err error
		switch e.Op {
		case "past":
			err = o.Past(e.Person, e.Said, e.ID, e.Spoken)
		case "person":
			o.Person(e.Text)
		case "context":
			o.Context(e.Text)
		case "wake":
			o.Wake(*e.Reply)
		case "said":
			if len(e.J) > 0 {
				o.Said(agent.Call{J: e.J})
			} else {
				o.Said(*e.Call)
			}
		case "reply":
			_, err = o.Reply(*e.Reply)
		case "spoke":
			o.Spoke(e.Text)
		case "end":
			err = o.End()
		case "move":
			err = o.Move()
		default:
			err = fmt.Errorf("v3: unknown event %q", e.Op)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func runV3(l *llm.LLM, path, ids, tag string, seed uint32, ruled bool, emit func(any)) {
	scen, err := readV3(path)
	if err != nil {
		fail(err)
	}
	idSet := map[string]bool{}
	for _, x := range strings.Split(ids, ",") {
		if x = strings.TrimSpace(x); x != "" {
			idSet[x] = true
		}
	}
	prompts := map[string]string{}
	t0, n := time.Now(), 0
	for _, s := range scen {
		if len(idSet) > 0 && !idSet[s.ID] {
			continue
		}
		p, ok := prompts[s.Prompt]
		if !ok {
			b, err := os.ReadFile(s.Prompt)
			if err != nil {
				fail(err)
			}
			p = string(b)
			if !strings.HasSuffix(p, agent.APIDeclarations(v3Catalog, v3Loaded)) {
				fail(fmt.Errorf("v3: %s does not end with the agent's REPL API declarations", s.Prompt))
			}
			prompts[s.Prompt] = p
		}
		for rep := 0; rep < max(s.Reps, 1); rep++ {
			sd := uint32(0)
			if seed != 0 {
				sd = seed + uint32(rep)
			}
			l.SetSeed(sd)
			if _, err := l.BeginDialog(p); err != nil {
				fail(err)
			}
			cfg := v3Config(ruled)
			since := time.Now()
			cfg.OnGen = func(g agent.GenInfo) {
				now := time.Now()
				first := g.First + max(now.Sub(since)-g.All, 0) // the injected turn's evaluation is part of the wait
				since = now
				emit(e10Gen{Kind: "e10-gen", Tag: tag, ID: s.ID, Group: s.Group, Arm: s.Arm, Rep: rep, Seed: sd,
					Label: g.Label, Try: g.Try, Text: g.Text, Key: g.Key, NCtx: g.NCtx,
					MsFirst: first.Milliseconds(), Ms: g.All.Milliseconds(), Harness: g.Harness})
				if rep == 0 {
					who := ""
					if g.Harness {
						who = " (harness)"
					}
					fmt.Printf("%-14s %-6s %d %5d tok %4dms%s %s\n", s.ID, g.Label, g.Try, g.NCtx, g.All.Milliseconds(), who, g.Text)
				}
			}
			if err := replayV3(l, s, cfg); err != nil {
				emit(e10Gen{Kind: "e10-gen", Tag: tag, ID: s.ID, Group: s.Group, Arm: s.Arm, Rep: rep, Seed: sd,
					Label: "error", Err: err.Error()})
				fmt.Printf("%-14s rep %d ERROR %v\n", s.ID, rep, err)
			}
		}
		n++
	}
	l.SetSeed(0)
	fmt.Fprintf(os.Stderr, "== v3: %d scenarios in %s (ruled %v)\n", n, time.Since(t0).Round(time.Second), ruled)
}
