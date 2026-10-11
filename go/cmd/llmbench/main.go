// llmbench — two-pass latency and intent-class accuracy of the llama.cpp
// adapter on a model: the parity check between Phi-3 and the gemma GGUF
// ollama holds, CPU vs GPU (HARNESS.md §3), and the model-front bench of
// MODEL-EXPERIMENTS.md E1 (Gemma 4 E2B/E4B vs gemma3:4b).
//
//	llmbench -model ~/.ollama/models/blobs/sha256-aeda... -ngl 99 \
//	         -corpus grammar/dog-corpus.tsv                    # gemma, GPU
//	llmbench -model Phi-3-mini-4k-instruct-Q4_K_M.gguf -ngl 0 \
//	         -corpus grammar/dog-corpus.tsv                    # phi3, CPU
//	llmbench -model <gguf> "go to the chair next to the window"
//	llmbench -model gemma-4-E2B-it-Q4_K_M.gguf -corpus grammar/dog-corpus.tsv \
//	         -think -vram -prefix-check -jsonl out.jsonl       # E1 row
//
// With -corpus every row runs and the intent CLASS (not the wording) is
// compared with the expected one — the regression baseline of HARNESS.md
// §7. Without it, the positional arguments are the utterances. Exit 2 when
// a corpus row misses.
//
// Experiment flags (all off by default; the harness is not affected):
//
//	-think          Gemma 4 thinking on (<|think|> in the system turn; pass 1
//	                runs the thought channel before the grammar)
//	-narrate        after each turn, a separate Narrate(utterance) call —
//	                the single-pass narration path, timed and printed
//	-vram           nvidia-smi before load, after prime and after the run
//	                (whole GPU and this process)
//	-prefix-check   the KV-prefix invariant on every row: tokens(prefix) must
//	                be a token-prefix of tokens(prefix+tail) (the tail is
//	                tokenized in context, step-4 F10)
//	-jsonl FILE     append one JSON record per turn and one summary record
//	                (tools/bench-table.py renders the E1 table from them)
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/intent"
	"github.com/metis-lang-dev/metis-agent/go/internal/llm"
)

type row struct{ utt, want, src string }

// turnRec is the per-turn -jsonl record.
type turnRec struct {
	Kind      string `json:"kind"` // "turn"
	Tag       string `json:"tag"`
	Utt       string `json:"utt"`
	Want      string `json:"want,omitempty"`
	Src       string `json:"src,omitempty"`
	Got       string `json:"got"`
	JSON      string `json:"json"`
	Voice     string `json:"voice"`
	Hit       *bool  `json:"hit,omitempty"`
	Unspeak   bool   `json:"unspeakable"`
	Echo      bool   `json:"echo,omitempty"` // voice is the utterance repeated
	MsEval    int64  `json:"ms_eval"`
	MsThink   int64  `json:"ms_think"`
	NThink    int    `json:"n_think"`
	Thought   string `json:"thought,omitempty"`
	MsJSON    int64  `json:"ms_json"`
	MsTTS     int64  `json:"ms_tts"`
	MsTotal   int64  `json:"ms_total"`
	Narrate   string `json:"narrate,omitempty"`
	MsNarrate int64  `json:"ms_narrate,omitempty"`
	NarrBad   string `json:"narrate_bad,omitempty"` // "unspeakable" | "echo"
	Err       string `json:"err,omitempty"`
}

