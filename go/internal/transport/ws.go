// The websocket transport (HARNESS.md §5, §6): one process serves the UI
// (-ui-dir), /ws for JSON events both ways, /ws_mic for 20 ms PCM frames,
// and streams TTS audio as binary frames. Ported from nina_splat's hub.go +
// web.go: the client/push drop-when-full fan-out, the mic token handshake
// (one mic per session, re-issued on each grab, stale sockets closed), the
// static FileServer. Dropped: COOP/COEP, bundles, pano, fpv/voxel lanes.
//
// Wire (text frames are JSON with "ev"; names = the jsonl vocabulary):
//
//	→ browser  state{dialog} · transcript{text,t} · reply{text,t} · intent{intent,voice,t}
//	           command{id,name,args} · pending{id,state} · tts{id,rate,channels,format} + binary PCM
//	           chunks + tts_end{id} · tts_stop{id} · role{master,mic_tok} · denied{what}
//	           runtime{kind,path,fact,narrated,sig,t} (internal/kernel: every runtime signal)
//	→ server   say_text{text} · attention{state} · task{id,result,detail} (result also
//	           "progress": re-arms the command's window) · say{text}
//	           tts_done{id,played_ms} · grab_master · release_master
//	           runtime_intent{text} (a runtime intent, e.g. "reset 0", when a kernel is attached)
//	/ws_mic?tok=…   binary 640-byte s16le 16 kHz frames (multiples accepted, re-cut)
package transport

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const micFrameBytes = 640 // 20 ms of s16le mono at 16 kHz (vad.FrameBytes)

// frame carries its websocket message type explicitly: nina_splat guessed
// text vs binary from the first byte, and a PCM chunk starting with '{'
// (1 in 128) went out as text and was lost by the player.
type frame struct {
	mt int
	b  []byte
}

type client struct {
	conn *websocket.Conn
	send chan frame
	mu   sync.Mutex
}

func (c *client) push(b []byte) { c.pushMT(websocket.TextMessage, b) }

func (c *client) pushMT(mt int, b []byte) {
	select {
	case c.send <- frame{mt, b}:
	default: // a slow viewer must not stall the engine
	}
}

// Task is the host's answer to a command (HARNESS.md §4).
type Task struct {
	ID     string `json:"id"`
	Result string `json:"result"` // done | failed | refused | expired
	Detail string `json:"detail"`
}

type ttsAck struct {
	played time.Duration
}

// Server is the ws transport. Set the On* callbacks before serving.
type Server struct {
	mu      sync.Mutex
	clients map[*client]bool
	master  *client
	micTok  string
	mics    map[*websocket.Conn]string
	dialog  string // cached for late joiners
	rack    []byte // the last rack pane, likewise
	acks    map[string]chan ttsAck
	mic     chan []byte
	dropped int

	uiDir string
	up    websocket.Upgrader

	OnSayText       func(text string)
	OnAttention     func(awake bool)
	OnTask          func(t Task)
	OnRuntimeIntent func(text string)          // runtime_intent{text}: a runtime intent down to the kernel
	OnRackMount     func(path string, on bool) // rack_mount{text: path, state: on|off}: the rack pane's switches
	OnSay           func(text string)
	OnMaster        func(claimed bool)
}

var _ Transport = (*Server)(nil)

func NewServer(uiDir string) *Server {
	return &Server{
		clients: map[*client]bool{}, mics: map[*websocket.Conn]string{},
		acks: map[string]chan ttsAck{}, mic: make(chan []byte, 64), uiDir: uiDir,
		up: websocket.Upgrader{ReadBufferSize: 1 << 16, WriteBufferSize: 1 << 16,
			CheckOrigin: func(*http.Request) bool { return true }},
	}
}

func jmsg(m map[string]any) []byte { b, _ := json.Marshal(m); return b }

func newTok() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Mic is the remote microphone: 640-byte frames from the session holding
// the mic token, for the same vad.Segmenter local capture feeds.
func (s *Server) Mic() <-chan []byte { return s.mic }

