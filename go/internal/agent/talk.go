package agent

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/llm"
)

// TalkConfig is talk mode (FRAMEWORK §5): the free dialog on its own
// context of the same model, so the intent prefix is never evicted.
type TalkConfig struct {
	System   string       // the persona of the free dialog ("" = talk mode off)
	NCtx     int          // its context (default 4096)
	Sampling llm.Sampling // default: temp 1.0, top-k 64, top-p 0.95, min-p 0.05, repeat 1.1, 160 tokens
	Keep     int          // turns kept in the history (default 12)
}

// talker owns the dialog history: the user's turns and the replies as
// heard. The history is the context's tape, so a cut rewrites the tape and
// SpliceTape keeps the KV up to the cut word.
type talker struct {
	l   *llm.LLM
	cfg TalkConfig

	mu    sync.Mutex
	turns []talkTurn
}

type talkTurn struct {
	id, user, full, heard string
	note                  string // a narration the person heard: a short system turn, no reply
}

func newTalker(main *llm.LLM, cfg TalkConfig) (*talker, error) {
	if cfg.NCtx <= 0 {
		cfg.NCtx = 4096
	}
	if cfg.Keep <= 0 {
		cfg.Keep = 12
	}
	if cfg.Sampling.Temp == 0 && cfg.Sampling.TopK == 0 {
		cfg.Sampling = llm.Sampling{Temp: 1.0, TopK: 64, TopP: 0.95, MinP: 0.05, Repeat: 1.1}
	}
	if cfg.Sampling.MaxToks <= 0 {
		cfg.Sampling.MaxToks = 160
	}
	l, err := main.Sibling(cfg.NCtx)
	if err != nil {
		return nil, err
	}
	n, err := l.PrimePrefix(cfg.System)
	if err != nil {
		return nil, err
	}
	log.Printf("[talk] on: own context %d, persona %d tok, keep %d turns", cfg.NCtx, n, cfg.Keep)
	return &talker{l: l, cfg: cfg}, nil
}

// turn generates one reply; complete sentences go to out as they appear
// (SplitSentences' rule, so the page's resume units match), the rest at
// the end.
func (t *talker) turn(id, text string, out func(string)) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t0 := time.Now()
	sent := 0
	emit := func(s string, final bool) {
		for {
			rest := s[sent:]
			k := sentenceEnd(rest, final)
			if k <= 0 {
				return
			}
			if piece := strings.TrimSpace(rest[:k]); piece != "" {
				out(piece)
			}
			sent += k
		}
	}
	reply, ttft, err := t.l.Chat(t.cfg.System, text, t.cfg.Sampling, func(sofar string) bool {
		// whitespace collapsed as the whole reply is below, so the offsets agree
		emit(strings.Join(strings.Fields(sofar), " "), false)
		return true
	})
	if err != nil {
		return err
	}
	reply = strings.Join(strings.Fields(reply), " ")
	if sent > len(reply) {
		sent = len(reply)
	}
	emit(reply, true)
	t.turns = append(t.turns, talkTurn{id: id, user: strings.TrimSpace(text), full: reply, heard: reply})
	if n := len(t.turns) - t.cfg.Keep; n > 0 {
		t.turns = t.turns[n:]
	}
	sp, err := t.retape()
	log.Printf("[talk] %s %q → %d chars (first token %d ms, %d ms; tape %d tok, +%d decoded)",
		id, text, len(reply), ttft.Milliseconds(), time.Since(t0).Milliseconds(), sp.Tape, sp.Tokens)
	return err
}

// cut: the reply of turn id was heard up to heard. Waits for the turn when
// it is still generating.
func (t *talker) cut(id, heard string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := range t.turns {
		if t.turns[i].id == id {
			t.turns[i].heard = strings.Join(strings.Fields(heard), " ")
			sp, err := t.retape()
			log.Printf("[talk] cut %s at %d/%d chars: tape %d tok, %d re-decoded after the kept prefix (%v)",
				id, len(t.turns[i].heard), len(t.turns[i].full), sp.Tape, sp.Tokens, err)
			return
		}
	}
	log.Printf("[talk] cut %s: no such turn", id)
}

// note puts a narration the person heard into the dialog's context.
func (t *talker) note(text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.turns = append(t.turns, talkTurn{note: text})
	if n := len(t.turns) - 2*t.cfg.Keep; n > 0 {
		t.turns = t.turns[n:]
	}
	if _, err := t.retape(); err != nil {
		log.Printf("[talk] note: %v", err)
	}
}

// retape writes the history as the context's tape: each turn the user's
// words and the reply as heard, [interrupted] when cut short.
func (t *talker) retape() (llm.Splice, error) {
	var b strings.Builder
	for _, x := range t.turns {
		if x.note != "" {
			b.WriteString(t.l.TapeTurn("system", "You told the person: "+x.note))
			continue
		}
		b.WriteString(t.l.TapeTurn("user", x.user))
		r := x.heard
		if r == "" {
			r = "..."
		}
		if x.heard != x.full {
			r += " [interrupted]"
		}
		b.WriteString(t.l.TapeTurn("model", r))
	}
	sp, err := t.l.SpliceTape(t.cfg.System, b.String())
	if err != nil {
		return sp, fmt.Errorf("talk: %w", err)
	}
	return sp, nil
}

// sentenceEnd is the length of the first complete piece of s for the page's
// sentence chain (tts.js splitSentences: a piece ends at . ? ! and is at
// least 40 characters; shorter ones join the next). final: the whole rest
// is one piece. 0 = none complete yet.
func sentenceEnd(s string, final bool) int {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c != '.' && c != '?' && c != '!' {
			continue
		}
		j := i + 1
		for j < len(s) && strings.ContainsRune(".?!\"')]", rune(s[j])) {
			j++
		}
		if j >= len(s) {
			break // the end of what is generated so far: wait for what follows
		}
		if s[j] != ' ' || len(strings.TrimSpace(s[:j])) < 40 {
			continue
		}
		return j + 1
	}
	if final {
		return len(s)
	}
	return 0
}
