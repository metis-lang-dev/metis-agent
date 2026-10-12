package agent

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/kernel"
	"github.com/metis-lang-dev/metis-agent/go/internal/narr"
)

// Domain is one domain runtime the agent appeals to (FRAMEWORK §4). The
// catalog describes the domain; the config only picks it: cards and the
// script come from the running domain through its own ops.
type Domain interface {
	Path() string                                                       // the tape, the name on the wire ("" = the watch answer names it)
	Display() string                                                    // in the person's words (RAG §9.1)
	Cards() ([]kernel.Card, error)                                      // the `cards` op
	Script() (*narr.Script, error)                                      // the `script` op
	Signals() <-chan kernel.Signal                                      // the watch's signals
	Intent(line string) (map[string]any, error)                         // the runtime's intent op
	Op(name, arg string, timeout time.Duration) (map[string]any, error) // watch, ctx --mirror, ledger
	Close() error
}

// DomainConfig picks one domain (config.yaml `domains:`, replacing
// kernel.runtimes). Fields left empty take `domain_defaults:` (the
// kernel.Config.Each merge).
type DomainConfig struct {
	Name        string   `yaml:"name"` // the path
	Display     string   `yaml:"display"`
	Plugin      string   `yaml:"plugin"` // "process" (the one built); "remote" later
	Catalog     string   `yaml:"catalog"`
	Host        string   `yaml:"host"`
	Cmd         []string `yaml:"cmd"`
	Dir         string   `yaml:"dir"`
	Env         []string `yaml:"env"`
	Watch       string   `yaml:"watch"`
	Narrate     []string `yaml:"narrate"`
	Triage      *bool    `yaml:"triage"`
	SettleBeats float64  `yaml:"settle_beats"`
}

// Kernel is the domain as the kernel package configures a runtime.
func (c DomainConfig) Kernel() kernel.Config {
	return kernel.Config{Enabled: true, Cmd: c.Cmd, Dir: c.Dir, Env: c.Env, Host: c.Host,
		Catalog: c.Catalog, Watch: c.Watch, Narrate: c.Narrate, Triage: c.Triage,
		SettleBeats: c.SettleBeats, Path: c.Name, Display: c.Display}
}

// Merge returns the domains with defaults filled in, by kernel.Config.Each
// (an entry's set fields win; name and display are the entry's own).
func Merge(defaults DomainConfig, list []DomainConfig) []DomainConfig {
	if len(list) == 0 {
		return nil
	}
	base := defaults.Kernel()
	for _, d := range list {
		base.Runtimes = append(base.Runtimes, d.Kernel())
	}
	var out []DomainConfig
	for i, k := range base.Each() {
		plugin := list[i].Plugin
		if plugin == "" {
			plugin = defaults.Plugin
		}
		if plugin == "" {
			plugin = "process"
		}
		out = append(out, DomainConfig{Name: k.Path, Display: k.Display, Plugin: plugin,
			Catalog: k.Catalog, Host: k.Host, Cmd: k.Cmd, Dir: k.Dir, Env: k.Env, Watch: k.Watch,
			Narrate: k.Narrate, Triage: k.Triage, SettleBeats: k.SettleBeats})
	}
	return out
}

var (
	regMu   sync.Mutex
	plugins = map[string]func(DomainConfig) (Domain, error){}
)

// Register adds a plugin: a constructor by name. A plugin is a Go package
// that calls Register in init (FRAMEWORK §4); "process" is this package's.
func Register(plugin string, mk func(DomainConfig) (Domain, error)) {
	regMu.Lock()
	defer regMu.Unlock()
	plugins[plugin] = mk
}

// Open starts a domain through its plugin.
func Open(cfg DomainConfig) (Domain, error) {
	regMu.Lock()
	mk := plugins[cfg.Plugin]
	regMu.Unlock()
	if mk == nil {
		var known []string
		for k := range plugins {
			known = append(known, k)
		}
		sort.Strings(known)
		return nil, fmt.Errorf("agent: domain %q: no plugin %q (registered: %s)", cfg.Name, cfg.Plugin, strings.Join(known, ", "))
	}
	return mk(cfg)
}

// DomainInfo is one row of the wire's domains{…} (FRAMEWORK §5).
type DomainInfo struct {
	Path    string `json:"path"`
	Display string `json:"display"`
	Plugin  string `json:"plugin"`
	State   string `json:"state"` // attaching | watching | closed | failed: …
}

// DomainsBlock is the intent system turn's account of the loaded domains
// (FRAMEWORK §4): one line per domain — its display name, its path and the
// moves its card admits. A template over Card; "" without domains, so the
// persona alone is the prompt (the parity bar).
func DomainsBlock(cards []kernel.Card) string {
	if len(cards) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Your domains\nLive runtimes you report on and can appeal to; their state reaches you as runtime reports, never as something the person said.\n")
	for _, c := range cards {
		name := c.Display
		if name == "" {
			name = strings.ReplaceAll(c.Name, "_", " ")
		}
		fmt.Fprintf(&b, "- %s (%s)", name, c.Name)
		if c.Text != "" {
			fmt.Fprintf(&b, ": %s", c.Text)
		}
		seen := map[string]bool{}
		var moves []string
		for _, m := range append(append([]kernel.Move{}, c.Moves...), c.Effects...) {
			if m.Name != "" && !seen[m.Name] {
				seen[m.Name] = true
				moves = append(moves, strings.ReplaceAll(m.Name, "_", " "))
			}
		}
		if len(moves) > 0 {
			fmt.Fprintf(&b, "; moves: %s", strings.Join(moves, ", "))
		}
		b.WriteString("\n")
	}
	return b.String()
}
