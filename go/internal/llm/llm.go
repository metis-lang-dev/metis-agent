// Package llm is a thin cgo shim over the CURRENT llama.cpp C API,
// implementing exactly what fsttm/two_pass.py does: KV-prefix reuse + two-pass
// grammar-constrained intent generation. The chat template and the
// end-of-turn token come from the model (GGUF metadata, else llama.cpp's
// built-in template for the architecture), so the same adapter serves
// Phi-3 and the gemma GGUF ollama holds (HARNESS.md §3).
//
// Why not go-llama.cpp: it is frozen at llama.cpp March-2024 (no Phi-3
// architecture, pre-sampler-chain API, no llama_memory_* ops) — not on par.
// The modern C API needs only ~a dozen calls, all cgo-friendly.
//
// Build: see go/build.sh — CGO_CFLAGS/-LDFLAGS point at a llama.cpp checkout
// built with -DGGML_CUDA=ON (or CPU-only on the Orin fallback profile).
package llm

/*
#cgo LDFLAGS: -lllama -lggml -lggml-base
#include <stdio.h>
#include <stdlib.h>
#include <llama.h>

// batch helper: single-token decode without repeated cgo slice plumbing
static int decode_one(struct llama_context *ctx, llama_token t) {
    return llama_decode(ctx, llama_batch_get_one(&t, 1));
}

// quiet log sink: drop everything below warnings (llama.cpp logs every
// tensor load at debug level otherwise)
static void quiet_log(enum ggml_log_level level, const char *text, void *ud) {
    (void)ud;
    if (level >= GGML_LOG_LEVEL_WARN) fputs(text, stderr);
}
static void install_quiet_log(void) { llama_log_set(quiet_log, NULL); }
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// LLM owns one model + context. All calls are serialized by mu — mirrors the
// single llama worker thread in the Python engine (llama.cpp contexts are not
// thread-safe).
type LLM struct {
	mu    sync.Mutex
	model *C.struct_llama_model
	ctx   *C.struct_llama_context
	vocab *C.struct_llama_vocab

	nCtx    int
	nBatch  int
	prefix  string // system prompt currently cached in the KV
	nPrefix int    // token count of the cached prefix
	nPast   int    // tokens currently in the KV (position of next token)

	tmpl    string // resolved chat template (built-in name or Jinja text)
	tailFmt string // rendered user turn + assistant cue, with userSentinel
	preText string // the rendered prefix the KV holds
	preToks []C.llama_token
	// the tape (runtime-wire step 4): whole turns appended after the prefix
	// and kept in the KV between calls — runtime facts, results, spoken
	// sentences. A turn rolls back to prefix+tape, never into it; only
	// SetTape (compaction) rewrites it.
	tapeText   string
	tapeToks   []C.llama_token
	tapeOut    bool          // a Decide cut the tape from the KV (triage on B's context); the next turn decodes it again
	firstTok   time.Duration // generate(): time to the first sampled token
	nDecoded   atomic.Int64
	nGenerated atomic.Int64
	eotText    string // end-of-turn marker text, stripped from decoded output
	pass2      Pass2
	stopIDs    map[C.llama_token]bool // end-of-turn / end-of-sentence ids

	// Gemma 4 thinking (MODEL-EXPERIMENTS.md E1): <|think|> in the system
	// turn, the model answers <|channel>thought…<channel|> first. Off unless
	// Config.Think; chanOpen/chanClose are -1 when the vocab has no channel.
	think     bool
	thinkMax  int
	chanOpen  C.llama_token
	chanClose C.llama_token
	pass2Turn bool
	pass2Cue  string
	// libmtmd context (audio.go, -tags mtmd): nil unless AttachAudio ran
	mtmd      unsafe.Pointer
	mtmdClose func()
	cfg       Config
	sibling   bool // a second context on another LLM's model (Sibling): Close keeps the model
}

type Config struct {
	ModelPath  string
	NCtx       int
	NBatch     int
	NThreads   int
	NGpuLayers int
	// Template: "" or "auto" → the GGUF's tokenizer.chat_template, else
	// llama.cpp's built-in template for the architecture (gemma3 → gemma,
	// phi3 → phi3, …); or a built-in name / Jinja text to force one.
	// "gemma4" is rendered here (TemplateGemma4): the Gemma 4 turn tokens
	// are not known to llama_chat_apply_template on this checkout.
	Template string
	Pass2    Pass2 // spoken-ack sampling; zero fields take the defaults
	// Think switches Gemma 4 thinking on (a <|think|> token opens the
	// system turn); pass 1 then runs the thought channel unconstrained
	// before the grammar. ThinkMaxTokens caps the thought (0 = 512).
	// Ignored by models without <|channel> tokens. Experiments only.
	Think          bool
	ThinkMaxTokens int
	// Pass2Turn renders the spoken-ack cue as its own chat turn (close the
	// model turn, a user turn "Spoken response:", a new model turn) instead
	// of the bare "\nSpoken response:" continuation Phi-3/Gemma 3 follow.
	// Gemma 4 echoes the JSON after the bare cue (E1). Experiments only.
	Pass2Turn bool
	// Pass2Cue is the user-turn text of Pass2Turn ("" = "Spoken response:").
	// Gemma 4 obeys the system role's "only the JSON" and answers a bare cue
	// with more JSON, so its cue has to be an explicit instruction. With
	// Pass2Turn, Narrate asks the same cue after the event in one user turn.
	Pass2Cue string
}

// TemplateGemma4 names the Go-side renderer of the Gemma 4 format
// (ai.google.dev/gemma/docs/core/prompt-formatting-gemma4):
//
//	<|turn>system\n{system}<turn|>\n<|turn>user\n{user}<turn|>\n<|turn>model\n
//
// Native system role: the KV-cached prefix is exactly the system turn.
const TemplateGemma4 = "gemma4"

// Pass2 sampling for the spoken acknowledgment (pass 1 is always greedy
// under the grammar). Defaults are the Phi-3 tuning: temp 0.4, top_k 40,
// 50 tokens; llmbench re-tunes them per model.
type Pass2 struct {
	Temp      float32
	TopK      int
	MaxTokens int
}

func (p Pass2) withDefaults() Pass2 {
	if p.Temp <= 0 {
		p.Temp = 0.4
	}
	if p.TopK <= 0 {
		p.TopK = 40
	}
	if p.MaxTokens <= 0 {
		p.MaxTokens = 50
	}
	return p
}

// userSentinel marks the user text inside a rendered chat so the constant
// prefix (everything before the user turn) and the per-turn tail can be
// split without knowing the template.
const userSentinel = "\x01FSTTM_USER\x01"

// Result of one two-pass turn.
type Result struct {
	JSON  string        // pass-1 grammar-constrained JSON text
	Voice string        // pass-2 spoken acknowledgment
	TJSON time.Duration // pass-1 generation time
	TTTS  time.Duration // pass-2 generation time
	TEval time.Duration // tail eval time (prefix reuse path)
	// TEncode: the media encoder's share of TEval (TwoPassAudio); 0 for text.
	TEncode time.Duration
	// Thinking (Config.Think): the thought channel text and its time;
	// empty / 0 when the model opened no channel.
	Thought string
	TThink  time.Duration
	NThink  int // thought tokens
}

func Load(cfg Config) (*LLM, error) {
	C.install_quiet_log()
	C.llama_backend_init()

	cPath := C.CString(cfg.ModelPath)
	defer C.free(unsafe.Pointer(cPath))

	mp := C.llama_model_default_params()
	mp.n_gpu_layers = C.int32_t(cfg.NGpuLayers)
	model := C.llama_model_load_from_file(cPath, mp)
	if model == nil {
		return nil, fmt.Errorf("llm: cannot load model %s", cfg.ModelPath)
	}

	ctx := newContext(model, cfg)
	if ctx == nil {
		C.llama_model_free(model)
		return nil, errors.New("llm: cannot create context")
	}
	l, err := withContext(model, ctx, cfg)
	if err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

func newContext(model *C.struct_llama_model, cfg Config) *C.struct_llama_context {
	cp := C.llama_context_default_params()
	cp.n_ctx = C.uint32_t(cfg.NCtx)
	// sliding-window models (gemma3): a reduced SWA cache cannot remove a
	// partial sequence, and the per-turn rewind below IS a partial removal
	cp.swa_full = C.bool(true)
	cp.n_batch = C.uint32_t(cfg.NBatch)
	cp.n_threads = C.int32_t(cfg.NThreads)
	cp.n_threads_batch = C.int32_t(cfg.NThreads)
	return C.llama_init_from_model(model, cp)
}

// Sibling is a second context on this LLM's model (the weights loaded
// once): its own KV, prefix and tape, its own lock. RAG §11: the agent
// narrators' prefix next to the orchestrator's, so neither re-primes the
// other. nCtx 0 = this one's. Close frees the context, not the model.
func (l *LLM) Sibling(nCtx int) (*LLM, error) {
	cfg := l.cfg
	if nCtx > 0 {
		cfg.NCtx = nCtx
	}
	ctx := newContext(l.model, cfg)
	if ctx == nil {
		return nil, errors.New("llm: cannot create a sibling context")
	}
	s, err := withContext(l.model, ctx, cfg)
	if s != nil {
		s.sibling = true
	}
	if err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func withContext(model *C.struct_llama_model, ctx *C.struct_llama_context, cfg Config) (*LLM, error) {
	l := &LLM{
		model: model, ctx: ctx, cfg: cfg,
		vocab:  C.llama_model_get_vocab(model),
		nCtx:   cfg.NCtx,
		nBatch: cfg.NBatch,
		pass2:  cfg.Pass2.withDefaults(),
	}
	l.stopIDs = l.stopTokens()
	l.chanOpen, l.chanClose = l.singleToken("<|channel>"), l.singleToken("<channel|>")
	l.think = cfg.Think && l.chanOpen >= 0 && l.chanClose >= 0
	l.thinkMax = cfg.ThinkMaxTokens
	if l.thinkMax <= 0 {
		l.thinkMax = 512
	}
	l.pass2Turn, l.pass2Cue = cfg.Pass2Turn, cfg.Pass2Cue
	if l.pass2Cue == "" {
		l.pass2Cue = "Spoken response:"
	}
	if err := l.resolveTemplate(cfg.Template); err != nil {
		return l, err
	}
	return l, nil
}

// Thinking reports whether the thought channel is on for this model.
func (l *LLM) Thinking() bool { return l.think }

// singleToken returns the id of a special token by its text, -1 when the
// vocabulary has no such single token.
func (l *LLM) singleToken(text string) C.llama_token {
	toks := l.tokenize(text, false)
	if len(toks) != 1 {
		return -1
	}
	return toks[0]
}

// Tokens exposes the tokenizer (ids) for boundary checks: the cached prefix
// is only valid if tokenize(prefix)+tokenize(tail) == tokenize(prefix+tail).
func (l *LLM) Tokens(text string, addSpecial bool) []int32 {
	l.mu.Lock()
	defer l.mu.Unlock()
	toks := l.tokenize(text, addSpecial)
	out := make([]int32, len(toks))
	for i, t := range toks {
		out[i] = int32(t)
	}
	return out
}

// Split renders a system prompt + user text the way PrimePrefix/TwoPass
// will: the KV-cached prefix and the per-turn tail.
func (l *LLM) Split(system, user string) (prefix, tail string, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	prefix, tailFmt, err := l.splitPrompt(system)
	if err != nil {
		return "", "", err
	}
	return prefix, strings.Replace(tailFmt, userSentinel, user, 1), nil
}

// Template reports the resolved chat template (a built-in name, or
// "gguf" when the model carried its own Jinja template).
func (l *LLM) Template() string {
	if strings.ContainsAny(l.tmpl, "{%") {
		return "gguf"
	}
	return l.tmpl
}

// EOT reports the end-of-turn marker text the adapter stops on.
func (l *LLM) EOT() string { return l.eotText }

func (l *LLM) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.mtmdClose != nil {
		l.mtmdClose() // the projector references the model: free it first
		l.mtmd, l.mtmdClose = nil, nil
	}
	if l.ctx != nil {
		C.llama_free(l.ctx)
		l.ctx = nil
	}
	if l.model != nil && !l.sibling {
		C.llama_model_free(l.model)
	}
	l.model = nil
}

// ── tokenizer plumbing ────────────────────────────────────────────────────────

func (l *LLM) tokenize(text string, addSpecial bool) []C.llama_token {
	cText := C.CString(text)
	defer C.free(unsafe.Pointer(cText))
	buf := make([]C.llama_token, len(text)+16)
	n := C.llama_tokenize(l.vocab, cText, C.int32_t(len(text)),
		&buf[0], C.int32_t(len(buf)), C.bool(addSpecial), C.bool(true))
	if n < 0 {
		buf = make([]C.llama_token, -n)
		n = C.llama_tokenize(l.vocab, cText, C.int32_t(len(text)),
			&buf[0], C.int32_t(len(buf)), C.bool(addSpecial), C.bool(true))
	}
	return buf[:n]
}

// pieces renders token ids one by one (diagnostics: where two
// tokenizations of the same text part ways).
func (l *LLM) pieces(ids []int32) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = l.detokenize([]C.llama_token{C.llama_token(id)})
	}
	return out
}

func (l *LLM) detokenize(toks []C.llama_token) string {
	if len(toks) == 0 {
		return ""
	}
	buf := make([]C.char, len(toks)*16+16)
	n := C.llama_detokenize(l.vocab, &toks[0], C.int32_t(len(toks)),
		&buf[0], C.int32_t(len(buf)), C.bool(false), C.bool(false))
	if n < 0 {
		buf = make([]C.char, -n)
		n = C.llama_detokenize(l.vocab, &toks[0], C.int32_t(len(toks)),
			&buf[0], C.int32_t(len(buf)), C.bool(false), C.bool(false))
	}
	return C.GoStringN(&buf[0], n)
}

// stopTokens: the model's end-of-turn and end-of-sentence ids (gemma:
// <end_of_turn> / <eos>; phi3: <|end|>), on top of llama_vocab_is_eog.
func (l *LLM) stopTokens() map[C.llama_token]bool {
	stops := map[C.llama_token]bool{}
	for _, t := range []C.llama_token{C.llama_vocab_eot(l.vocab), C.llama_vocab_eos(l.vocab)} {
		if t >= 0 {
			stops[t] = true
		}
	}
	if eot := C.llama_vocab_eot(l.vocab); eot >= 0 {
		l.eotText = C.GoString(C.llama_vocab_get_text(l.vocab, eot))
	} else if eos := C.llama_vocab_eos(l.vocab); eos >= 0 {
		// gemma4 declares only eos (<turn|>), no separate eot
		l.eotText = C.GoString(C.llama_vocab_get_text(l.vocab, eos))
	}
	return stops
}

// builtinTemplate maps a GGUF architecture to llama.cpp's built-in chat
// template name (llama_chat_apply_template accepts the name).
var builtinTemplate = map[string]string{
	"gemma": "gemma", "gemma2": "gemma", "gemma3": "gemma", "gemma3n": "gemma",
	"phi3": "phi3", "llama": "llama3", "qwen2": "chatml", "qwen3": "chatml",
	"mistral": "mistral-v7", "deepseek2": "deepseek3",
}

// resolveTemplate picks the chat template: an explicit name/text, else the
// GGUF's own tokenizer.chat_template, else the built-in one for the
// architecture. The choice is validated by rendering once.
func (l *LLM) resolveTemplate(want string) error {
	switch want {
	case "", "auto":
		if arch := l.meta("general.architecture"); arch == "gemma4" {
			// the GGUF's Jinja template uses <|turn>/<turn|>, which
			// llama_chat_apply_template (May 2026) does not detect — the
			// built-in "gemma" would render Gemma 3 tokens instead
			l.tmpl = TemplateGemma4
		} else if t := C.llama_model_chat_template(l.model, nil); t != nil {
			l.tmpl = C.GoString(t)
		} else {
			arch := l.meta("general.architecture")
			name, ok := builtinTemplate[arch]
			if !ok {
				return fmt.Errorf("llm: no chat template in the GGUF and no "+
					"built-in for architecture %q — set llm.template", arch)
			}
			l.tmpl = name
		}
	default:
		l.tmpl = want
	}
	full, err := l.applyTemplate([]chatMsg{{"system", "s"}, {"user", userSentinel}}, true)
	if err != nil {
		return fmt.Errorf("llm: template %q: %w", l.Template(), err)
	}
	if !strings.Contains(full, userSentinel) {
		return fmt.Errorf("llm: template %q lost the user turn", l.Template())
	}
	return nil
}

func (l *LLM) meta(key string) string {
	cKey := C.CString(key)
	defer C.free(unsafe.Pointer(cKey))
	buf := make([]C.char, 256)
	if n := C.llama_model_meta_val_str(l.model, cKey, &buf[0], C.size_t(len(buf))); n > 0 {
		return C.GoStringN(&buf[0], n)
	}
	return ""
}

type chatMsg struct{ role, content string }

// applyTemplate renders msgs through llama_chat_apply_template (built-in
// templates by name, or Jinja text the library recognizes).
func (l *LLM) applyTemplate(msgs []chatMsg, addAss bool) (string, error) {
	if l.tmpl == TemplateGemma4 {
		return l.renderGemma4(msgs, addAss), nil
	}
	cTmpl := C.CString(l.tmpl)
	defer C.free(unsafe.Pointer(cTmpl))
	n := len(msgs)
	arr := (*[1 << 12]C.struct_llama_chat_message)(C.malloc(C.size_t(n) *
		C.size_t(unsafe.Sizeof(C.struct_llama_chat_message{}))))[:n:n]
	defer C.free(unsafe.Pointer(&arr[0]))
	for i, m := range msgs {
		arr[i].role, arr[i].content = C.CString(m.role), C.CString(m.content)
		defer C.free(unsafe.Pointer(arr[i].role))
		defer C.free(unsafe.Pointer(arr[i].content))
	}
	buf := make([]C.char, 4096)
	r := C.llama_chat_apply_template(cTmpl, &arr[0], C.size_t(n), C.bool(addAss),
		&buf[0], C.int32_t(len(buf)))
	if r > C.int32_t(len(buf)) {
		buf = make([]C.char, int(r)+1)
		r = C.llama_chat_apply_template(cTmpl, &arr[0], C.size_t(n), C.bool(addAss),
			&buf[0], C.int32_t(len(buf)))
	}
	if r < 0 {
		return "", errors.New("llama_chat_apply_template failed (unknown template?)")
	}
	return C.GoStringN(&buf[0], r), nil
}

// renderGemma4 is the Gemma 4 turn format (TemplateGemma4). "assistant" is
// "model"; with thinking on, <|think|> opens the system turn (the toggle is
// per conversation and lives in the system instruction, per Google's
// prompt-formatting guide).
func (l *LLM) renderGemma4(msgs []chatMsg, addAss bool) string {
	var b strings.Builder
	for _, m := range msgs {
		role, content := m.role, m.content
		if role == "assistant" {
			role = "model"
		}
		if role == "system" && l.think {
			content = "<|think|>\n" + content
		}
		b.WriteString("<|turn>" + role + "\n" + content + "<turn|>\n")
	}
	if addAss {
		b.WriteString("<|turn>model\n")
	}
	return b.String()
}

// splitPrompt renders [system, user] and splits it into the constant
// prefix (cached in the KV once per system prompt) and the per-turn tail
// (re-evaluated with the user text in place of the sentinel). The boundary
// is the system-only rendering when the template has one (phi3:
// "<|system|>\n…<|end|>\n", byte-identical to the shim's old hard-code, so
// the KV reuse is unchanged); templates that fold the system prompt into
// the first user turn (gemma) split right before the user text instead.
func (l *LLM) splitPrompt(system string) (prefix, tailFmt string, err error) {
	if system == "" {
		// no system turn (RAG §11.2: instructions folded into the user
		// cue); the prefix is the template's text before the user's words
		full, err := l.applyTemplate([]chatMsg{{"user", userSentinel}}, true)
		if err != nil {
			return "", "", err
		}
		i := strings.Index(full, userSentinel)
		if i < 0 {
			return "", "", errors.New("llm: template lost the user turn")
		}
		return full[:i], full[i:], nil
	}
	full, err := l.applyTemplate([]chatMsg{{"system", system}, {"user", userSentinel}}, true)
	if err != nil {
		return "", "", err
	}
	i := strings.Index(full, userSentinel)
	if i < 0 {
		return "", "", errors.New("llm: template lost the user turn")
	}
	cut := i
	if sysOnly, err := l.applyTemplate([]chatMsg{{"system", system}}, false); err == nil &&
		sysOnly != "" && len(sysOnly) <= i && strings.HasPrefix(full, sysOnly) {
		cut = len(sysOnly)
	}
	return full[:cut], full[cut:], nil
}

func (l *LLM) tail(user string) string {
	// leading/trailing newlines would merge with the prefix's last token
	return strings.Replace(l.tailFmt, userSentinel, strings.Trim(user, "\r\n"), 1)
}

// tailTokens tokenizes the per-turn tail IN CONTEXT: tokenize(prefix+tail)
// minus the cached prefix. A split tokenization is not the same thing
// (step-4 F10, opus's E1): the SPM tokenizer of gemma3 puts a space-prefixed
// piece at the start of every fresh text fragment, so tokens(tail) begins
// "▁halt" where the whole prompt has "halt" after "\n\n" — and gemma3 folds
// the system prompt into the user turn, so no cut inside that turn is
// stable. The prefix itself IS stable (checked in primeLocked); if a turn
// ever re-tokenizes it differently, the split tokenization is the fallback
// and the miss is logged.
func (l *LLM) tailTokens(user string) []C.llama_token { return l.tailTokensWith(user, "") }

// tailTokensWith is tailTokens with text already in the model's turn
// appended (Decide: earlier calls and their responses), tokenized in the
// same context.
func (l *LLM) tailTokensWith(user, cont string) []C.llama_token {
	return l.afterBase(l.tail(user) + cont)
}

// afterBase tokenizes t in the context of prefix+tape and returns only its
// own tokens; when the base would re-tokenize, the split tokenization is
// the fallback and the miss is logged.
func (l *LLM) afterBase(t string) []C.llama_token { return l.after(t, true) }

// after tokenizes t in the context of the prefix, plus the tape when
// withTape, and returns only its own tokens.
func (l *LLM) after(t string, withTape bool) []C.llama_token {
	if t == "" {
		return nil
	}
	base := append([]C.llama_token{}, l.preToks...)
	baseText := l.preText
	if withTape {
		base, baseText = append(base, l.tapeToks...), baseText+l.tapeText
	}
	whole := l.tokenize(baseText+t, true)
	if n := len(base); len(whole) > n && sameTokens(whole[:n], base) {
		return whole[n:]
	}
	fmt.Fprintf(os.Stderr, "[llm] tail %.60q re-tokenizes the prefix+tape — split tokenization\n", t)
	return l.tokenize(t, false)
}

// cToken names the token type for the tests (test files cannot use cgo).
type cToken = C.llama_token

func sameTokens(a, b []C.llama_token) bool {
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

// prefixProbes are turns the prime-time invariant check renders: plain,
// capitalized, punctuated, a number, leading space (whisper's habit).
var prefixProbes = []string{"halt", "What time is it?", "go to tag 5", " the robot is not armed"}

// checkPrefix asserts the KV-prefix invariant for the in-context tail:
// tokens(prefix) ++ tailTokens(u) == tokens(prefix+tail(u)) for the probes.
func (l *LLM) checkPrefix() error {
	for _, u := range prefixProbes {
		whole := l.tokenize(l.preText+l.tail(u), true)
		n := len(l.preToks)
		if len(whole) <= n || !sameTokens(whole[:n], l.preToks) {
			return fmt.Errorf("llm: prefix is not tokenization-stable before %q", u)
		}
	}
	return nil
}

// ── decode ────────────────────────────────────────────────────────────────────

// decodeTokens feeds tokens through llama_decode in n_batch chunks. Positions
// are inferred from the KV sequence (batch.pos == NULL), so this continues
// wherever the memory currently ends.
func (l *LLM) decodeTokens(toks []C.llama_token) error {
	for off := 0; off < len(toks); off += l.nBatch {
		end := off + l.nBatch
		if end > len(toks) {
			end = len(toks)
		}
		chunk := toks[off:end]
		batch := C.llama_batch_get_one(&chunk[0], C.int32_t(len(chunk)))
		if rc := C.llama_decode(l.ctx, batch); rc != 0 {
			return fmt.Errorf("llama_decode returned %d (n_past=%d)", rc, l.nPast)
		}
		l.nPast += len(chunk)
		l.nDecoded.Add(int64(len(chunk)))
	}
	return nil
}

// TokenCounts is the adapter's running count: tokens evaluated into the KV
// (prompts, tails, the tape) and tokens generated (the quiet-cost and
// tokens/hour measures, runtime-wire steps 4–5).
func (l *LLM) TokenCounts() (decoded, generated int64) { return l.nDecoded.Load(), l.nGenerated.Load() }

// ── KV prefix reuse (two_pass._prime_prefix) ─────────────────────────────────

// PrimePrefix evaluates the constant system prefix ONCE and keeps it in the
// KV cache; each turn then drops only the tokens after it. Call at startup
// (pre-warm) — TwoPass re-primes automatically if the prefix changed.
func (l *LLM) PrimePrefix(systemPrompt string) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.primeLocked(systemPrompt)
}

func (l *LLM) primeLocked(systemPrompt string) (int, error) {
	if l.tapeOut && l.prefix == systemPrompt && l.nPrefix > 0 {
		// a Decide ran without the tape: put it back after the prefix
		l.tapeOut = false
		if err := l.rewind(systemPrompt, l.nPrefix); err != nil {
			return 0, err
		}
		if l.nPast == l.nPrefix && len(l.tapeToks) > 0 { // (a failed rewind re-primed it already)
			if err := l.decodeTokens(l.tapeToks); err != nil {
				return 0, err
			}
		}
		return l.nPrefix + len(l.tapeToks), nil
	}
	if l.prefix == systemPrompt && l.nPrefix > 0 && l.nPast >= l.nPrefix+len(l.tapeToks) {
		return l.nPrefix + len(l.tapeToks), nil
	}
	// the tape belongs to its prefix: kept across a re-prime of the same
	// prompt (a failed rewind), dropped when the prompt changes
	if l.prefix != systemPrompt && len(l.tapeToks) > 0 {
		fmt.Fprintf(os.Stderr, "[llm] prompt changed — tape of %d tok dropped\n", len(l.tapeToks))
		l.tapeText, l.tapeToks = "", nil
	}
	prefixText, tailFmt, err := l.splitPrompt(systemPrompt)
	if err != nil {
		return 0, err
	}
	l.tailFmt = tailFmt
	toks := l.tokenize(prefixText, true)
	l.preText, l.preToks = prefixText, toks
	if err := l.checkPrefix(); err != nil {
		fmt.Fprintf(os.Stderr, "[llm] %v — turns fall back to split tokenization\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "[llm] prefix %d tok: tokenization-stable, tail tokenized in context\n", len(toks))
	}
	mem := C.llama_get_memory(l.ctx)
	C.llama_memory_clear(mem, C.bool(true))
	l.nPast, l.tapeOut = 0, false
	if err := l.decodeTokens(toks); err != nil {
		return 0, err
	}
	l.prefix = systemPrompt
	l.nPrefix = len(toks)
	if len(l.tapeToks) > 0 {
		if err := l.decodeTokens(l.tapeToks); err != nil {
			return 0, err
		}
	}
	return l.nPrefix + len(l.tapeToks), nil
}

// rewind drops every token after the cached prefix (N09-1071 invariant 2:
// no earlier turn, finished or discarded, leaves a trace). If the memory
// refuses the partial removal, the cache is cleared and the prefix
// re-evaluated instead of continuing on a contaminated context.
func (l *LLM) rewind(systemPrompt string, nPrefix int) error {
	mem := C.llama_get_memory(l.ctx)
	ok := bool(C.llama_memory_seq_rm(mem, 0, C.llama_pos(nPrefix), -1))
	if ok && int(C.llama_memory_seq_pos_max(mem, 0)) != nPrefix-1 {
		// the call said yes but the sequence is not where the prefix ends:
		// treat as a failed rewind (step-4 F3 watch)
		fmt.Fprintf(os.Stderr, "[llm] rewind: seq_pos_max=%d after seq_rm, want %d — clearing\n",
			int(C.llama_memory_seq_pos_max(mem, 0)), nPrefix-1)
		ok = false
	}
	if !ok {
		C.llama_memory_clear(mem, C.bool(true))
		l.nPast, l.nPrefix = 0, 0 // re-prime the same prompt: the tape is decoded again
		if _, err := l.primeLocked(systemPrompt); err != nil {
			return fmt.Errorf("llm: rewind failed and re-prime failed: %w", err)
		}
		return nil
	}
	l.nPast = nPrefix
	return nil
}

// ── two-pass generation (two_pass.approach_a) ────────────────────────────────

// TwoPass runs one intent turn: reuse the cached prefix, eval the user tail,
// generate grammar-constrained JSON (greedy), append the TTS cue, generate
// the spoken ack (temp 0.4 / top_k 40). The KV is CONTINUED across both
// passes — no state save/load.
func (l *LLM) TwoPass(systemPrompt, userText, gbnf string) (*Result, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.twoPassLocked(systemPrompt, gbnf, func() (time.Duration, error) {
		return 0, l.decodeTokens(l.tailTokens(userText))
	})
}

// twoPassLocked is TwoPass with the user tail's evaluation passed in: text
// tokens for TwoPass, an audio slice through libmtmd for TwoPassAudio (the
// E3 path). evalTail returns the share of its time spent encoding media.
func (l *LLM) twoPassLocked(systemPrompt, gbnf string, evalTail func() (time.Duration, error)) (*Result, error) {
	tEval0 := time.Now()
	nPrefix, err := l.primeLocked(systemPrompt)
	if err != nil {
		return nil, err
	}
	// Drop everything after the prefix, rewind, eval only the user tail.
	if err := l.rewind(systemPrompt, nPrefix); err != nil {
		return nil, err
	}
	tEncode, err := evalTail()
	if err != nil {
		return nil, err
	}
	tEval := time.Since(tEval0)

	// Thinking (Gemma 4, Config.Think): the thought channel runs
	// unconstrained before the grammar is switched on.
	thought, nThink, tThink, err := l.thinkLocked()
	if err != nil {
		return nil, err
	}

	// Pass 1: grammar-constrained JSON, greedy (production json_temp=0).
	cGbnf, cRoot := C.CString(gbnf), C.CString("root")
	defer C.free(unsafe.Pointer(cGbnf))
	defer C.free(unsafe.Pointer(cRoot))
	grammar := C.llama_sampler_init_grammar(l.vocab, cGbnf, cRoot)
	if grammar == nil {
		return nil, errors.New("llm: grammar failed to compile")
	}
	chain1 := C.llama_sampler_chain_init(C.llama_sampler_chain_default_params())
	C.llama_sampler_chain_add(chain1, grammar) // chain owns grammar now
	C.llama_sampler_chain_add(chain1, C.llama_sampler_init_greedy())
	defer C.llama_sampler_free(chain1)

	t0 := time.Now()
	jsonToks, err := l.generate(chain1, 80, "")
	if err != nil {
		return nil, err
	}
	tJSON := time.Since(t0)
	jsonText := l.clean(l.detokenize(jsonToks))

	// Pass 2: TTS continuation — the JSON stays in context; append the cue and
	// keep generating.
	cue := "\nSpoken response:"
	if l.pass2Turn {
		// close the model turn and ask in a user turn (Config.Pass2Turn)
		turn, err := l.applyTemplate([]chatMsg{{"user", l.pass2Cue}}, true)
		if err != nil {
			return nil, err
		}
		cue = l.eotText + "\n" + turn
	}
	if err := l.decodeTokens(l.tokenize(cue, false)); err != nil {
		return nil, err
	}
	chain2 := C.llama_sampler_chain_init(C.llama_sampler_chain_default_params())
	C.llama_sampler_chain_add(chain2, C.llama_sampler_init_top_k(C.int32_t(l.pass2.TopK)))
	C.llama_sampler_chain_add(chain2, C.llama_sampler_init_temp(C.float(l.pass2.Temp)))
	C.llama_sampler_chain_add(chain2, C.llama_sampler_init_dist(C.LLAMA_DEFAULT_SEED))
	defer C.llama_sampler_free(chain2)

	t0 = time.Now()
	// 30 cut acks mid-sentence ("...here to help you! How can I")
	ttsToks, err := l.generate(chain2, l.pass2.MaxTokens, "\n")
	if err != nil {
		return nil, err
	}
	tTTS := time.Since(t0)
	voice := l.detokenize(ttsToks)
	if i := strings.IndexByte(voice, '\n'); i >= 0 {
		voice = voice[:i]
	}
	voice = l.clean(voice)

	return &Result{JSON: jsonText, Voice: voice,
		TJSON: tJSON, TTTS: tTTS, TEval: tEval, TEncode: tEncode,
		Thought: thought, NThink: nThink, TThink: tThink}, nil
}

// thinkLocked runs the Gemma 4 thought channel when thinking is on: the
// first sampled token decides — <|channel> opens a thought that is decoded
// greedily until <channel|> (or thinkMax tokens, then the close token is
// fed to end it); anything else means no thought, nothing is decoded and
// the logits stay for the grammar pass. Returns the thought text.
func (l *LLM) thinkLocked() (string, int, time.Duration, error) {
	if !l.think {
		return "", 0, 0, nil
	}
	t0 := time.Now()
	chain := C.llama_sampler_chain_init(C.llama_sampler_chain_default_params())
	C.llama_sampler_chain_add(chain, C.llama_sampler_init_greedy())
	defer C.llama_sampler_free(chain)
	if tok := C.llama_sampler_sample(chain, l.ctx, -1); tok != l.chanOpen {
		return "", 0, time.Since(t0), nil
	}
	if rc := C.decode_one(l.ctx, l.chanOpen); rc != 0 {
		return "", 0, 0, fmt.Errorf("llama_decode returned %d opening the thought", rc)
	}
	l.nPast++
	var out []C.llama_token
	closed := false
	for len(out) < l.thinkMax {
		tok := C.llama_sampler_sample(chain, l.ctx, -1)
		if tok == l.chanClose {
			closed = true
			break
		}
		if bool(C.llama_vocab_is_eog(l.vocab, tok)) || l.stopIDs[tok] {
			break
		}
		out = append(out, tok)
		if rc := C.decode_one(l.ctx, tok); rc != 0 {
			return "", 0, 0, fmt.Errorf("llama_decode returned %d mid-thought", rc)
		}
		l.nPast++
	}
	// close the channel (the model's own close, or ours when cut) so the
	// answer tokens follow in the trained position
	_ = closed
	if rc := C.decode_one(l.ctx, l.chanClose); rc != 0 {
		return "", 0, 0, fmt.Errorf("llama_decode returned %d closing the thought", rc)
	}
	l.nPast++
	text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l.detokenize(out)), "thought"))
	return text, len(out) + 2, time.Since(t0), nil
}

// clean trims decoded text, drops Gemma 4 thought-channel markup and strips
// a trailing end-of-turn marker.
func (l *LLM) clean(text string) string {
	t := dropChannel(strings.TrimSpace(text))
	if l.eotText != "" {
		t = strings.TrimSpace(strings.TrimSuffix(t, l.eotText))
	}
	return strings.TrimSpace(strings.TrimSuffix(t, "<|end|>"))
}

// dropChannel removes Gemma 4 thought-channel markup so it never reaches
// TTS: a leading <|channel>…<channel|> thought is dropped, and text from a
// stray marker on is cut (`"I can't do that."<channel|>{"intent":…}`).
func dropChannel(t string) string {
	const open, close = "<|channel>", "<channel|>"
	if strings.HasPrefix(t, open) {
		i := strings.Index(t, close)
		if i < 0 {
			return ""
		}
		t = strings.TrimSpace(t[i+len(close):])
	}
	for _, m := range []string{open, close} {
		if i := strings.Index(t, m); i >= 0 {
			t = strings.TrimSpace(t[:i])
		}
	}
	return t
}

// Narrate is the single-pass narration of a typed event (HARNESS.md §4,
// DESIGN.md "Narrate an event"): the same cached prefix, the event as the
// user turn, then only the spoken-response pass — no intent JSON. One
// sentence, pass-2 sampling, stops at the first newline.
func (l *LLM) Narrate(systemPrompt, event string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	nPrefix, err := l.primeLocked(systemPrompt)
	if err != nil {
		return "", err
	}
	if err := l.rewind(systemPrompt, nPrefix); err != nil {
		return "", err
	}
	user, cue := event, "Spoken response:"
	if l.pass2Turn {
		// the cue rides in the user turn (Config.Pass2Cue), no model-side cue
		user, cue = event+"\n\n"+l.pass2Cue, ""
	}
	if err := l.decodeTokens(l.tailTokens(user)); err != nil {
		return "", err
	}
	// thinking on: let the thought channel close before the cue
	if _, _, _, err := l.thinkLocked(); err != nil {
		return "", err
	}
	if cue != "" {
		if err := l.decodeTokens(l.tokenize(cue, false)); err != nil {
			return "", err
		}
	}
	chain := C.llama_sampler_chain_init(C.llama_sampler_chain_default_params())
	C.llama_sampler_chain_add(chain, C.llama_sampler_init_top_k(C.int32_t(l.pass2.TopK)))
	C.llama_sampler_chain_add(chain, C.llama_sampler_init_temp(C.float(l.pass2.Temp)))
	C.llama_sampler_chain_add(chain, C.llama_sampler_init_dist(C.LLAMA_DEFAULT_SEED))
	defer C.llama_sampler_free(chain)
	// the few-shot prompt teaches `"utterance" → {json}`; a narration that
	// starts imitating it is cut at the arrow or the brace
	toks, err := l.generate(chain, l.pass2.MaxTokens, "\n→{")
	if err != nil {
		return "", err
	}
	voice := l.detokenize(toks)
	if i := strings.IndexAny(voice, "\n→{"); i >= 0 {
		voice = voice[:i]
	}
	return strings.TrimSpace(strings.Trim(l.clean(voice), `"`)), nil
}

// Decide is one grammar-constrained, greedy pass under its own system
// prompt (runtime-wire step 3: the orchestrator's tool call). The system
// prompt is the cached prefix like TwoPass's — switching from the dog
// prompt re-primes it once. cont is what is already in the model's turn
// after the user text (earlier calls and the runtime's responses), so a
// refusal steers the next call. Gemma 4 only (the native call markup).
func (l *LLM) Decide(systemPrompt, user, cont, gbnf string, maxTokens int) (string, error) {
	out, _, err := l.DecideTimed(systemPrompt, user, cont, gbnf, maxTokens)
	return out, err
}

// DecideTimed is Decide plus the time from the call to its first token
// (the tail's evaluation included): the splice-to-first-token measure.
//
// It runs on B's context (RAG §9, runtime-wire step 5: C ≈ B, the tape
// made the first pick worse): the one prefix and the turn, with the tape
// cut from the KV — no standing region for triage. The tape stays the
// narration's and the asks'; the next such turn decodes it back.
func (l *LLM) DecideTimed(systemPrompt, user, cont, gbnf string, maxTokens int) (string, time.Duration, error) {
	if l.tmpl != TemplateGemma4 {
		return "", 0, errors.New("llm: Decide needs the gemma4 template (native tool calls)")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	t0 := time.Now() // after the lock: the splice cost, not the wait behind a narration
	if l.prefix != systemPrompt || l.nPrefix == 0 {
		if _, err := l.primeLocked(systemPrompt); err != nil {
			return "", 0, err
		}
	}
	for try := 0; l.nPast != l.nPrefix && try < 2; try++ { // a failed rewind re-primes with the tape
		if err := l.rewind(systemPrompt, l.nPrefix); err != nil {
			return "", 0, err
		}
	}
	l.tapeOut = len(l.tapeToks) > 0
	if err := l.decodeTokens(l.after(l.tail(user)+cont, false)); err != nil {
		return "", 0, err
	}
	cGbnf, cRoot := C.CString(gbnf), C.CString("root")
	defer C.free(unsafe.Pointer(cGbnf))
	defer C.free(unsafe.Pointer(cRoot))
	grammar := C.llama_sampler_init_grammar(l.vocab, cGbnf, cRoot)
	if grammar == nil {
		return "", 0, errors.New("llm: decide grammar failed to compile")
	}
	chain := C.llama_sampler_chain_init(C.llama_sampler_chain_default_params())
	C.llama_sampler_chain_add(chain, grammar)
	C.llama_sampler_chain_add(chain, C.llama_sampler_init_greedy())
	defer C.llama_sampler_free(chain)
	if maxTokens <= 0 {
		maxTokens = 120
	}
	tGen := time.Now()
	toks, err := l.generate(chain, maxTokens, "")
	if err != nil {
		return "", 0, err
	}
	ttft := tGen.Sub(t0) + l.firstTok
	return strings.TrimSpace(l.detokenize(toks)), ttft, nil
}

// Speak is the runtime's cued narration (runtime-wire step 4, CML.md §7):
// one turn after prefix + tape (the piece and its cue as the user text),
// one sentence back with pass-2 sampling, cut at the first newline. With
// think, the turn opens the thought channel itself (<|channel>) — per call,
// without <|think|> in the system turn, so the one prefix stays — the
// thought runs greedily to its close (≤ thinkMax), then the sentence.
// Returns the sentence, the time to its first spoken token and the thought.
func (l *LLM) Speak(systemPrompt, user string, think bool) (string, time.Duration, string, error) {
	n := 0
	if think {
		n = 64 // a narration's thought: short (live: uncapped it ran ~250 tok, 1.7 s)
	}
	return l.SpeakCap(systemPrompt, user, n)
}

// SpeakCap is Speak with the thought's cap per call: 0 = no thinking
// (RAG §11.2's sweep: off / 64 / 256), bounded by Config.ThinkMaxTokens.
func (l *LLM) SpeakCap(systemPrompt, user string, thinkCap int) (string, time.Duration, string, error) {
	think := thinkCap > 0
	t0 := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	base, err := l.primeLocked(systemPrompt)
	if err != nil {
		return "", 0, "", err
	}
	if err := l.rewind(systemPrompt, base); err != nil {
		return "", 0, "", err
	}
	if err := l.decodeTokens(l.tailTokens(user)); err != nil {
		return "", 0, "", err
	}
	var thought string
	if think && l.chanOpen >= 0 {
		if rc := C.decode_one(l.ctx, l.chanOpen); rc != 0 {
			return "", 0, "", fmt.Errorf("llama_decode returned %d opening the thought", rc)
		}
		l.nPast++
		g := C.llama_sampler_chain_init(C.llama_sampler_chain_default_params())
		C.llama_sampler_chain_add(g, C.llama_sampler_init_greedy())
		var out []C.llama_token
		max := thinkCap
		if l.thinkMax > 0 {
			max = min(l.thinkMax, thinkCap)
		}
		for len(out) < max {
			tok := C.llama_sampler_sample(g, l.ctx, -1)
			if tok == l.chanClose || bool(C.llama_vocab_is_eog(l.vocab, tok)) {
				break
			}
			out = append(out, tok)
			l.nGenerated.Add(1)
			if rc := C.decode_one(l.ctx, tok); rc != 0 {
				C.llama_sampler_free(g)
				return "", 0, "", fmt.Errorf("llama_decode returned %d mid-thought", rc)
			}
			l.nPast++
		}
		C.llama_sampler_free(g)
		thought = l.detokenize(out)
		if rc := C.decode_one(l.ctx, l.chanClose); rc != 0 {
			return "", 0, "", fmt.Errorf("llama_decode returned %d closing the thought", rc)
		}
		l.nPast++
	}
	// the turn speaks through the dog prompt's own convention: its examples
	// teach that "Spoken response:" is followed by a natural sentence
	// (live, without it: "Hello, …", "UNKNOWN: …", "DANGER: …")
	if err := l.decodeTokens(l.tokenize("Spoken response:", false)); err != nil {
		return "", 0, thought, err
	}
	// the sentence is held to a sentence by grammar: under the one prefix
	// (dog JSON + tool declarations) E2B answered some cues with an intent
	// or a tool call (live, step 4)
	cG, cR := C.CString(sentenceGBNF), C.CString("root")
	defer C.free(unsafe.Pointer(cG))
	defer C.free(unsafe.Pointer(cR))
	chain := C.llama_sampler_chain_init(C.llama_sampler_chain_default_params())
	if g := C.llama_sampler_init_grammar(l.vocab, cG, cR); g != nil {
		C.llama_sampler_chain_add(chain, g)
	}
	C.llama_sampler_chain_add(chain, C.llama_sampler_init_top_k(C.int32_t(l.pass2.TopK)))
	C.llama_sampler_chain_add(chain, C.llama_sampler_init_temp(C.float(l.pass2.Temp)))
	C.llama_sampler_chain_add(chain, C.llama_sampler_init_dist(C.LLAMA_DEFAULT_SEED))
	defer C.llama_sampler_free(chain)
	tGen := time.Now()
	toks, err := l.generate(chain, l.pass2.MaxTokens+20, "\n")
	if err != nil {
		return "", 0, thought, err
	}
	ttft := tGen.Sub(t0) + l.firstTok
	voice := l.detokenize(toks)
	if i := strings.IndexByte(voice, '\n'); i >= 0 {
		voice = voice[:i]
	}
	return strings.TrimSpace(strings.Trim(l.clean(voice), `"`)), ttft, strings.TrimSpace(thought), nil
}

// sentenceGBNF: one spoken sentence — a capital, ordinary text without
// markup, JSON, tool-call or fact-syntax characters, a final stop.
const sentenceGBNF = `root ::= " "? "\""? [A-Z] [a-zA-Z0-9 ,;'()%-]{3,200} [.!?] "\""?`

// Answer is one free-text model turn after the cached prefix and the tape
// (E6 splice): the user text as the turn, the thought channel closed when
// thinking is on, then greedy until the end of the turn. It returns the
// cleaned text, the time to its first token (the user turn's evaluation
// included) and the whole call. The turn is rolled back by the next call.
func (l *LLM) Answer(systemPrompt, user string, maxTokens int) (string, time.Duration, time.Duration, error) {
	t0 := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	base, err := l.primeLocked(systemPrompt)
	if err != nil {
		return "", 0, 0, err
	}
	if err := l.rewind(systemPrompt, base); err != nil {
		return "", 0, 0, err
	}
	if err := l.decodeTokens(l.tailTokens(user)); err != nil {
		return "", 0, 0, err
	}
	if _, _, _, err := l.thinkLocked(); err != nil {
		return "", 0, 0, err
	}
	chain := C.llama_sampler_chain_init(C.llama_sampler_chain_default_params())
	C.llama_sampler_chain_add(chain, C.llama_sampler_init_greedy())
	defer C.llama_sampler_free(chain)
	if maxTokens <= 0 {
		maxTokens = 80
	}
	tGen := time.Now()
	toks, err := l.generate(chain, maxTokens, "")
	if err != nil {
		return "", 0, 0, err
	}
	ttft := tGen.Sub(t0) + l.firstTok
	return strings.TrimSpace(l.clean(l.detokenize(toks))), ttft, time.Since(t0), nil
}

// ── the tape (runtime-wire step 4) ───────────────────────────────────────────

// Splice is what one tape write cost.
type Splice struct {
	Tokens int           // tokens decoded by this write
	Tape   int           // tape tokens after it
	Dur    time.Duration // the decode
}

// TapeTurn renders one tape entry as a whole turn of the model's template
// (gemma4: "<|turn>user\n…<turn|>\n"), so entries append without editing.
func (l *LLM) TapeTurn(role, text string) string {
	if l.tmpl == TemplateGemma4 {
		return l.renderGemma4([]chatMsg{{role, text}}, false)
	}
	r, err := l.applyTemplate([]chatMsg{{role, text}}, false)
	if err != nil {
		return text + "\n"
	}
	return r
}

// AppendTape adds whole turns at the end of the tape: tokenized in the
// context of prefix+tape, decoded once, kept. A turn in flight is rolled
// back first (the tape is written only between turns).
func (l *LLM) AppendTape(systemPrompt, text string) (Splice, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t0 := time.Now()
	base, err := l.primeLocked(systemPrompt)
	if err != nil {
		return Splice{}, err
	}
	if err := l.rewind(systemPrompt, base); err != nil {
		return Splice{}, err
	}
	toks := l.afterBase(text)
	if l.nPrefix+len(l.tapeToks)+len(toks)+l.turnReserve() > l.nCtx {
		return Splice{}, fmt.Errorf("llm: tape full (%d + %d tok, n_ctx %d) — compact first", len(l.tapeToks), len(toks), l.nCtx)
	}
	if err := l.decodeTokens(toks); err != nil {
		return Splice{}, err
	}
	l.tapeText += text
	l.tapeToks = append(l.tapeToks, toks...)
	return Splice{Tokens: len(toks), Tape: len(l.tapeToks), Dur: time.Since(t0)}, nil
}

// SetTape replaces the whole tape (compaction): the KV is cut back to the
// prefix and the new tape decoded once.
func (l *LLM) SetTape(systemPrompt, text string) (Splice, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t0 := time.Now()
	if _, err := l.primeLocked(systemPrompt); err != nil {
		return Splice{}, err
	}
	l.tapeText, l.tapeToks = "", nil
	if err := l.rewind(systemPrompt, l.nPrefix); err != nil {
		return Splice{}, err
	}
	toks := l.afterBase(text)
	if l.nPrefix+len(toks)+l.turnReserve() > l.nCtx {
		return Splice{}, fmt.Errorf("llm: compacted tape still too big (%d tok)", len(toks))
	}
	if err := l.decodeTokens(toks); err != nil {
		return Splice{}, err
	}
	l.tapeText, l.tapeToks = text, toks
	return Splice{Tokens: len(toks), Tape: len(toks), Dur: time.Since(t0)}, nil
}

// TapeTokens is the tape's size in tokens.
func (l *LLM) TapeTokens() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.tapeToks)
}

// turnReserve is the room a turn needs after prefix+tape (tail + pass 1 +
// cue + pass 2, or a Decide with its retries).
func (l *LLM) turnReserve() int { return 512 }

// generate samples tokens with the given chain until EOG / the model's
// end-of-turn / maxTokens (/ any rune of stopChars shows up in the decoded
// text; "" = none).
func (l *LLM) generate(chain *C.struct_llama_sampler, maxTokens int,
	stopChars string) ([]C.llama_token, error) {
	var out []C.llama_token
	t0 := time.Now()
	l.firstTok = 0
	for len(out) < maxTokens {
		tok := C.llama_sampler_sample(chain, l.ctx, -1) // applies + accepts
		if l.firstTok == 0 {
			l.firstTok = time.Since(t0)
		}
		l.nGenerated.Add(1)
		if bool(C.llama_vocab_is_eog(l.vocab, tok)) || l.stopIDs[tok] {
			break
		}
		out = append(out, tok)
		if stopChars != "" && strings.ContainsAny(l.detokenize(out), stopChars) {
			break
		}
		if rc := C.decode_one(l.ctx, tok); rc != 0 {
			return out, fmt.Errorf("llama_decode returned %d mid-generation", rc)
		}
		l.nPast++
	}
	return out, nil
}