// Handler serves the UI at / (when uiDir is set), /ws and /ws_mic.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.wsCtrl)
	mux.HandleFunc("/ws_mic", s.wsMic)
	if s.uiDir != "" {
		fs := http.FileServer(http.Dir(s.uiDir))
		mux.Handle("/", http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			rw.Header().Set("Cache-Control", "no-cache")
			if r.URL.Path == "/" {
				http.ServeFile(rw, r, filepath.Join(s.uiDir, "index.html"))
				return
			}
			fs.ServeHTTP(rw, r)
		}))
	} else {
		mux.HandleFunc("/", func(rw http.ResponseWriter, r *http.Request) {
			rw.Header().Set("Content-Type", "text/plain")
			_, _ = rw.Write([]byte("metis-agent harness: /ws (events), /ws_mic?tok= (audio); " +
				"start with -ui-dir to serve a UI\n"))
		})
	}
	return mux
}

// ── Transport (events out) ────────────────────────────────────────────────────

func now() float64 { return float64(time.Now().UnixNano()) / 1e9 }

func (s *Server) Intent(intentJSON any, voice string) {
	s.broadcast(jmsg(map[string]any{"ev": "intent", "intent": intentJSON, "voice": voice, "t": now()}))
}
func (s *Server) Transcript(text string) {
	s.broadcast(jmsg(map[string]any{"ev": "transcript", "text": text, "t": now()}))
}
func (s *Server) Reply(text string) {
	s.broadcast(jmsg(map[string]any{"ev": "reply", "text": text, "t": now()}))
}
func (s *Server) DialogState(state string) {
	s.mu.Lock()
	s.dialog = state
	s.mu.Unlock()
	s.broadcast(jmsg(map[string]any{"ev": "state", "dialog": state}))
}

// Pending tells the UI where a command stands (expired rows go grey).
func (s *Server) Pending(id, state string) {
	s.broadcast(jmsg(map[string]any{"ev": "pending", "id": id, "state": state, "t": now()}))
}

// Runtime shows one runtime signal in the UI's events pane: the adapter's
// fact sentence, whether the dialog narrates it, and the signal verbatim.
func (s *Server) Runtime(kind, path, fact string, narrated bool, sig json.RawMessage) {
	s.broadcast(jmsg(map[string]any{"ev": "runtime", "kind": kind, "path": path, "fact": fact,
		"narrated": narrated, "sig": sig, "t": now()}))
}

// Rack shows the rack pane: per domain, mounted, records, open surprises,
// last said (internal/rack Snapshot), and the last insertion's trigger.
func (s *Server) Rack(paths any, last string) {
	b := jmsg(map[string]any{"ev": "rack", "paths": paths, "last": last, "t": now()})
	s.mu.Lock()
	s.rack = b
	s.mu.Unlock()
	s.broadcast(b)
}

// Command is the wire.Emitter: the typed command the host would execute.
func (s *Server) Command(id, name string, args map[string]any) {
	if args == nil {
		args = map[string]any{}
	}
	s.broadcast(jmsg(map[string]any{"ev": "command", "id": id, "name": name,
		"args": args, "t": now()}))
}

// ── TTS lane ─────────────────────────────────────────────────────────────────

// Listeners reports whether anyone would hear a TTS stream.
func (s *Server) Listeners() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients)
}

// SendTTS streams one utterance: tts{id} header, binary s16le chunks of
// ~100 ms, tts_end{id}. Audio follows the mic token (the session talking
// to the harness hears the reply); with no holder every viewer gets it.
// Returns the channel tts_done{id} arrives on.
func (s *Server) SendTTS(id string, rate int, pcm []int16) <-chan ttsAck {
	ack := make(chan ttsAck, 1)
	s.mu.Lock()
	s.acks[id] = ack
	target := s.master
	s.mu.Unlock()
	send := func(mt int, b []byte) {
		if target != nil {
			target.pushMT(mt, b)
		} else {
			s.broadcastMT(mt, b)
		}
	}
	send(websocket.TextMessage, jmsg(map[string]any{"ev": "tts", "id": id, "rate": rate,
		"channels": 1, "format": "s16le", "samples": len(pcm)}))
	chunk := rate / 10 // 100 ms
	if chunk <= 0 {
		chunk = 2400
	}
	for off := 0; off < len(pcm); off += chunk {
		end := off + chunk
		if end > len(pcm) {
			end = len(pcm)
		}
		b := make([]byte, 2*(end-off))
		for i, v := range pcm[off:end] {
			b[2*i], b[2*i+1] = byte(v), byte(v>>8)
		}
		send(websocket.BinaryMessage, b)
	}
	send(websocket.TextMessage, jmsg(map[string]any{"ev": "tts_end", "id": id}))
	return ack
}

