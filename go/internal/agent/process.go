package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/kernel"
	"github.com/metis-lang-dev/metis-agent/go/internal/narr"
)

// process is the one plugin built (FRAMEWORK §4): the domain runtime as a
// subprocess, `metis --json --host SPEC CATALOG` (metispy) under
// kernel.Client — a rename and a seam over what fsttm-go's startKernel does.
type process struct {
	cfg    DomainConfig
	c      *kernel.Client
	sig    chan kernel.Signal
	cancel context.CancelFunc
}

func init() { Register("process", openProcess) }

func openProcess(cfg DomainConfig) (Domain, error) {
	ctx, cancel := context.WithCancel(context.Background())
	p := &process{cfg: cfg, sig: make(chan kernel.Signal, 1024), cancel: cancel}
	c, err := kernel.Start(ctx, cfg.Kernel(), func(s kernel.Signal) { p.sig <- s })
	if err != nil {
		cancel()
		return nil, err
	}
	p.c = c
	return p, nil
}

func (p *process) Path() string                  { return p.cfg.Name }
func (p *process) Display() string               { return p.cfg.Display }
func (p *process) Signals() <-chan kernel.Signal { return p.sig }

func (p *process) Op(name, arg string, timeout time.Duration) (map[string]any, error) {
	ans, err := p.c.Op(name, arg, timeout)
	if name == "watch" && err == nil && p.cfg.Name == "" {
		if path, ok := ans["path"].(string); ok {
			p.cfg.Name = path // a runtime whose path only the watch names
		}
	}
	return ans, err
}

func (p *process) Intent(line string) (map[string]any, error) {
	return p.c.Op("intent", line, 10*time.Second)
}

func (p *process) Cards() ([]kernel.Card, error) {
	ans, err := p.c.Op("cards", "", 30*time.Second)
	if err != nil {
		return nil, err
	}
	var cs struct {
		Cards []kernel.Card `json:"cards"`
	}
	b, _ := json.Marshal(ans)
	if err := json.Unmarshal(b, &cs); err != nil {
		return nil, fmt.Errorf("cards: %w", err)
	}
	return cs.Cards, nil
}

func (p *process) Script() (*narr.Script, error) {
	ans, err := p.c.Op("script", "", 30*time.Second)
	if err != nil {
		return nil, err
	}
	return narr.Parse(p.cfg.Name, ans)
}

func (p *process) Close() error {
	err := p.c.Close()
	p.cancel()
	return err
}
