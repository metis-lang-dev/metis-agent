package llm

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
)

// The voice-gen profile (llama-pin s-aeb12858 step 3), the Go half of
// py/tools/voice_profile.py: the same dialog (the live v3 turn, filler
// exchanges, one user turn asking for a long answer), greedy Gen --reps
// times, each from the user turn decoded afresh (a rollback leaves the last
// generation's logits), per-token ms = Gen's whole time / the tokens it
// sampled. Go's context takes llama.cpp's default flash attention (AUTO).
// Off unless VOICE_PROFILE_OUT names the JSON file:
//
//	VOICE_PROFILE_OUT=go.json VOICE_PROFILE_SYSTEM=turn.txt ./build.sh test -run TestVoiceProfile -count=1 ./internal/llm
func TestVoiceProfile(t *testing.T) {
	out, sysFile := os.Getenv("VOICE_PROFILE_OUT"), os.Getenv("VOICE_PROFILE_SYSTEM")
	if out == "" || sysFile == "" {
		t.Skip("VOICE_PROFILE_OUT / VOICE_PROFILE_SYSTEM not set")
	}
	model := os.Getenv("VOICE_PROFILE_MODEL")
	if model == "" {
		model = e2bPath
	}
	system, err := os.ReadFile(sysFile)
	if err != nil {
		t.Fatal(err)
	}
	const (
		nCtx, reps, maxTokens, filler = 16384, 5, 64, 40
		short                         = "You are Nina, a quadruped robot dog. Answer in the first person."
		ask                           = "Tell me, in about a hundred words, what you did today and what you saw on the way."
	)
	exchange := "<|turn>user\nwhere are you going now?<turn|>\n<|turn>model\nI am walking to the kitchen; " +
		"I will tell you when I arrive.<turn|>\n"
	l, err := Load(Config{ModelPath: model, NCtx: nCtx, NBatch: 512, NThreads: 6, NGpuLayers: 99, Template: "gemma4"})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	run := func(sys string, fill int) map[string]any {
		if _, err := l.BeginDialog(sys); err != nil {
			t.Fatal(err)
		}
		if fill > 0 {
			if _, err := l.Inject(strings.Repeat(exchange, fill)); err != nil {
				t.Fatal(err)
			}
		}
		basePast, err := l.Inject("")
		if err != nil {
			t.Fatal(err)
		}
		var rows []map[string]any
		var per []float64
		mark := basePast
		for i := 0; i < reps; i++ {
			// the user turn decoded again each time: a rollback leaves the last generation's logits
			if err := l.Rollback(basePast); err != nil {
				t.Fatal(err)
			}
			if mark, err = l.Inject("<|turn>user\n" + ask + "<turn|>\n<|turn>model\n"); err != nil {
				t.Fatal(err)
			}
			_, g0 := l.TokenCounts()
			text, _, first, whole, err := l.Gen("", false, maxTokens, "")
			if err != nil {
				t.Fatal(err)
			}
			_, g1 := l.TokenCounts()
			n := g1 - g0
			ms := float64(whole.Microseconds()) / 1e3
			p := ms / float64(max(1, n))
			per = append(per, p)
			if len(text) > 80 {
				text = text[:80]
			}
			rows = append(rows, map[string]any{"tokens": n, "ms": ms, "first_ms": float64(first.Microseconds()) / 1e3,
				"ms_per_token": p, "text": text})
		}
		s := append([]float64(nil), per...)
		sort.Float64s(s)
		return map[string]any{"n_past": mark, "reps": rows, "median_ms_per_token": s[len(s)/2],
			"min_ms_per_token": s[0], "max_ms_per_token": s[len(s)-1]}
	}
	res := map[string]any{"stack": "go", "flash_attn": "default (AUTO)", "n_ctx": nCtx, "max_tokens": maxTokens}
	run(short, 0) // warm-up
	res["short"] = run(short, 0)
	res["long"] = run(string(system), filler)
	for _, k := range []string{"short", "long"} {
		r := res[k].(map[string]any)
		t.Logf("%s n_past %d: %.2f ms/token (min %.2f, max %.2f)", k, r["n_past"], r["median_ms_per_token"],
			r["min_ms_per_token"], r["max_ms_per_token"])
	}
	b, _ := json.MarshalIndent(res, "", " ")
	if err := os.WriteFile(out, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}
