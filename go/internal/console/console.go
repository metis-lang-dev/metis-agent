// Package console is metis-agent's console backend (docs/FRAMEWORK.md §5):
// the static UI (fsttm-web web/) and one wire, JSON text frames on /ws.
// One agent, one session, N viewers of it. No audio and no floor: the page
// owns both.
//
//	→ page     intent{id,intent,voice,t,…} · talk{id,text,done,t} · narration{id,path,kind,form,line,sentence,t}
//	           runtime{kind,path,fact,narrated,sig,t} · rack{paths,last,t} · command{id,name,args,t}
//	           pending{id,state,t} · domains{domains,t} · error{id,error,t}
//	→ backend  say_text{id,text,mode} (mode intent | talk) · cut{id,heard} · heard{id,text} · task{id,result,detail}
//	           say{text} · runtime_intent{text} · rack_mount{text,state}
package console

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/metis-lang-dev/metis-agent/go/internal/agent"
)

// In is one message from the page.
type In struct {
	Ev     string `json:"ev"`
	ID     string `json:"id"`
	Text   string `json:"text"`
	Mode   string `json:"mode"`
	Heard  string `json:"heard"`
	Result string `json:"result"`
	Detail string `json:"detail"`
	State  string `json:"state"`
	// agent-console (s-7f3d1088): world{domain, atoms}, activate{domain,
	// bindings} / deactivate{domain}, hello{since}
	Domain   string          `json:"domain"`
	Atoms    []string        `json:"atoms"`
	Bindings json.RawMessage `json:"bindings"`
	Since    int             `json:"since"`
	At       time.Time       `json:"-"` // when the frame arrived
}

type client struct {
	conn *websocket.Conn
	send chan []byte
}

// Server is the console backend. Handle receives every page message.
type Server struct {
	mu      sync.Mutex
	clients map[*client]bool
	sticky  map[string][]byte // the last rack and domains frames, for late joiners
	uiDir   string
	up      websocket.Upgrader

	Handle func(In)
	Hello  func(In) []agent.V3Frame // hello{since}: the frames for that page alone (nil: hello ignored)
	joined chan struct{}            // closed at the first viewer
	once   sync.Once
	LibDir string // served at /lib/ (fsttm-web's lib/); "" = <ui-dir>/../lib when it exists
	// WidgetDir is served at /sdk/catalog/: the loop SDK's CATALOG widget
	// (catalog.js, catalog.css; catalog.WidgetDir), the list both consoles show
	WidgetDir string
}

func New(uiDir string) *Server {
	lib := ""
	if uiDir != "" {
		if st, err := os.Stat(filepath.Join(uiDir, "..", "lib", "dialog.js")); err == nil && !st.IsDir() {
			lib = filepath.Join(uiDir, "..", "lib")
		}
	}
	return &Server{LibDir: lib, joined: make(chan struct{}), clients: map[*client]bool{}, sticky: map[string][]byte{}, uiDir: uiDir,
		up: websocket.Upgrader{ReadBufferSize: 1 << 16, WriteBufferSize: 1 << 16,
			CheckOrigin: func(*http.Request) bool { return true }}}
}

// Handler serves the UI at / and the wire at /ws.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.ws)
	if s.LibDir != "" {
		// the fsttm-web library beside the page (its import map points at
		// lib/): the repo's lib/, which llama-server needs copied in
		mux.Handle("/lib/", http.StripPrefix("/lib/", noCache(http.FileServer(http.Dir(s.LibDir)))))
	}
	if s.WidgetDir != "" {
		mux.Handle("/sdk/catalog/", http.StripPrefix("/sdk/catalog/", noCache(http.FileServer(http.Dir(s.WidgetDir)))))
	}
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
		mux.HandleFunc("/", func(rw http.ResponseWriter, _ *http.Request) {
			rw.Header().Set("Content-Type", "text/plain")
			_, _ = rw.Write([]byte("metis-agent: /ws; start with -ui-dir <fsttm-web>/web to serve the page\n"))
		})
	}
	return mux
}

