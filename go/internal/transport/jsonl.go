// Package transport is the engine's mouth toward whoever hosts it. The
// vocabulary is the jsonl `ev` set fsttm-go spoke to its robot relay; the
// websocket transport of HARNESS.md §6 uses the same names, so the two
// transports stay one vocabulary.
//
// OUT (one JSON object per line on the writer; everything else to stderr):
//
//	{"ev":"intent","intent":{...},"voice":"..."}
//	{"ev":"transcript","text":"..."}      every utterance the floor accepted
//	{"ev":"reply","text":"..."}           what is actually spoken
//	{"ev":"state","dialog":"AWAKE"}       ASLEEP | AWAKE | LISTENING | SPEAKING
//
// IN (ReadSay on a reader):
//
//	{"ev":"say","text":"Battery low."}    → Engine.Announce
package transport

import (
	"bufio"
	"encoding/json"
	"io"
	"log"
	"os"
	"sync"
)

// Transport is the seam the harness wires Engine.Events into. Step 2 adds
// the websocket implementation (and the typed command/task pair).
type Transport interface {
	Intent(intentJSON any, voice string)
	Transcript(text string)
	Reply(text string)
	DialogState(state string)
	// Pending reports a command's lifecycle: expired | progress | answered |
	// late — the UI greys an expired row and marks late outcomes.
	Pending(id, state string)
}

// Jsonl writes events as JSON lines — the SIL / headless transport.
type Jsonl struct {
	mu  sync.Mutex
	out io.Writer
}

var _ Transport = (*Jsonl)(nil)

func NewJsonl(w io.Writer) *Jsonl {
	if w == nil {
		w = os.Stdout
	}
	return &Jsonl{out: w}
}

func (l *Jsonl) emit(v map[string]any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.out.Write(append(b, '\n'))
}

func (l *Jsonl) Intent(intentJSON any, voice string) {
	l.emit(map[string]any{"ev": "intent", "intent": intentJSON, "voice": voice})
}
func (l *Jsonl) Transcript(text string) { l.emit(map[string]any{"ev": "transcript", "text": text}) }
func (l *Jsonl) Reply(text string)      { l.emit(map[string]any{"ev": "reply", "text": text}) }
func (l *Jsonl) DialogState(state string) {
	l.emit(map[string]any{"ev": "state", "dialog": state})
}
func (l *Jsonl) Pending(id, state string) {
	l.emit(map[string]any{"ev": "pending", "id": id, "state": state})
}

// ReadSay consumes `say` events from r; each text goes to announce
// (Engine.Announce — spoken only on a free floor). Blocks; run as a
// goroutine. Returns on EOF.
func (l *Jsonl) ReadSay(r io.Reader, announce func(string)) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		var ev struct {
			Ev   string `json:"ev"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		if ev.Ev == "say" && ev.Text != "" {
			log.Printf("[transport] say → announce %q", ev.Text)
			announce(ev.Text)
		}
	}
}