// SendTTSStop tells the player to flush (barge-in); the ack still arrives
// as tts_done{id, played_ms}.
func (s *Server) SendTTSStop(id string) {
	s.broadcast(jmsg(map[string]any{"ev": "tts_stop", "id": id}))
}

// ForgetTTS drops the ack channel once Speak is done with it.
func (s *Server) ForgetTTS(id string) {
	s.mu.Lock()
	delete(s.acks, id)
	s.mu.Unlock()
}

// ── sessions ─────────────────────────────────────────────────────────────────

func (s *Server) broadcast(b []byte) { s.broadcastMT(websocket.TextMessage, b) }

func (s *Server) broadcastMT(mt int, b []byte) {
	s.mu.Lock()
	cs := make([]*client, 0, len(s.clients))
	for c := range s.clients {
		cs = append(cs, c)
	}
	s.mu.Unlock()
	for _, c := range cs {
		c.pushMT(mt, b)
	}
}

func (s *Server) serveConn(c *client) {
	for f := range c.send {
		c.mu.Lock()
		err := c.conn.WriteMessage(f.mt, f.b)
		c.mu.Unlock()
		if err != nil {
			return
		}
	}
}

func (s *Server) wsCtrl(rw http.ResponseWriter, r *http.Request) {
	conn, err := s.up.Upgrade(rw, r, nil)
	if err != nil {
		return
	}
	c := &client{conn: conn, send: make(chan frame, 256)}
	s.mu.Lock()
	s.clients[c] = true
	dialog, claimed, rack := s.dialog, s.master != nil, s.rack
	s.mu.Unlock()
	log.Printf("[ws] viewer connected: %s", conn.RemoteAddr())
	go s.serveConn(c)
	c.push(jmsg(map[string]any{"ev": "role", "master": false, "claimed": claimed}))
	if dialog != "" {
		c.push(jmsg(map[string]any{"ev": "state", "dialog": dialog}))
	}
	if rack != nil {
		c.push(rack)
	}
	conn.SetReadLimit(1 << 20)
	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		if mt == websocket.TextMessage {
			s.onText(c, data)
		}
	}
	s.dropClient(c)
	close(c.send)
	conn.Close()
}

func (s *Server) dropClient(c *client) {
	s.mu.Lock()
	delete(s.clients, c)
	wasMaster := s.master == c
	if wasMaster {
		s.master, s.micTok = nil, ""
	}
	s.mu.Unlock()
	if wasMaster {
		log.Printf("[ws] mic holder %s disconnected", c.conn.RemoteAddr())
		s.dropStaleMics()
		s.broadcast(jmsg(map[string]any{"ev": "role", "claimed": false}))
		if s.OnMaster != nil {
			s.OnMaster(false)
		}
	}
}

