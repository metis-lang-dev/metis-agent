package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/metis-lang-dev/metis-agent/go/internal/tts"
)

type wsc struct {
	t    *testing.T
	conn *websocket.Conn
}

func dial(t *testing.T, srv *httptest.Server, path string) *wsc {
	t.Helper()
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + path
	c, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return &wsc{t: t, conn: c}
}

func (c *wsc) send(m map[string]any) {
	b, _ := json.Marshal(m)
	if err := c.conn.WriteMessage(websocket.TextMessage, b); err != nil {
		c.t.Fatal(err)
	}
}

// next reads frames until one JSON event with ev==want (or any when want=="")
// arrives; binary frames are returned as {"_bin": len}.
func (c *wsc) next(want string) map[string]any {
	c.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		mt, b, err := c.conn.ReadMessage()
		if err != nil {
			c.t.Fatalf("waiting for %q: %v", want, err)
		}
		if mt == websocket.BinaryMessage {
			if want == "_bin" {
				return map[string]any{"_bin": len(b)}
			}
			continue
		}
		var m map[string]any
		if json.Unmarshal(b, &m) != nil {
			c.t.Fatalf("bad frame %q", b)
		}
		if want == "" || m["ev"] == want {
			return m
		}
	}
}

func newSrv(t *testing.T) (*Server, *httptest.Server) {
	s := NewServer("")
	h := httptest.NewServer(s.Handler())
	t.Cleanup(h.Close)
	return s, h
}