func (s *Server) ws(rw http.ResponseWriter, r *http.Request) {
	conn, err := s.up.Upgrade(rw, r, nil)
	if err != nil {
		return
	}
	c := &client{conn: conn, send: make(chan []byte, 256)}
	s.mu.Lock()
	s.clients[c] = true
	var late [][]byte
	for _, k := range []string{"domains", "rack"} {
		if b := s.sticky[k]; b != nil {
			late = append(late, b)
		}
	}
	s.mu.Unlock()
	for _, b := range late {
		c.send <- b
	}
	log.Printf("[console] viewer connected (%s)", r.RemoteAddr)
	s.once.Do(func() { close(s.joined) })
	go func() {
		for b := range c.send {
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if conn.WriteMessage(websocket.TextMessage, b) != nil {
				return
			}
		}
	}()
	defer func() {
		s.mu.Lock()
		delete(s.clients, c)
		s.mu.Unlock()
		close(c.send)
		conn.Close()
		log.Printf("[console] viewer gone (%s)", r.RemoteAddr)
	}()
	for {
		_, b, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var m In
		if json.Unmarshal(b, &m) != nil || m.Ev == "" {
			continue
		}
		m.At = time.Now()
		if m.Ev == "hello" && s.Hello != nil { // answered to this page alone, in order
			if !s.replay(c, s.Hello(m)) {
				return
			}
			continue
		}
		if s.Handle != nil {
			s.Handle(m)
		}
	}
}

func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Cache-Control", "no-cache")
		h.ServeHTTP(rw, r)
	})
}

// replay sends frames to one page, waiting for its writer (a gone page
// ends it: false).
func (s *Server) replay(c *client, fr []agent.V3Frame) bool {
	for _, f := range fr {
		m := map[string]any{"ev": f.Ev, "t": now()}
		for k, v := range f.F {
			m[k] = v
		}
		b, _ := json.Marshal(m)
		select {
		case c.send <- b:
		case <-time.After(10 * time.Second):
			return false
		}
	}
	return true
}

// Joined is closed when the first viewer connects.
func (s *Server) Joined() <-chan struct{} { return s.joined }

func now() float64 { return float64(time.Now().UnixNano()) / 1e9 }

// Send broadcasts one frame {ev, …fields, t}.
func (s *Server) Send(ev string, fields map[string]any) {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["ev"], fields["t"] = ev, now()
	b, _ := json.Marshal(fields)
	s.mu.Lock()
	if ev == "rack" || ev == "domains" {
		s.sticky[ev] = b
	}
	for c := range s.clients {
		select {
		case c.send <- b:
		default: // a slow viewer must not stall the agent
		}
	}
	s.mu.Unlock()
}

// ── agent.Sink ────────────────────────────────────────────────────────────────

var _ agent.Sink = (*Server)(nil)

func (s *Server) Command(id, name string, args map[string]any) {
	if args == nil {
		args = map[string]any{}
	}
	s.Send("command", map[string]any{"id": id, "name": name, "args": args})
}

func (s *Server) Runtime(kind, path, fact string, narrated bool, sig json.RawMessage) {
	s.Send("runtime", map[string]any{"kind": kind, "path": path, "fact": fact, "narrated": narrated, "sig": sig})
}

func (s *Server) Rack(paths any, last string) {
	s.Send("rack", map[string]any{"paths": paths, "last": last})
}

func (s *Server) Pending(id, state string) {
	s.Send("pending", map[string]any{"id": id, "state": state})
}

func (s *Server) Domains(ds []agent.DomainInfo) {
	s.Send("domains", map[string]any{"domains": ds})
}

// ── the agent's answers ───────────────────────────────────────────────────────

// Intent answers say_text{mode: intent}: the intent JSON as an object, the
// voice, and the model's timings (the parity run's fields).
func (s *Server) Intent(id string, t agent.Turn) {
	f := map[string]any{"id": id, "intent": json.RawMessage(t.JSON), "voice": t.Voice}
	if t.Say > 0 {
		f["say"] = t.Say
	}
	if !json.Valid([]byte(t.JSON)) {
		f["intent"] = t.JSON
	}
	if t.R != nil {
		f["json_text"] = t.R.JSON
		f["t_eval_ms"] = t.R.TEval.Milliseconds()
		f["t_json_ms"] = t.R.TJSON.Milliseconds()
		f["t_tts_ms"] = t.R.TTTS.Milliseconds()
		f["model_voice"] = t.R.Voice
	}
	s.Send("intent", f)
}

func (s *Server) Talk(id, text string, done bool) {
	s.Send("talk", map[string]any{"id": id, "text": text, "done": done})
}

func (s *Server) Narration(n agent.Narration) {
	f := map[string]any{"id": n.ID, "path": n.Path, "kind": n.Kind, "form": n.Form,
		"line": n.Line, "sentence": n.Sentence}
	if n.Say > 0 {
		f["say"] = n.Say
	}
	s.Send("narration", f)
}

func (s *Server) Error(id string, err error) {
	s.Send("error", map[string]any{"id": id, "error": err.Error()})
}
