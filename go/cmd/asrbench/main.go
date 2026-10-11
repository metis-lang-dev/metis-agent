// asrbench — the ASR side of MODEL-EXPERIMENTS.md E2: whisper (the harness's
// internal/stt, same model and settings as config) over a WAV corpus with a
// manifest, timed per utterance, scored by WER and — the number that
// matters — the transcript handed on for intent-class scoring: it writes a
// TSV in llmbench's -corpus format (transcript<TAB>intent<TAB>source), so
//
//	asrbench -manifest models/corpus-wav/manifest.tsv -out results/whisper.tsv
//	llmbench -model <gguf> -corpus results/whisper.tsv          # intent after ASR
//
// Other engines run as a command per WAV (-cmd, {} = the WAV path), e.g.
// NeMo-Speech.cpp:
//
//	asrbench -manifest ... -cmd 'nemo-speech transcribe {} --model m.gguf --device cuda:0 --no-warmup -f text'
//
// Command mode times the whole process (load + warmup + decode), so it is
// the WER/transcript path, not the latency path — latency for a streaming
// engine comes from its own bench (nemo-speech bench asr) or the SDK.
//
// -jsonl appends per-utterance and summary records (tools/bench-table.py
// does not read these; they are for the report).
package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/stt"
)

type item struct{ wav, text, want, src string }

type rec struct {
	Kind   string  `json:"kind"`
	Tag    string  `json:"tag"`
	Wav    string  `json:"wav,omitempty"`
	Ref    string  `json:"ref,omitempty"`
	Hyp    string  `json:"hyp,omitempty"`
	WER    float64 `json:"wer"`
	Exact  bool    `json:"exact"`
	Ms     int64   `json:"ms"`
	AudioS float64 `json:"audio_s,omitempty"`
	N      int     `json:"n,omitempty"`
	MsMean int64   `json:"ms_mean,omitempty"`
	MsP50  int64   `json:"ms_p50,omitempty"`
	MsP90  int64   `json:"ms_p90,omitempty"`
	ExactN int     `json:"exact_n,omitempty"`
	RTF    float64 `json:"rtf,omitempty"`
	Err    string  `json:"err,omitempty"`
}

