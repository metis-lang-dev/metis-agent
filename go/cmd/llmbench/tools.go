package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/llm"
)

// E9 (MODEL-EXPERIMENTS.md): the dog intents as Gemma 4 native tool calls
// against the JSON two-pass, on the -corpus rows. One greedy pass 1 per row
// and arm, then each pass-2 rendering -tools-reps times with the seed
// seed+rep (RAG §12.7):
//
//	J       pass 1: JSON under dog.gbnf, the harness's prompt (dog prompt +
//	        the gemma4 pass-2 examples); pass 2 "J": the bare "\nSpoken
//	        response:" after the JSON, as TwoPass does
//	J-free  pass 1: J's prompt, no grammar; parsed after (the grammar's
//	        cost apart from the format's)
//	T       pass 1: one native call under dog-tools.gbnf, the T prompt;
//	        pass 2 "T-rewind": the bare cue after the call, as J
//	T-rt    pass 1: as T under the T-rt prompt (its pass-2 examples are
//	        round-trips); pass 2 "T-rt": the call stays, a <|tool_response>
//	        carries {status:<-tools-rt-status>}, the model speaks on in its
//	        turn. (Step 1's first run put T-rt after T's own prompt with
//	        {status:ok}: the prompt's bare-cue convention leaked into every
//	        line — results/e9/run1.)
//	T-free  pass 1: the T prompt, no grammar; parsed after (e9-score.py)
//	T-noex  pass 1: T without the intent examples (read 4)
//	T-think pass 1: T with the thought channel (read 6; needs -think)
//
// tools/e9-score.py maps every arm through one function and scores it
// against the harness's reference answers.

type e9Call struct {
	Kind     string `json:"kind"` // "e9-call"
	Tag      string `json:"tag"`
	Arm      string `json:"arm"`
	Utt      string `json:"utt"`
	Want     string `json:"want"`
	Src      string `json:"src"`
	Text     string `json:"text"`
	MsIntent int64  `json:"ms_intent"`
	NPrefix  int    `json:"n_prefix"`
	Err      string `json:"err,omitempty"`
}

type e9Voice struct {
	Kind    string `json:"kind"` // "e9-voice"
	Tag     string `json:"tag"`
	Arm     string `json:"arm"` // J | T-rewind | T-rt
	Utt     string `json:"utt"`
	Rep     int    `json:"rep"`
	Seed    uint32 `json:"seed"`
	Voice   string `json:"voice"`
	MsFirst int64  `json:"ms_first"`
	MsVoice int64  `json:"ms_voice"`
	NCont   int    `json:"n_cont"`
	Err     string `json:"err,omitempty"`
}

var callName = regexp.MustCompile(`<\|tool_call>call:([A-Za-z_]+)\{`)

// rtResponse is the dispatcher's acknowledgement as the template's
// format_tool_response_block renders a mapping.
func rtResponse(call, status string) string {
	name := "unknown"
	if m := callName.FindStringSubmatch(call); m != nil {
		name = m[1]
	}
	return `<|tool_response>response:` + name + `{status:<|"|>` + status + `<|"|>}<tool_response|>`
}

func readPrompt(list string) (string, error) {
	var b strings.Builder
	for _, f := range strings.Split(list, ",") {
		x, err := os.ReadFile(strings.TrimSpace(f))
		if err != nil {
			return "", err
		}
		b.Write(x) // byte for byte, as the harness's prompt + prompt_extra
	}
	return b.String(), nil
}

func runTools(l *llm.LLM, rows []row, arms, jPrompt, jGbnf, tPrompt, tNoex, tRT, tGbnf, rtStatus, tag string, reps int, seed uint32, emit func(any)) {
	type arm struct {
		name, prompt, gbnf string
		pass2              []string // pass-2 modes after this pass 1
		max                int
	}
	load := func(p string) string {
		s, err := readPrompt(p)
		if err != nil {
			fail(err)
		}
		return s
	}
	jg, tg := load(jGbnf), load(tGbnf)
	all := map[string]arm{
		"J":       {"J", load(jPrompt), jg, []string{"J"}, 80},
		"J-free":  {"J-free", load(jPrompt), "", nil, 160},
		"T":       {"T", load(tPrompt), tg, []string{"T-rewind"}, 120},
		"T-rt":    {"T-rt", load(tRT), tg, []string{"T-rt"}, 120},
		"T-free":  {"T-free", load(tPrompt), "", nil, 160},
		"T-noex":  {"T-noex", load(tNoex), tg, nil, 120},
		"T-think": {"T-think", load(tPrompt), tg, nil, 120},
	}
	if reps < 1 {
		reps = 1
	}
	for _, name := range strings.Split(arms, ",") {
		a, ok := all[strings.TrimSpace(name)]
		if !ok {
			fail(fmt.Errorf("-tools: unknown arm %q (J, J-free, T, T-rt, T-free, T-noex, T-think)", name))
		}
		if a.name == "T-think" && !l.Thinking() {
			fail(fmt.Errorf("-tools T-think needs -think"))
		}
		t0 := time.Now()
		n, err := l.PrimePrefix(a.prompt)
		if err != nil {
			fail(err)
		}
		fmt.Fprintf(os.Stderr, "== %s: prefix %d tok primed in %dms\n", a.name, n, time.Since(t0).Milliseconds())
		for _, r := range rows {
			text, mark, d, err := l.Propose(a.prompt, r.utt, a.gbnf, a.max)
			rec := e9Call{Kind: "e9-call", Tag: tag, Arm: a.name, Utt: r.utt, Want: r.want, Src: r.src,
				Text: text, MsIntent: d.Milliseconds(), NPrefix: n}
			if err != nil {
				rec.Err = err.Error()
				emit(rec)
				fmt.Printf("%-8s %-36q ERROR %v\n", a.name, r.utt, err)
				continue
			}
			emit(rec)
			fmt.Printf("%-8s %-36q %4dms %s\n", a.name, r.utt, d.Milliseconds(), text)
			for _, mode := range a.pass2 {
				cont := "\nSpoken response:"
				if mode == "T-rt" {
					cont = rtResponse(text, rtStatus)
				}
				for rep := 0; rep < reps; rep++ {
					s := uint32(0)
					if seed != 0 {
						s = seed + uint32(rep)
					}
					l.SetSeed(s)
					v, first, all, nc, err := l.SpeakFrom(mark, cont)
					vr := e9Voice{Kind: "e9-voice", Tag: tag, Arm: mode, Utt: r.utt, Rep: rep, Seed: s,
						Voice: v, MsFirst: first.Milliseconds(), MsVoice: all.Milliseconds(), NCont: nc}
					if err != nil {
						vr.Err = err.Error()
					}
					emit(vr)
					if rep == 0 {
						fmt.Printf("%42s %-8s %4dms → %q\n", "", mode, first.Milliseconds(), v)
					}
				}
			}
		}
	}
	l.SetSeed(0)
}