func TestSessionEventsAndCallbacks(t *testing.T) {
	s, h := newSrv(t)
	var mu sync.Mutex
	var said, announced []string
	var tasks []Task
	var attn []bool
	s.OnSayText = func(x string) { mu.Lock(); said = append(said, x); mu.Unlock() }
	s.OnSay = func(x string) { mu.Lock(); announced = append(announced, x); mu.Unlock() }
	s.OnTask = func(x Task) { mu.Lock(); tasks = append(tasks, x); mu.Unlock() }
	s.OnAttention = func(a bool) { mu.Lock(); attn = append(attn, a); mu.Unlock() }
	s.DialogState("AWAKE") // before anyone connects: cached for late joiners

	c := dial(t, h, "/ws")
	if m := c.next("role"); m["master"] != false {
		t.Errorf("first role %v", m)
	}
	if m := c.next("state"); m["dialog"] != "AWAKE" {
		t.Errorf("late joiner did not get the cached state: %v", m)
	}
	c.send(map[string]any{"ev": "say_text", "text": "sit down"})
	c.send(map[string]any{"ev": "say", "text": "Battery low."})
	c.send(map[string]any{"ev": "attention", "state": "ASLEEP"})
	c.send(map[string]any{"ev": "task", "id": "c-1", "result": "refused", "detail": "not armed"})
	c.send(map[string]any{"ev": "bogus"})
	if m := c.next("denied"); m["what"] != "bogus" {
		t.Errorf("denied %v", m)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(said) != 1 || said[0] != "sit down" {
		t.Errorf("say_text %v", said)
	}
	if len(announced) != 1 || announced[0] != "Battery low." {
		t.Errorf("say %v", announced)
	}
	if len(attn) != 1 || attn[0] != false {
		t.Errorf("attention %v", attn)
	}
	if len(tasks) != 1 || tasks[0] != (Task{ID: "c-1", Result: "refused", Detail: "not armed"}) {
		t.Errorf("task %v", tasks)
	}
}

func TestEventsOutAndCommand(t *testing.T) {
	s, h := newSrv(t)
	c := dial(t, h, "/ws")
	c.next("role")
	s.Transcript("go to the kitchen")
	s.Intent(json.RawMessage(`{"intent":"NAVIGATE"}`), "On it.")
	s.Reply("On it.")
	s.Command("c-9", "NAVIGATE", map[string]any{"position": []float64{1, 2, 0}})
	s.Command("c-10", "STOP", nil)
	if m := c.next("transcript"); m["text"] != "go to the kitchen" || m["t"] == nil {
		t.Errorf("transcript %v", m)
	}
	if m := c.next("intent"); m["intent"].(map[string]any)["intent"] != "NAVIGATE" || m["voice"] != "On it." {
		t.Errorf("intent %v", m)
	}
	if m := c.next("reply"); m["text"] != "On it." {
		t.Errorf("reply %v", m)
	}
	if m := c.next("command"); m["id"] != "c-9" || m["name"] != "NAVIGATE" || m["args"] == nil {
		t.Errorf("command %v", m)
	}
	if m := c.next("command"); m["name"] != "STOP" || len(m["args"].(map[string]any)) != 0 {
		t.Errorf("command without args must carry {}: %v", m)
	}
}

func TestMicTokenHandshakeAndFrames(t *testing.T) {
	s, h := newSrv(t)
	a := dial(t, h, "/ws")
	a.next("role")
	// a stream without a token is cut on its first frame
	bad := dial(t, h, "/ws_mic?tok=nope")
	_ = bad.conn.WriteMessage(websocket.BinaryMessage, make([]byte, 640))
	bad.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := bad.conn.ReadMessage(); err == nil {
		t.Error("stale-token mic stream should be closed by the server")
	}
	a.send(map[string]any{"ev": "grab_master"})
	var tok string
	for i := 0; i < 3; i++ {
		m := a.next("role")
		if m["master"] == true {
			tok = m["mic_tok"].(string)
			break
		}
	}
	if tok == "" {
		t.Fatal("no mic_tok after grab_master")
	}
	mic := dial(t, h, "/ws_mic?tok="+tok)
	// 1280 bytes = two frames; 320 + 320 = one frame re-cut
	_ = mic.conn.WriteMessage(websocket.BinaryMessage, make([]byte, 1280))
	_ = mic.conn.WriteMessage(websocket.BinaryMessage, make([]byte, 320))
	_ = mic.conn.WriteMessage(websocket.BinaryMessage, make([]byte, 320))
	for i := 0; i < 3; i++ {
		select {
		case f := <-s.Mic():
			if len(f) != 640 {
				t.Errorf("frame %d bytes", len(f))
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("frame %d never arrived", i)
		}
	}
	// a second session grabbing rotates the token: the first mic dies
	b := dial(t, h, "/ws")
	b.next("role")
	b.send(map[string]any{"ev": "grab_master"})
	mic.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := mic.conn.ReadMessage(); err == nil {
		t.Error("old mic stream should be cut when the token rotates")
	}
	told := false
	for i := 0; i < 4 && !told; i++ { // skip the claimed broadcasts still queued
		m := a.next("role")
		told = m["master"] == false && m["reason"] != nil
	}
	if !told {
		t.Error("previous holder not told that the mic was taken")
	}
}

// fake synthesizer: 24 kHz, 1 ms of audio per character
type fakeSynth struct {
	mu       sync.Mutex
	cancelCh chan struct{}
}

func (f *fakeSynth) Synthesize(_ context.Context, text string) (tts.Audio, error) {
	return tts.Audio{PCM: make([]int16, 24*len(text)), Rate: 24000}, nil
}
func (f *fakeSynth) Cancel() {}

func TestWsSpeakerBlocksOnTTSDone(t *testing.T) {
	s, h := newSrv(t)
	spk := NewWsSpeaker(s, &fakeSynth{}, 5*time.Second)
	// no listener: instant, completed
	r, err := spk.Speak(context.Background(), "hello")
	if err != nil || !r.Completed || r.Played != r.Synthesized {
		t.Fatalf("no-listener result %+v %v", r, err)
	}
	c := dial(t, h, "/ws")
	c.next("role")
	done := make(chan tts.SpeakResult, 1)
	go func() {
		r, _ := spk.Speak(context.Background(), strings.Repeat("x", 300)) // 300 ms
		done <- r
	}()
	hdr := c.next("tts")
	id := hdr["id"].(string)
	if hdr["rate"] != 24000.0 || hdr["samples"] != 7200.0 {
		t.Errorf("tts header %v", hdr)
	}
	if b := c.next("_bin"); b["_bin"] != 4800 { // 100 ms chunk = 2400 samples
		t.Errorf("first chunk %v", b)
	}
	if m := c.next("tts_end"); m["id"] != id {
		t.Errorf("tts_end %v", m)
	}
	select {
	case <-done:
		t.Fatal("Speak returned before tts_done")
	case <-time.After(400 * time.Millisecond):
	}
	c.send(map[string]any{"ev": "tts_done", "id": id, "played_ms": 300})
	select {
	case r := <-done:
		if !r.Completed || r.Played != 300*time.Millisecond || r.Synthesized != 300*time.Millisecond {
			t.Errorf("result %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Speak did not return on tts_done")
	}
}

func TestWsSpeakerCancelSendsStop(t *testing.T) {
	s, h := newSrv(t)
	spk := NewWsSpeaker(s, &fakeSynth{}, 5*time.Second)
	c := dial(t, h, "/ws")
	c.next("role")
	done := make(chan tts.SpeakResult, 1)
	go func() {
		r, _ := spk.Speak(context.Background(), strings.Repeat("x", 1000)) // 1 s
		done <- r
	}()
	id := c.next("tts")["id"].(string)
	c.next("tts_end")
	time.Sleep(100 * time.Millisecond)
	spk.Cancel()
	if m := c.next("tts_stop"); m["id"] != id {
		t.Errorf("tts_stop %v", m)
	}
	c.send(map[string]any{"ev": "tts_done", "id": id, "played_ms": 120})
	select {
	case r := <-done:
		if r.Completed || r.Played != 120*time.Millisecond || r.Synthesized != time.Second {
			t.Errorf("cancel result %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Speak did not return after Cancel")
	}
}

func TestWsSpeakerTimeout(t *testing.T) {
	s, h := newSrv(t)
	spk := NewWsSpeaker(s, &fakeSynth{}, 100*time.Millisecond)
	c := dial(t, h, "/ws")
	c.next("role")
	t0 := time.Now()
	r, err := spk.Speak(context.Background(), strings.Repeat("x", 200)) // 200 ms + 100 margin
	if err != nil || !r.Completed {
		t.Fatalf("timeout result %+v %v", r, err)
	}
	if el := time.Since(t0); el < 250*time.Millisecond || el > 2*time.Second {
		t.Errorf("returned after %s, want ≈300ms", el)
	}
	_ = http.StatusOK
}

func TestPCMChunkStartingWithBraceStaysBinary(t *testing.T) {
	s, h := newSrv(t)
	c := dial(t, h, "/ws")
	c.next("role")
	pcm := make([]int16, 2400) // one 100 ms chunk at 24 kHz
	pcm[0] = 0x007b            // low byte '{' — the old first-byte heuristic sent this as text
	go s.SendTTS("u-1", 24000, pcm)
	c.next("tts")
	if b := c.next("_bin"); b["_bin"] != 4800 {
		t.Fatalf("chunk lost or not binary: %v", b)
	}
	c.next("tts_end")
}

// The rack pane (rack step 2): the last pane is replayed to a late joiner,
// and the pane's switches come back as rack_mount{text: path, state}.
func TestRackPaneAndMount(t *testing.T) {
	s, h := newSrv(t)
	type mount struct {
		path string
		on   bool
	}
	got := make(chan mount, 2)
	s.OnRackMount = func(p string, on bool) { got <- mount{p, on} }
	s.Rack([]map[string]any{{"path": "kitchen", "mounted": true}}, "event(kitchen): 3 records, 90 tok")
	c := dial(t, h, "/ws")
	if m := c.next("rack"); m["last"] != "event(kitchen): 3 records, 90 tok" || len(m["paths"].([]any)) != 1 {
		t.Errorf("late joiner's rack pane %v", m)
	}
	c.send(map[string]any{"ev": "rack_mount", "text": "kitchen_shared", "state": "off"})
	c.send(map[string]any{"ev": "rack_mount", "text": "kitchen", "state": "maybe"})
	if m := c.next("denied"); m["what"] != "rack_mount" {
		t.Errorf("a bad state was not denied: %v", m)
	}
	select {
	case m := <-got:
		if m != (mount{"kitchen_shared", false}) {
			t.Errorf("mount %v", m)
		}
	case <-time.After(time.Second):
		t.Fatal("no rack_mount")
	}
}
