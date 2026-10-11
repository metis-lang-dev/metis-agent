// llmbench — two-pass latency and intent-class accuracy of the llama.cpp
// adapter on a model: the parity check between Phi-3 and the gemma GGUF
// ollama holds, CPU vs GPU (HARNESS.md §3).
//
//	llmbench -model ~/.ollama/models/blobs/sha256-aeda... -ngl 99 \
//	         -corpus grammar/dog-corpus.tsv                    # gemma, GPU
//	llmbench -model Phi-3-mini-4k-instruct-Q4_K_M.gguf -ngl 0 \
//	         -corpus grammar/dog-corpus.tsv                    # phi3, CPU
//	llmbench -model <gguf> "go to the chair next to the window"
//
// With -corpus every row runs and the intent CLASS (not the wording) is
// compared with the expected one — the regression baseline of HARNESS.md
// §7. Without it, the positional arguments are the utterances. Exit 2 when
// a corpus row misses.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/intent"
	"github.com/metis-lang-dev/metis-agent/go/internal/llm"
)

type row struct{ utt, want, src string }

func main() {
	model := flag.String("model", "", "GGUF model path")
	promptF := flag.String("prompt", "grammar/dog-prompt.txt", "system prompt file")
	gbnfF := flag.String("gbnf", "grammar/dog.gbnf", "GBNF grammar file")
	corpusF := flag.String("corpus", "", "TSV: utterance<TAB>expected_intent[<TAB>source]")
	tmpl := flag.String("template", "auto", "chat template: auto | gemma | phi3 | ...")
	ngl := flag.Int("ngl", 99, "n_gpu_layers (0 = CPU)")
	nctx := flag.Int("n_ctx", 4096, "context length")
	nbatch := flag.Int("n_batch", 512, "batch size")
	threads := flag.Int("threads", 6, "CPU threads")
	temp := flag.Float64("pass2_temp", 0, "pass-2 temperature (0 = default 0.4)")
	topk := flag.Int("pass2_top_k", 0, "pass-2 top_k (0 = default 40)")
	maxtok := flag.Int("pass2_max_tokens", 0, "pass-2 max tokens (0 = default 50)")
	flag.Parse()
	if *model == "" {
		fail(fmt.Errorf("-model is required"))
	}

	prompt, err := os.ReadFile(*promptF)
	if err != nil {
		fail(err)
	}
	gbnf, err := os.ReadFile(*gbnfF)
	if err != nil {
		fail(err)
	}
	rows := []row{}
	if *corpusF != "" {
		if rows, err = readCorpus(*corpusF); err != nil {
			fail(err)
		}
	}
	for _, u := range flag.Args() {
		rows = append(rows, row{utt: u})
	}
	if len(rows) == 0 {
		fail(fmt.Errorf("nothing to run: -corpus or utterances"))
	}

	t0 := time.Now()
	l, err := llm.Load(llm.Config{ModelPath: *model, NCtx: *nctx, NBatch: *nbatch,
		NThreads: *threads, NGpuLayers: *ngl, Template: *tmpl,
		Pass2: llm.Pass2{Temp: float32(*temp), TopK: *topk, MaxTokens: *maxtok}})
	if err != nil {
		fail(err)
	}
	defer l.Close()
	tLoad := time.Since(t0)
	t0 = time.Now()
	n, err := l.PrimePrefix(string(prompt))
	if err != nil {
		fail(err)
	}
	fmt.Fprintf(os.Stderr, "model %s  template=%s eot=%q ngl=%d n_ctx=%d\nload %dms  prime %d tok in %dms\n",
		*model, l.Template(), l.EOT(), *ngl, *nctx, tLoad.Milliseconds(), n,
		time.Since(t0).Milliseconds())

	var lat, latJSON, latTTS []time.Duration
	hit, scored, parrot := 0, 0, 0
	bySrc := map[string][2]int{}
	for _, r := range rows {
		res, err := l.TwoPass(string(prompt), r.utt, string(gbnf))
		if err != nil {
			fmt.Printf("%-40q ERROR %v\n", r.utt, err)
			if r.want != "" {
				scored++
				s := bySrc[r.src]
				s[1]++
				bySrc[r.src] = s
			}
			continue
		}
		total := res.TJSON + res.TTTS + res.TEval
		lat = append(lat, total)
		latJSON = append(latJSON, res.TJSON)
		latTTS = append(latTTS, res.TTTS)
		got, verdict := "?", ""
		if c, err := intent.Parse(res.JSON); err == nil {
			got = c.Intent
		} else {
			verdict = "  UNPARSABLE"
		}
		if strings.ContainsAny(res.Voice, "{}[]<>") || res.Voice == "" {
			parrot++
			verdict += "  VOICE-UNSPEAKABLE"
		}
		if r.want != "" {
			scored++
			s := bySrc[r.src]
			s[1]++
			if got == r.want {
				hit++
				s[0]++
			} else {
				verdict += fmt.Sprintf("  MISS want %s", r.want)
			}
			bySrc[r.src] = s
		}
		fmt.Printf("%-40q json=%4dms tts=%4dms eval=%3dms  %-12s %s%s\n", r.utt,
			res.TJSON.Milliseconds(), res.TTTS.Milliseconds(), res.TEval.Milliseconds(),
			got, res.JSON, verdict)
		fmt.Printf("%42s voice → %q\n", "", res.Voice)
	}
	if len(lat) == 0 {
		os.Exit(1)
	}
	fmt.Printf("\n== %s ngl=%d template=%s: n=%d  turn mean=%dms p50=%dms p90=%dms max=%dms  "+
		"| json mean=%dms | tts mean=%dms | unspeakable voice=%d\n",
		*model, *ngl, l.Template(), len(lat), mean(lat), pct(lat, 50), pct(lat, 90),
		pct(lat, 100), mean(latJSON), mean(latTTS), parrot)
	if scored > 0 {
		fmt.Printf("== intent class: %d/%d correct", hit, scored)
		keys := make([]string, 0, len(bySrc))
		for k := range bySrc {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			s := bySrc[k]
			fmt.Printf("  %s=%d/%d", k, s[0], s[1])
		}
		fmt.Println()
		if hit < scored {
			os.Exit(2)
		}
	}
}

func mean(d []time.Duration) int64 {
	var sum time.Duration
	for _, x := range d {
		sum += x
	}
	return (sum / time.Duration(len(d))).Milliseconds()
}

func pct(d []time.Duration, p int) int64 {
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	i := len(s) * p / 100
	if i >= len(s) {
		i = len(s) - 1
	}
	return s[i].Milliseconds()
}

func readCorpus(path string) ([]row, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []row
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		ln := strings.TrimSpace(sc.Text())
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		p := strings.Split(ln, "\t")
		if len(p) < 2 {
			return nil, fmt.Errorf("%s: bad row %q", path, ln)
		}
		r := row{utt: p[0], want: p[1], src: "corpus"}
		if len(p) > 2 {
			r.src = p[2]
		}
		out = append(out, r)
	}
	return out, sc.Err()
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "llmbench:", err)
	os.Exit(1)
}
