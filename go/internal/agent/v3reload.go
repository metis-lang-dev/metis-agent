package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Reload and the catalog seen on disk (e2e-workflow s-143c3088 step 3,
// WORKFLOW.md stage 6): the backend watches each served domain's
// directory; a change marks the domain in the catalog frame with a
// one-line summary, and reload(domain) has the domain agent recompile the
// program and re-read intent.yaml and the host — a failed compile leaves
// the old domain running and returns the error. The dialog keeps its past;
// the mirror says reloaded.

// watched is what a domain directory holds that a reload takes: the
// program, the intents, the host, the overlay, the manifest.
func watched(name string) bool {
	switch filepath.Ext(name) {
	case ".llp", ".py", ".gbnf":
		return true
	}
	return name == "intent.yaml" || name == "domain.yaml" || strings.HasSuffix(name, ".notch.json") ||
		strings.HasSuffix(name, "-prompt.txt") || strings.HasSuffix(name, "-spoken.txt")
}

// snapshot is the directory's watched files (and intent/'s), by content.
func snapshot(dir string) map[string]string {
	out := map[string]string{}
	for _, d := range []string{dir, filepath.Join(dir, "intent")} {
		es, _ := os.ReadDir(d)
		for _, e := range es {
			if e.IsDir() || !watched(e.Name()) {
				continue
			}
			if b, err := os.ReadFile(filepath.Join(d, e.Name())); err == nil {
				rel, _ := filepath.Rel(dir, filepath.Join(d, e.Name()))
				out[rel] = string(b)
			}
		}
	}
	return out
}

// summary is one line: per changed file its name and its first changed
// line ("dog.llp: %% I have arrived at {P}!").
func summary(base, now map[string]string) string {
	var names []string
	for f := range now {
		if base[f] != now[f] {
			names = append(names, f)
		}
	}
	for f := range base {
		if _, ok := now[f]; !ok {
			names = append(names, f)
		}
	}
	sort.Strings(names)
	var parts []string
	for _, f := range names {
		a, b := strings.Split(base[f], "\n"), strings.Split(now[f], "\n")
		first := ""
		for i := 0; i < max(len(a), len(b)); i++ {
			var x, y string
			if i < len(a) {
				x = a[i]
			}
			if i < len(b) {
				y = b[i]
			}
			if x != y {
				first = strings.TrimSpace(y)
				if first == "" {
					first = "− " + strings.TrimSpace(x)
				}
				break
			}
		}
		if _, ok := now[f]; !ok {
			first = "removed"
		}
		if len(first) > 80 {
			first = first[:77] + "…"
		}
		parts = append(parts, f+": "+first)
	}
	return strings.Join(parts, "; ")
}

// runWatch polls the watched directories every 2 s.
func (v *v3) runWatch(ctx context.Context) {
	ld := v.live
	if len(ld.watch) == 0 {
		return
	}
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		snaps := map[string]map[string]string{}
		for d, dir := range ld.watch {
			snaps[d] = snapshot(dir) // the disk read outside the lock
		}
		v.mu.Lock() // the baseline a reload replaces is read under the reload's lock: no stale mark after it
		moved := false
		for d, snap := range snaps {
			s := summary(ld.baseOf(d), snap)
			if ld.changed[d] != s {
				ld.changed[d], moved = s, true
				if s != "" {
					log.Printf("[v3] %s changed on disk: %s", d, s)
				}
			}
		}
		if moved {
			v.pushCatalog()
		}
		v.mu.Unlock()
	}
}

func (ld *liveDomains) baseOf(d string) map[string]string {
	ld.jmu.Lock()
	defer ld.jmu.Unlock()
	return ld.base[d]
}

// V3Reload is reload(domain) from the page (by: person) or the model.
func (a *Agent) V3Reload(domain, by string) (key string, err error) {
	v := a.v3
	if v == nil || v.live == nil {
		return "", fmt.Errorf("no live domain agents (-loop v3 with v3.domains)")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	ld := v.live
	rec := map[string]any{"kind": "dlg-domain", "verb": "reload", "domain": domain, "by": by}
	defer func() {
		if err != nil {
			rec["error"] = err.Error()
		} else {
			rec["key"] = key
		}
		ld.record(rec)
		v.pushCatalog()
	}()
	p := ld.procs[domain]
	if p == nil {
		return "", fmt.Errorf("no domain agent serves %s", domain)
	}
	var dir string
	if d, ok := ld.watch[domain]; ok {
		dir = d
	}
	before := snapshot(dir)
	rs, err := p.Do(map[string]any{"reload": map[string]any{}, "domain": domain})
	if err != nil {
		return "", err
	}
	ok := false
	for _, r := range ld.sift(rs) {
		switch r["kind"] {
		case "error":
			e, _ := r["error"].(string)
			return "", fmt.Errorf("%s", e) // the old domain runs on
		case "reloaded":
			ok = true
			key, _ = r["key"].(string)
		}
	}
	if !ok {
		return "", fmt.Errorf("%s: the domain agent did not reload (no reloaded reply)", domain)
	}
	// the new runtime does not know the old tasks; the mirror says reloaded
	v.o.m.tasks[domain] = nil
	v.o.m.last[domain] = "reloaded"
	delete(v.o.m.surprise, domain)
	v.o.Activated(domain)
	if dir != "" {
		ld.jmu.Lock()
		ld.base[domain] = before
		ld.jmu.Unlock()
		ld.changed[domain] = ""
	}
	// a regenerated intent grammar is the one the next turn is held to
	if g := a.cfg.GBNFPath; g != "" {
		if b, err := os.ReadFile(g); err == nil && string(b) != v.gbnf {
			v.gbnf = string(b)
			log.Printf("[v3] the intent grammar re-read after the reload (%s)", g)
		}
	}
	return key, nil
}