func main() {
	manifest := flag.String("manifest", "models/corpus-wav/manifest.tsv", "wav<TAB>utterance<TAB>intent<TAB>source")
	model := flag.String("model", "/home/axadmin/archive/vox/FSTTM/models/ggml-base.en-q5_1.bin", "whisper ggml model (config stt.model)")
	lang := flag.String("lang", "en", "whisper language")
	threads := flag.Int("threads", 6, "whisper threads")
	cmd := flag.String("cmd", "", "external engine: shell command per WAV, {} = path; stdout = transcript")
	outF := flag.String("out", "", "write transcript TSV for llmbench -corpus")
	jsonlF := flag.String("jsonl", "", "append JSON records")
	tag := flag.String("tag", "", "run label (default: whisper:<model> or cmd)")
	flag.Parse()

	items, err := readManifest(*manifest)
	if err != nil {
		fail(err)
	}
	dir := filepath.Dir(*manifest)

	var transcribe func(wav string) (string, error)
	if *cmd != "" {
		if *tag == "" {
			*tag = "cmd:" + strings.Fields(*cmd)[0]
		}
		transcribe = func(wav string) (string, error) {
			c := exec.Command("sh", "-c", strings.ReplaceAll(*cmd, "{}", wav))
			c.Stderr = nil
			b, err := c.Output()
			return strings.TrimSpace(string(b)), err
		}
	} else {
		if *tag == "" {
			*tag = "whisper:" + strings.TrimSuffix(filepath.Base(*model), ".bin")
		}
		t0 := time.Now()
		s, err := stt.New(*model, *lang, nil, *threads)
		if err != nil {
			fail(err)
		}
		defer s.Close()
		fmt.Fprintf(os.Stderr, "whisper %s loaded+warmed in %dms\n", *model, time.Since(t0).Milliseconds())
		transcribe = func(wav string) (string, error) {
			pcm, err := readWav16k(wav)
			if err != nil {
				return "", err
			}
			r, ok := s.Transcribe(pcm)
			if !ok {
				return "", nil // noise / rejected
			}
			return r.Text, nil
		}
	}

	var out, jl *os.File
	if *outF != "" {
		if out, err = os.Create(*outF); err != nil {
			fail(err)
		}
		defer out.Close()
		fmt.Fprintf(out, "# %s transcripts of %s — utterance<TAB>expected_intent<TAB>source\n", *tag, *manifest)
	}
	if *jsonlF != "" {
		if jl, err = os.OpenFile(*jsonlF, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err != nil {
			fail(err)
		}
		defer jl.Close()
	}
	emit := func(r rec) {
		if jl != nil {
			b, _ := json.Marshal(r)
			jl.Write(append(b, '\n'))
		}
	}

	var lat []time.Duration
	var werSum, audioSum float64
	exact, n := 0, 0
	for _, it := range items {
		wav := filepath.Join(dir, it.wav)
		secs := wavSeconds(wav)
		t0 := time.Now()
		hyp, err := transcribe(wav)
		d := time.Since(t0)
		r := rec{Kind: "utt", Tag: *tag, Wav: it.wav, Ref: it.text, Hyp: hyp, Ms: d.Milliseconds(), AudioS: secs}
		if err != nil {
			r.Err = err.Error()
			fmt.Printf("%-40q ERROR %v\n", it.text, err)
			emit(r)
			continue
		}
		n++
		lat = append(lat, d)
		audioSum += secs
		w := wer(normalize(it.text), normalize(hyp))
		werSum += w
		r.WER = w
		r.Exact = normalize(hyp) == normalize(it.text)
		if r.Exact {
			exact++
		}
		mark := ""
		if !r.Exact {
			mark = fmt.Sprintf("  WER %.2f", w)
		}
		fmt.Printf("%-40q %5dms  → %q%s\n", it.text, d.Milliseconds(), hyp, mark)
		if out != nil {
			t := strings.ReplaceAll(hyp, "\t", " ")
			if t == "" {
				t = "(silence)"
			}
			fmt.Fprintf(out, "%s\t%s\t%s\n", t, it.want, it.src)
		}
		emit(r)
	}
	if n == 0 {
		os.Exit(1)
	}
	sum := rec{Kind: "summary", Tag: *tag, N: n, MsMean: mean(lat), MsP50: pct(lat, 50), MsP90: pct(lat, 90),
		WER: werSum / float64(n), ExactN: exact}
	if audioSum > 0 {
		var total time.Duration
		for _, d := range lat {
			total += d
		}
		sum.RTF = total.Seconds() / audioSum
	}
	fmt.Printf("\n== %s: n=%d  ms mean=%d p50=%d p90=%d  WER mean=%.3f  exact=%d/%d  RTF=%.3f\n",
		*tag, n, sum.MsMean, sum.MsP50, sum.MsP90, sum.WER, exact, n, sum.RTF)
	emit(sum)
}

var punct = regexp.MustCompile(`[^a-z0-9' ]+`)

// normalize lowercases, strips punctuation and collapses spaces; apostrophes
// stay ("what's"). Numbers are NOT spelled out/in: "ninety" ≠ "90" counts as
// an error, which the intent-class score downstream forgives.
func normalize(s string) string {
	s = punct.ReplaceAllString(strings.ToLower(s), " ")
	return strings.Join(strings.Fields(s), " ")
}

// wer is word error rate: Levenshtein over words / reference words.
func wer(ref, hyp string) float64 {
	r, h := strings.Fields(ref), strings.Fields(hyp)
	if len(r) == 0 {
		if len(h) == 0 {
			return 0
		}
		return 1
	}
	prev := make([]int, len(h)+1)
	cur := make([]int, len(h)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(r); i++ {
		cur[0] = i
		for j := 1; j <= len(h); j++ {
			cost := 1
			if r[i-1] == h[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return float64(prev[len(h)]) / float64(len(r))
}

// readWav16k returns the s16le PCM of a 16 kHz mono WAV (data chunk only).
func readWav16k(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) < 44 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, fmt.Errorf("%s: not a WAV", path)
	}
	off := 12
	var rate uint32
	var ch, bits uint16
	for off+8 <= len(b) {
		id := string(b[off : off+4])
		sz := int(binary.LittleEndian.Uint32(b[off+4 : off+8]))
		off += 8
		if off+sz > len(b) {
			sz = len(b) - off
		}
		switch id {
		case "fmt ":
			ch = binary.LittleEndian.Uint16(b[off+2:])
			rate = binary.LittleEndian.Uint32(b[off+4:])
			bits = binary.LittleEndian.Uint16(b[off+14:])
		case "data":
			if rate != 16000 || ch != 1 || bits != 16 {
				return nil, fmt.Errorf("%s: want 16 kHz mono s16le, got %d Hz %d ch %d bit", path, rate, ch, bits)
			}
			return b[off : off+sz], nil
		}
		off += sz + sz%2
	}
	return nil, fmt.Errorf("%s: no data chunk", path)
}

func wavSeconds(path string) float64 {
	pcm, err := readWav16k(path)
	if err != nil {
		return 0
	}
	return float64(len(pcm)/2) / 16000
}

func readManifest(path string) ([]item, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []item
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
		it := item{wav: p[0], text: p[1], src: "corpus"}
		if len(p) > 2 {
			it.want = p[2]
		}
		if len(p) > 3 {
			it.src = p[3]
		}
		out = append(out, it)
	}
	return out, sc.Err()
}

func mean(d []time.Duration) int64 {
	if len(d) == 0 {
		return 0
	}
	var s time.Duration
	for _, x := range d {
		s += x
	}
	return (s / time.Duration(len(d))).Milliseconds()
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

func fail(err error) {
	fmt.Fprintln(os.Stderr, "asrbench:", err)
	os.Exit(1)
}
