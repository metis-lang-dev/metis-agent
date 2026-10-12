// episodes — runtime-wire step 5: the harness's triage over seeded kitchen
// episodes, one fresh runtime per episode, per arm (metispy
// metis.domains.kitchen.eval; DESIGN.md "Evaluation"):
//
//	A  `watch arm=blind`: the runtime checks nothing and applies every
//	   intent; the model is woken by the first inconsistent reading and
//	   has no tape — the LLM alone
//	B  the runtime checks (surprise, refusals, bounds); no tape
//	C  the runtime checks, and the tape (step 4): facts and results in
//	   the context, compacted at quiescence. Since RAG §9 (step 5's C ≈ B)
//	   Decide cuts the tape, so C triages as B does
//
//	episodes -episodes ep.jsonl -arms A,B,C -o runs.jsonl -harness harness.jsonl
//	python -m metis.domains.kitchen.eval score --episodes ep.jsonl runs.jsonl --harness harness.jsonl
//
// Per episode it writes {episode, arm, ledger} to -o and {episode, arm,
// wakes, tokens, noticed} to -harness: wakes = surprises handed to the
// model, tokens = tokens the adapter evaluated + generated in the episode.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/kernel"
	"github.com/metis-lang-dev/metis-agent/go/internal/llm"
)

type tapeAdapter struct {
	l   *llm.LLM
	sys string
}

func (t tapeAdapter) Turn(role, text string) string { return t.l.TapeTurn(role, text) }
func (t tapeAdapter) Append(text string) (int, int, time.Duration, error) {
	sp, err := t.l.AppendTape(t.sys, text)
	return sp.Tokens, sp.Tape, sp.Dur, err
}
func (t tapeAdapter) Set(text string) (int, time.Duration, error) {
	sp, err := t.l.SetTape(t.sys, text)
	return sp.Tape, sp.Dur, err
}

func main() {
	model := flag.String("model", "models/gemma4/gemma-4-E2B-it-Q4_K_M.gguf", "GGUF (the harness's E2B Q4)")
	ngl := flag.Int("ngl", 99, "n_gpu_layers")
	prompts := flag.String("prompt", "grammar/dog-prompt.txt,grammar/pass2-examples-gemma4.txt", "system prompt files, concatenated (config.ws.yaml prompt + prompt_extra)")
	epF := flag.String("episodes", "", "episode lines (python -m metis.domains.kitchen.eval episodes)")
	arms := flag.String("arms", "A,B,C", "arms to run")
	dir := flag.String("dir", "../../metispy", "metispy checkout (the runtime's working directory)")
	catalog := flag.String("catalog", "metis/domains/kitchen/catalogs/kitchen_host.llp", "served catalog")
	every := flag.Float64("every", 0.5, "watch beat in seconds")
	settle := flag.Float64("settle-beats", 2.5, "triage waits this many beats for the next reading")
	out := flag.String("o", "runs.jsonl", "ledger lines")
	harness := flag.String("harness", "harness.jsonl", "wakes/tokens lines")
	limit := flag.Int("n", 0, "only the first n episodes (0 = all)")
	flag.Parse()
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	var sys strings.Builder
	for _, f := range strings.Split(*prompts, ",") {
		b, err := os.ReadFile(f)
		if err != nil {
			log.Fatal(err)
		}
		sys.Write(b)
	}
	eps, err := readLines(*epF)
	if err != nil {
		log.Fatal(err)
	}
	if *limit > 0 && *limit < len(eps) {
		eps = eps[:*limit]
	}
	l, err := llm.Load(llm.Config{ModelPath: *model, NCtx: 4096, NBatch: 512, NThreads: 6,
		NGpuLayers: *ngl, Template: llm.TemplateGemma4, Pass2: llm.Pass2{Temp: 0.4, TopK: 40, MaxTokens: 50}})
	if err != nil {
		log.Fatal(err)
	}
	defer l.Close()
	ro, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	defer ro.Close()
	rh, err := os.Create(*harness)
	if err != nil {
		log.Fatal(err)
	}
	defer rh.Close()

	for _, arm := range strings.Split(*arms, ",") {
		for i, line := range eps {
			var ep struct {
				ID       string `json:"id"`
				Kind     string `json:"kind"`
				Beats    int    `json:"beats"`
				Operator int    `json:"operator"`
				Scope    string `json:"scope"`
			}
			if err := json.Unmarshal([]byte(line), &ep); err != nil {
				log.Fatal(err)
			}
			t0 := time.Now()
			ledger, wakes, tokens, noticed, err := run(l, sys.String(), arm, line, ep.Beats, ep.Operator, ep.Scope,
				kernel.Config{Dir: *dir, Catalog: *catalog, Host: "metis.domains.kitchen.host:make_episode",
					Env: []string{"METIS_KITCHEN_EPISODE=" + line}, SettleBeats: *settle,
					Watch: fmt.Sprintf("every=%g", *every)})
			if err != nil {
				log.Printf("episode %s arm %s: %v", ep.ID, arm, err)
				continue
			}
			b, _ := json.Marshal(map[string]any{"episode": ep.ID, "arm": arm, "ledger": ledger})
			fmt.Fprintln(ro, string(b))
			b, _ = json.Marshal(map[string]any{"episode": ep.ID, "arm": arm, "wakes": wakes, "tokens": tokens, "noticed": noticed})
			fmt.Fprintln(rh, string(b))
			log.Printf("[episodes] %s %d/%d %s (%s): wakes %d, tokens %d, noticed %d, %s",
				arm, i+1, len(eps), ep.ID, ep.Kind, wakes, tokens, noticed, time.Since(t0).Round(time.Second))
		}
	}
}