func (s *Server) onText(c *client, data []byte) {
	var m struct {
		Ev       string          `json:"ev"`
		Text     string          `json:"text"`
		State    string          `json:"state"`
		ID       string          `json:"id"`
		Result   string          `json:"result"`
		Detail   string          `json:"detail"`
		PlayedMs float64         `json:"played_ms"`
		Raw      json.RawMessage `json:"-"`
	}
	if json.Unmarshal(data, &m) != nil {
		return
	}
	switch m.Ev {
	case "grab_master":
		// taking the mic always succeeds; the previous holder is told and
		// its stream dies with the token rotation
		s.mu.Lock()
		prev := s.master
		tok := newTok()
		s.master, s.micTok = c, tok
		s.mu.Unlock()
		if prev != nil && prev != c {
			prev.push(jmsg(map[string]any{"ev": "role", "master": false,
				"reason": "mic taken by " + c.conn.RemoteAddr().String()}))
		}
		s.dropStaleMics()
		c.push(jmsg(map[string]any{"ev": "role", "master": true, "mic_tok": tok}))
		s.broadcast(jmsg(map[string]any{"ev": "role", "claimed": true}))
		if s.OnMaster != nil {
			s.OnMaster(true)
		}
	case "release_master":
		s.mu.Lock()
		if s.master == c {
			s.master, s.micTok = nil, ""
		}
		s.mu.Unlock()
		s.dropStaleMics()
		s.broadcast(jmsg(map[string]any{"ev": "role", "claimed": false}))
		if s.OnMaster != nil {
			s.OnMaster(false)
		}
	case "say_text":
		if m.Text != "" && s.OnSayText != nil {
			s.OnSayText(m.Text)
		}
	case "say":
		if m.Text != "" && s.OnSay != nil {
			s.OnSay(m.Text)
		}
	case "attention":
		if s.OnAttention != nil && (m.State == "AWAKE" || m.State == "ASLEEP") {
			s.OnAttention(m.State == "AWAKE")
		}
	case "runtime_intent":
		if m.Text != "" && s.OnRuntimeIntent != nil {
			s.OnRuntimeIntent(m.Text)
		} else {
			c.push(jmsg(map[string]any{"ev": "denied", "what": m.Ev}))
		}
	case "rack_mount":
		if m.Text != "" && s.OnRackMount != nil && (m.State == "on" || m.State == "off") {
			s.OnRackMount(m.Text, m.State == "on")
		} else {
			c.push(jmsg(map[string]any{"ev": "denied", "what": m.Ev}))
		}
	case "task":
		if m.ID != "" && s.OnTask != nil {
			s.OnTask(Task{ID: m.ID, Result: m.Result, Detail: m.Detail})
		}
	case "tts_done":
		s.mu.Lock()
		ack := s.acks[m.ID]
		s.mu.Unlock()
		if ack != nil {
			select {
			case ack <- ttsAck{played: time.Duration(m.PlayedMs * float64(time.Millisecond))}:
			default:
			}
		}
	default:
		c.push(jmsg(map[string]any{"ev": "denied", "what": m.Ev}))
	}
}

// ── mic lane ─────────────────────────────────────────────────────────────────

func (s *Server) micTokOK(tok string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return tok != "" && tok == s.micTok
}

func (s *Server) dropStaleMics() {
	s.mu.Lock()
	var stale []*websocket.Conn
	for c, tok := range s.mics {
		if tok != s.micTok {
			stale = append(stale, c)
		}
	}
	s.mu.Unlock()
	for _, c := range stale {
		log.Printf("[ws] mic stream cut — token rotated (%s)", c.RemoteAddr())
		c.Close()
	}
}

func (s *Server) wsMic(rw http.ResponseWriter, r *http.Request) {
	tok := r.URL.Query().Get("tok")
	conn, err := s.up.Upgrade(rw, r, nil)
	if err != nil {
		return
	}
	log.Printf("[ws] mic stream opened (%s)", r.RemoteAddr)
	conn.SetReadLimit(4 << 20)
	s.mu.Lock()
	s.mics[conn] = tok
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.mics, conn)
		s.mu.Unlock()
		conn.Close()
		log.Printf("[ws] mic stream closed (%s)", r.RemoteAddr)
	}()
	var rest []byte
	warned := false
	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if !s.micTokOK(tok) {
			log.Printf("[ws] mic stream rejected — stale/no token (%s)", r.RemoteAddr)
			return
		}
		if mt != websocket.BinaryMessage {
			continue
		}
		// the worklet sends exact 640-byte frames; anything else is re-cut
		// (and said once) so the segmenter never sees a partial frame
		if len(data) != micFrameBytes && !warned {
			log.Printf("[ws] mic frames are %d bytes, expected %d — re-cutting", len(data), micFrameBytes)
			warned = true
		}
		rest = append(rest, data...)
		for len(rest) >= micFrameBytes {
			f := make([]byte, micFrameBytes)
			copy(f, rest[:micFrameBytes])
			rest = rest[micFrameBytes:]
			select {
			case s.mic <- f:
			default:
				s.mu.Lock()
				s.dropped++
				n := s.dropped
				s.mu.Unlock()
				if n%100 == 1 {
					log.Printf("[ws] mic frames dropped: %d (engine not draining)", n)
				}
			}
		}
	}
}