// summaryRec is the -jsonl run record.
type summaryRec struct {
	Kind        string         `json:"kind"` // "summary"
	Tag         string         `json:"tag"`
	Model       string         `json:"model"`
	Arch        string         `json:"arch"`
	Template    string         `json:"template"`
	EOT         string         `json:"eot"`
	Think       bool           `json:"think"`
	Pass2Turn   bool           `json:"pass2_turn,omitempty"`
	NGL         int            `json:"ngl"`
	NCtx        int            `json:"n_ctx"`
	MsLoad      int64          `json:"ms_load"`
	NPrefix     int            `json:"n_prefix"`
	MsPrime     int64          `json:"ms_prime"`
	N           int            `json:"n"`
	MsMean      int64          `json:"ms_mean"`
	MsP50       int64          `json:"ms_p50"`
	MsP90       int64          `json:"ms_p90"`
	MsMax       int64          `json:"ms_max"`
	MsJSONMean  int64          `json:"ms_json_mean"`
	MsTTSMean   int64          `json:"ms_tts_mean"`
	MsThinkMean int64          `json:"ms_think_mean"`
	NThinkMean  int            `json:"n_think_mean"`
	MsNarrMean  int64          `json:"ms_narrate_mean,omitempty"`
	Hit         int            `json:"hit"`
	Scored      int            `json:"scored"`
	BySrc       map[string]int `json:"by_src_hit"`
	BySrcN      map[string]int `json:"by_src_n"`
	Unspeak     int            `json:"unspeakable"`
	Echo        int            `json:"echo"`
	NarrN       int            `json:"narrate_n,omitempty"`
	NarrUnspeak int            `json:"narrate_unspeakable,omitempty"`
	NarrEcho    int            `json:"narrate_echo,omitempty"`
	PrefixOK    *bool          `json:"prefix_ok,omitempty"`
	Prefix      string         `json:"prefix,omitempty"`
	Tail        string         `json:"tail,omitempty"`
	VRAM        map[string]int `json:"vram_mib,omitempty"` // gpu_before, gpu_loaded, gpu_end, proc_loaded, proc_end
	Misses      []string       `json:"misses,omitempty"`
}