// run serves one episode: a fresh runtime, a fresh tape, the arm's watch,
// triage until the episode is over, then the ledger.
func run(l *llm.LLM, dog, arm, line string, beats, operator int, scope string, kcfg kernel.Config) (map[string]any, int64, int64, int64, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := kernel.NewHost(kcfg, nil)
	h.WakeOnReading = arm == "A"
	over := make(chan struct{}, 1)
	h.OnEvent = func(ev kernel.Event) {
		if ev.Kind == "episode_over" {
			select {
			case over <- struct{}{}:
			default:
			}
		}
	}
	kc, err := kernel.Start(ctx, kcfg, h.Signal)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	defer kc.Close()
	h.C = kc
	ans, err := kc.Op("cards", "", 30*time.Second)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	var cards struct {
		Cards []kernel.Card `json:"cards"`
	}
	b, _ := json.Marshal(ans)
	_ = json.Unmarshal(b, &cards)
	h.Dec, h.LLM = kernel.NewDecider(cards.Cards), l
	h.Prompt = dog + h.Dec.System
	if _, err := l.PrimePrefix(h.Prompt); err != nil {
		return nil, 0, 0, 0, err
	}
	if _, err := l.SetTape(h.Prompt, ""); err != nil { // every episode starts with an empty tape
		return nil, 0, 0, 0, err
	}
	if arm == "C" {
		h.Tape = tapeAdapter{l, h.Prompt}
	}
	d0, g0 := l.TokenCounts() // the episode's own tokens: the one-time prime is not one of them
	w := fmt.Sprintf("%s beats=%d operator=%d scope=%s", kcfg.Watch, beats, operator, scope)
	if arm == "A" {
		w += " arm=blind"
	}
	if a, err := kc.Op("watch", w, 30*time.Second); err != nil || a["ok"] != true {
		return nil, 0, 0, 0, fmt.Errorf("watch %s: %v %v", w, a, err)
	}
	select {
	case <-over:
	case <-time.After(time.Duration(float64(beats)*h.Beat.Seconds()*3+120) * time.Second):
		return nil, 0, 0, 0, fmt.Errorf("no episode_over")
	}
	h.WaitTriage(90 * time.Second)
	led, err := kc.Op("ledger", "", 30*time.Second)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	d1, g1 := l.TokenCounts()
	return led, h.Wakes.Load(), (d1 - d0) + (g1 - g0), h.Noticed.Load(), nil
}

func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if t := strings.TrimSpace(sc.Text()); t != "" {
			out = append(out, t)
		}
	}
	return out, sc.Err()
}
