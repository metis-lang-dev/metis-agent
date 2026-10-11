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
	"strings"
	"sync"
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
	eotText string // end-of-turn marker text, stripped from decoded output
	pass2   Pass2
	stopIDs map[C.llama_token]bool // end-of-turn / end-of-sentence ids
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
	Template string
	Pass2    Pass2 // spoken-ack sampling; zero fields take the defaults
}

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

	cp := C.llama_context_default_params()
	cp.n_ctx = C.uint32_t(cfg.NCtx)
	cp.n_batch = C.uint32_t(cfg.NBatch)
	cp.n_threads = C.int32_t(cfg.NThreads)
	cp.n_threads_batch = C.int32_t(cfg.NThreads)
	ctx := C.llama_init_from_model(model, cp)
	if ctx == nil {
		C.llama_model_free(model)
		return nil, errors.New("llm: cannot create context")
	}

	l := &LLM{
		model: model, ctx: ctx,
		vocab:  C.llama_model_get_vocab(model),
		nCtx:   cfg.NCtx,
		nBatch: cfg.NBatch,
		pass2:  cfg.Pass2.withDefaults(),
	}
	l.stopIDs = l.stopTokens()
	if err := l.resolveTemplate(cfg.Template); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
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
	if l.ctx != nil {
		C.llama_free(l.ctx)
		l.ctx = nil
	}
	if l.model != nil {
		C.llama_model_free(l.model)
		l.model = nil
	}
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
		if t := C.llama_model_chat_template(l.model, nil); t != nil {
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

// splitPrompt renders [system, user] and splits it into the constant
// prefix (cached in the KV once per system prompt) and the per-turn tail
// (re-evaluated with the user text in place of the sentinel). The boundary
// is the system-only rendering when the template has one (phi3:
// "<|system|>\n…<|end|>\n", byte-identical to the shim's old hard-code, so
// the KV reuse is unchanged); templates that fold the system prompt into
// the first user turn (gemma) split right before the user text instead.
func (l *LLM) splitPrompt(system string) (prefix, tailFmt string, err error) {
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
	return strings.Replace(l.tailFmt, userSentinel, user, 1)
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
	}
	return nil
}

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
	if l.prefix == systemPrompt && l.nPast >= l.nPrefix && l.nPrefix > 0 {
		return l.nPrefix, nil
	}
	prefixText, tailFmt, err := l.splitPrompt(systemPrompt)
	if err != nil {
		return 0, err
	}
	l.tailFmt = tailFmt
	toks := l.tokenize(prefixText, true)
	mem := C.llama_get_memory(l.ctx)
	C.llama_memory_clear(mem, C.bool(true))
	l.nPast = 0
	if err := l.decodeTokens(toks); err != nil {
		return 0, err
	}
	l.prefix = systemPrompt
	l.nPrefix = len(toks)
	return l.nPrefix, nil
}

// ── two-pass generation (two_pass.approach_a) ────────────────────────────────

// TwoPass runs one intent turn: reuse the cached prefix, eval the user tail,
// generate grammar-constrained JSON (greedy), append the TTS cue, generate
// the spoken ack (temp 0.4 / top_k 40). The KV is CONTINUED across both
// passes — no state save/load.
func (l *LLM) TwoPass(systemPrompt, userText, gbnf string) (*Result, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	tEval0 := time.Now()
	nPrefix, err := l.primeLocked(systemPrompt)
	if err != nil {
		return nil, err
	}
	// Drop everything after the prefix, rewind, eval only the user tail.
	mem := C.llama_get_memory(l.ctx)
	C.llama_memory_seq_rm(mem, 0, C.llama_pos(nPrefix), -1)
	l.nPast = nPrefix
	if err := l.decodeTokens(l.tokenize(l.tail(userText), false)); err != nil {
		return nil, err
	}
	tEval := time.Since(tEval0)

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
	if err := l.decodeTokens(l.tokenize("\nSpoken response:", false)); err != nil {
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
		TJSON: tJSON, TTTS: tTTS, TEval: tEval}, nil
}

// clean trims decoded text and strips a trailing end-of-turn marker.
func (l *LLM) clean(text string) string {
	t := strings.TrimSpace(text)
	if l.eotText != "" {
		t = strings.TrimSpace(strings.TrimSuffix(t, l.eotText))
	}
	return strings.TrimSpace(strings.TrimSuffix(t, "<|end|>"))
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
	mem := C.llama_get_memory(l.ctx)
	C.llama_memory_seq_rm(mem, 0, C.llama_pos(nPrefix), -1)
	l.nPast = nPrefix
	if err := l.decodeTokens(l.tokenize(l.tail(event), false)); err != nil {
		return "", err
	}
	if err := l.decodeTokens(l.tokenize("Spoken response:", false)); err != nil {
		return "", err
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

// generate samples tokens with the given chain until EOG / the model's
// end-of-turn / maxTokens (/ any rune of stopChars shows up in the decoded
// text; "" = none).
func (l *LLM) generate(chain *C.struct_llama_sampler, maxTokens int,
	stopChars string) ([]C.llama_token, error) {
	var out []C.llama_token
	for len(out) < maxTokens {
		tok := C.llama_sampler_sample(chain, l.ctx, -1) // applies + accepts
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