func main() {
	model := flag.String("model", "", "GGUF model path")
	promptF := flag.String("prompt", "grammar/dog-prompt.txt", "system prompt file")
	gbnfF := flag.String("gbnf", "grammar/dog.gbnf", "GBNF grammar file")
	corpusF := flag.String("corpus", "", "TSV: utterance<TAB>expected_intent[<TAB>source]")
	tmpl := flag.String("template", "auto", "chat template: auto | gemma | gemma4 | phi3 | ...")
	ngl := flag.Int("ngl", 99, "n_gpu_layers (0 = CPU)")
	nctx := flag.Int("n_ctx", 4096, "context length")
	nbatch := flag.Int("n_batch", 512, "batch size")
	threads := flag.Int("threads", 6, "CPU threads")
	temp := flag.Float64("pass2_temp", 0, "pass-2 temperature (0 = default 0.4)")
	topk := flag.Int("pass2_top_k", 0, "pass-2 top_k (0 = default 40)")
	maxtok := flag.Int("pass2_max_tokens", 0, "pass-2 max tokens (0 = default 50)")
	think := flag.Bool("think", false, "Gemma 4 thinking on (<|think|> in the system turn)")
	thinkMax := flag.Int("think_max", 512, "max thought tokens with -think")
	narrate := flag.Bool("narrate", false, "also run a separate Narrate(utterance) per turn")
	vram := flag.Bool("vram", false, "sample nvidia-smi before load / after prime / after the run")
	prefixCheck := flag.Bool("prefix-check", false, "verify the KV-prefix tokenization invariant on every row")
	jsonlF := flag.String("jsonl", "", "append per-turn + summary JSON records to this file")
	tag := flag.String("tag", "", "run label for -jsonl (default: model basename[+think])")
	pass2Turn := flag.Bool("pass2-turn", false, "pass 2 as its own chat turn instead of the bare \"Spoken response:\" continuation")
	pass2Cue := flag.String("pass2-cue", "", "with -pass2-turn: the user-turn cue text (default \"Spoken response:\")")
	raw := flag.Bool("raw", false, "score the pass-1 text as a call class verb[:domain] (tools/e4-activate), not as a dog intent")
	flag.Parse()
	if *model == "" {
		fail(fmt.Errorf("-model is required"))
	}
	if *tag == "" {
		*tag = strings.TrimSuffix(filepath.Base(*model), ".gguf")
		if *think {
			*tag += "+think"
		}
		if *pass2Turn {
			*tag += "+p2turn"
		}
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
	// a held-out row the prompt now teaches verbatim is no longer held out
	for i, r := range rows {
		if r.src == "heldout" && strings.Contains(string(prompt), fmt.Sprintf("%q →", r.utt)) {
			rows[i].src = "heldout-in-prompt"
		}
	}

	var out *os.File
	if *jsonlF != "" {
		if out, err = os.OpenFile(*jsonlF, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err != nil {
			fail(err)
		}
		defer out.Close()
	}
	emit := func(v any) {
		if out != nil {
			b, _ := json.Marshal(v)
			out.Write(append(b, '\n'))
		}
	}

	sum := summaryRec{Kind: "summary", Tag: *tag, Model: *model, Think: *think, Pass2Turn: *pass2Turn, NGL: *ngl, NCtx: *nctx,
		BySrc: map[string]int{}, BySrcN: map[string]int{}}
	if *vram {
		sum.VRAM = map[string]int{}
		g, _ := gpuMiB()
		sum.VRAM["gpu_before"] = g
		fmt.Fprintf(os.Stderr, "vram before load: gpu %d MiB\n", g)
	}

	t0 := time.Now()
	l, err := llm.Load(llm.Config{ModelPath: *model, NCtx: *nctx, NBatch: *nbatch,
		NThreads: *threads, NGpuLayers: *ngl, Template: *tmpl,
		Pass2:          llm.Pass2{Temp: float32(*temp), TopK: *topk, MaxTokens: *maxtok},
		Think:          *think,
		ThinkMaxTokens: *thinkMax,
		Pass2Turn:      *pass2Turn,
		Pass2Cue:       *pass2Cue})
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
	sum.MsLoad, sum.NPrefix, sum.MsPrime = tLoad.Milliseconds(), n, time.Since(t0).Milliseconds()
	sum.Template, sum.EOT = l.Template(), l.EOT()
	fmt.Fprintf(os.Stderr, "model %s  template=%s eot=%q ngl=%d n_ctx=%d think=%v\nload %dms  prime %d tok in %dms\n",
		*model, l.Template(), l.EOT(), *ngl, *nctx, l.Thinking(), tLoad.Milliseconds(), n,
		time.Since(t0).Milliseconds())
	if *think && !l.Thinking() {
		fmt.Fprintln(os.Stderr, "warning: -think requested but the model has no <|channel> tokens; running without")
	}
	if *vram {
		g, p := gpuMiB()
		sum.VRAM["gpu_loaded"], sum.VRAM["proc_loaded"] = g, p
		fmt.Fprintf(os.Stderr, "vram loaded+primed: gpu %d MiB, this process %d MiB\n", g, p)
	}
	if *prefixCheck {
		prefix, tail, err := l.Split(string(prompt), "hello")
		if err != nil {
			fail(err)
		}
		sum.Prefix, sum.Tail = prefix, tail
		fmt.Fprintf(os.Stderr, "prefix (%d bytes) starts %q … ends %q\ntail %q\n", len(prefix),
			head(prefix, 40), head(prefix[max(0, len(prefix)-40):], 40), tail)
	}

	var lat, latJSON, latTTS, latThink, latNarr []time.Duration
	nThink := 0
	hit, scored, parrot := 0, 0, 0
	prefixOK := true
	for _, r := range rows {
		rec := turnRec{Kind: "turn", Tag: *tag, Utt: r.utt, Want: r.want, Src: r.src}
		if *prefixCheck {
			prefix, tail, err := l.Split(string(prompt), r.utt)
			if err != nil {
				fail(err)
			}
			// the engine tokenizes the tail in context (F10): what must hold
			// is that the cached prefix is a token-prefix of the whole prompt
			pre := l.Tokens(prefix, true)
			whole := l.Tokens(prefix+tail, true)
			if len(whole) <= len(pre) || !equalInt32(pre, whole[:len(pre)]) {
				prefixOK = false
				fmt.Printf("%-40q PREFIX-SPLIT MISMATCH: prefix %d tokens is not a token-prefix of the %d whole\n",
					r.utt, len(pre), len(whole))
			}
		}
		res, err := l.TwoPass(string(prompt), r.utt, string(gbnf))
		if err != nil {
			fmt.Printf("%-40q ERROR %v\n", r.utt, err)
			rec.Err = err.Error()
			if r.want != "" {
				scored++
				sum.BySrcN[r.src]++
			}
			emit(rec)
			continue
		}
		total := res.TJSON + res.TTTS + res.TEval + res.TThink
		lat = append(lat, total)
		latJSON = append(latJSON, res.TJSON)
		latTTS = append(latTTS, res.TTTS)
		latThink = append(latThink, res.TThink)
		nThink += res.NThink
		got, verdict := "?", ""
		if *raw {
			if got = callClass(res.JSON); got == "" {
				got, verdict = "?", "  UNPARSABLE"
			}
		} else if c, err := intent.Parse(res.JSON); err == nil {
			got = c.Intent
		} else {
			verdict = "  UNPARSABLE"
		}
		switch voiceBad(res.Voice, r.utt) {
		case "unspeakable":
			parrot++
			rec.Unspeak = true
			verdict += "  VOICE-UNSPEAKABLE"
		case "echo":
			sum.Echo++
			rec.Echo = true
			verdict += "  VOICE-ECHO"
		}
		if r.want != "" {
			scored++
			sum.BySrcN[r.src]++
			h := got == r.want
			rec.Hit = &h
			if h {
				hit++
				sum.BySrc[r.src]++
			} else {
				verdict += fmt.Sprintf("  MISS want %s", r.want)
				sum.Misses = append(sum.Misses, fmt.Sprintf("%s→%s(want %s)", r.utt, got, r.want))
			}
		}
		thinkCol := ""
		if l.Thinking() {
			thinkCol = fmt.Sprintf("think=%4dms/%3dtok ", res.TThink.Milliseconds(), res.NThink)
		}
		fmt.Printf("%-40q %sjson=%4dms tts=%4dms eval=%3dms  %-12s %s%s\n", r.utt, thinkCol,
			res.TJSON.Milliseconds(), res.TTTS.Milliseconds(), res.TEval.Milliseconds(),
			got, res.JSON, verdict)
		fmt.Printf("%42s voice → %q\n", "", res.Voice)
		if res.Thought != "" {
			fmt.Printf("%42s thought → %q\n", "", head(res.Thought, 160))
		}
		rec.Got, rec.JSON, rec.Voice, rec.Thought, rec.NThink = got, res.JSON, res.Voice, res.Thought, res.NThink
		rec.MsEval, rec.MsThink, rec.MsJSON, rec.MsTTS, rec.MsTotal = res.TEval.Milliseconds(),
			res.TThink.Milliseconds(), res.TJSON.Milliseconds(), res.TTTS.Milliseconds(), total.Milliseconds()
		if *narrate {
			tn := time.Now()
			voice, err := l.Narrate(string(prompt), r.utt)
			d := time.Since(tn)
			latNarr = append(latNarr, d)
			if err != nil {
				voice = "ERROR " + err.Error()
			}
			rec.Narrate, rec.MsNarrate = voice, d.Milliseconds()
			sum.NarrN++
			switch rec.NarrBad = voiceBad(voice, r.utt); rec.NarrBad {
			case "unspeakable":
				sum.NarrUnspeak++
			case "echo":
				sum.NarrEcho++
			}
			fmt.Printf("%42s narrate(%4dms) → %q\n", "", d.Milliseconds(), voice)
		}
		emit(rec)
	}
	if len(lat) == 0 {
		os.Exit(1)
	}
	if *vram {
		g, p := gpuMiB()
		sum.VRAM["gpu_end"], sum.VRAM["proc_end"] = g, p
		fmt.Fprintf(os.Stderr, "vram after run: gpu %d MiB, this process %d MiB\n", g, p)
	}
	if *prefixCheck {
		sum.PrefixOK = &prefixOK
		fmt.Printf("== prefix-check: %s (%d rows)\n", map[bool]string{true: "ok", false: "MISMATCH"}[prefixOK], len(rows))
	}

	fmt.Printf("\n== %s ngl=%d template=%s think=%v: n=%d  turn mean=%dms p50=%dms p90=%dms max=%dms  "+
		"| json mean=%dms | tts mean=%dms | think mean=%dms | unspeakable voice=%d echo=%d\n",
		*model, *ngl, l.Template(), l.Thinking(), len(lat), mean(lat), pct(lat, 50), pct(lat, 90),
		pct(lat, 100), mean(latJSON), mean(latTTS), mean(latThink), parrot, sum.Echo)
	if len(latNarr) > 0 {
		fmt.Printf("== narrate: n=%d mean=%dms unspeakable=%d echo=%d\n",
			sum.NarrN, mean(latNarr), sum.NarrUnspeak, sum.NarrEcho)
	}
	sum.N, sum.MsMean, sum.MsP50, sum.MsP90, sum.MsMax = len(lat), mean(lat), pct(lat, 50), pct(lat, 90), pct(lat, 100)
	sum.MsJSONMean, sum.MsTTSMean, sum.MsThinkMean, sum.NThinkMean = mean(latJSON), mean(latTTS), mean(latThink), nThink/len(lat)
	if len(latNarr) > 0 {
		sum.MsNarrMean = mean(latNarr)
	}
	sum.Hit, sum.Scored, sum.Unspeak = hit, scored, parrot
	if scored > 0 {
		fmt.Printf("== intent class: %d/%d correct", hit, scored)
		keys := make([]string, 0, len(sum.BySrcN))
		for k := range sum.BySrcN {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("  %s=%d/%d", k, sum.BySrc[k], sum.BySrcN[k])
		}
		fmt.Println()
	}
	emit(sum)
	if scored > 0 && hit < scored {
		os.Exit(2)
	}
}

// callClassRe reads a verb and an optional domain out of either rendering of
// the E4 choice: <|tool_call>call:activate{domain:<|"|>kitchen<|"|>}<tool_call|>
// or {"intent":"ACTIVATE","domain":"kitchen"}.
var callClassRe = regexp.MustCompile(`(?i)(activate|deactivate|keep)(?:[^a-z]*domain[^a-z]+([a-z]+))?`)

func callClass(text string) string {
	m := callClassRe.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	c := strings.ToLower(m[1])
	if m[2] != "" {
		c += ":" + strings.ToLower(m[2])
	}
	return c
}

// gpuMiB samples nvidia-smi: whole-GPU used memory and this process's.
// (0, 0) when nvidia-smi is unavailable.
func gpuMiB() (gpu, proc int) {
	if b, err := exec.Command("nvidia-smi", "--query-gpu=memory.used",
		"--format=csv,noheader,nounits").Output(); err == nil {
		gpu, _ = strconv.Atoi(strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0]))
	}
	if b, err := exec.Command("nvidia-smi", "--query-compute-apps=pid,used_memory",
		"--format=csv,noheader,nounits").Output(); err == nil {
		me := strconv.Itoa(os.Getpid())
		for _, ln := range strings.Split(string(b), "\n") {
			p := strings.Split(ln, ",")
			if len(p) == 2 && strings.TrimSpace(p[0]) == me {
				proc, _ = strconv.Atoi(strings.TrimSpace(p[1]))
			}
		}
	}
	return
}

func head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func equalInt32(a, b []int32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func mean(d []time.Duration) int64 {
	if len(d) == 0 {
		return 0
	}
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

// voiceBad classifies a spoken line: "unspeakable" (empty, JSON / markup,
// or a bare intent name like "UNKNOWN" leaking into speech), "echo" (the
// utterance repeated back), or "".
func voiceBad(voice, utt string) string {
	if strings.ContainsAny(voice, "{}[]<>") || strings.TrimSpace(voice) == "" ||
		bareIntent.MatchString(strings.TrimSpace(voice)) {
		return "unspeakable"
	}
	norm := func(s string) string {
		return strings.ToLower(strings.TrimRight(strings.Trim(strings.TrimSpace(s), `"' `), ".!?"))
	}
	if norm(voice) == norm(utt) {
		return "echo"
	}
	return ""
}

var bareIntent = regexp.MustCompile(`^"?[A-Z_]+"?$`)

func fail(err error) {
	fmt.Fprintln(os.Stderr, "llmbench:", err)
	os.Exit(1)
}
