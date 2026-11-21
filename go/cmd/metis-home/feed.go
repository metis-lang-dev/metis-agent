package main

// The home as a NAMED RUNTIME of the Metis surprise interface (metis-agent
// plugin/docs/SURPRISE-INTERFACE.md v1.4, RUNTIME-INTERFACE.md §5; the Go
// side of py metis_agent/runtime/feed.py): a Claude Code session with the
// Metis plugin in the home workspace attaches with `/metis-runtime home`.
//
//	discovery  <workspace>/.metis/runtimes/<name>.json {name, url, token, pid, domain, workspace, protocol, started}
//	GET  /feed?since=&client=   the backlog as JSON lines (plain long-free GET, as py's client reads it)
//	POST /signal                one envelope in: hello, bye, and the feedback types, relayed to the OTHER clients
//	GET  /status                the state and the hello
//
// Payload first, then the signal: a kernel surprise is written to the
// workspace by metispy's Workspace SDK (`python -m metis.catalog.workspace
// drop`, the one producer path), then signalled as surprise.new. No run
// control: the home runs as long as it lives.

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const protocol = 1

var feedback = map[string]bool{"surprise.taken": true, "surprise.tried": true, "surprise.ruled": true,
	"surprise.applied": true, "catalog.changed": true, "world.changed": true, "explain.answer": true,
	"explain.request": true}

// Feed is the runtime's side of the interface.
type Feed struct {
	Name, Domain, Workspace string
	Python                  string // the interpreter with metispy (the SDK's drop); "" = surprises signalled, not written
	OnSignal                func(sig map[string]any)

	mu            sync.Mutex
	seq           int
	backlog       []map[string]any
	clients       map[string]map[string]any
	counts        map[string]int
	restartNeeded bool
	token         string
	url           string
	discovery     string
	started       string
}

func now() string { return time.Now().Format("2006-01-02T15:04:05.000-07:00") }

var slugRe = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

func slug(s string) string { return strings.Trim(slugRe.ReplaceAllString(s, "-"), "-") }

// Emit is one of the runtime's own signals, kept in the backlog.
func (f *Feed) Emit(typ string, fields map[string]any) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	sig := map[string]any{"v": protocol, "seq": f.seq, "ts": now(), "type": typ, "from": "runtime:" + f.Name}
	for k, v := range fields {
		sig[k] = v
	}
	if typ != "status" {
		f.backlog = append(f.backlog, sig)
		if len(f.backlog) > 2000 {
			f.backlog = f.backlog[len(f.backlog)-2000:]
		}
	}
	return sig
}

func (f *Feed) hello() map[string]any {
	return map[string]any{"v": protocol, "type": "hello", "from": "runtime:" + f.Name, "name": f.Name, "domain": f.Domain,
		"workspace": f.Workspace, "protocol": protocol, "capabilities": []any{"explain"}}
}

func (f *Feed) state() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	cl := make([]string, 0, len(f.clients))
	for c := range f.clients {
		cl = append(cl, c)
	}
	sort.Strings(cl)
	counts := map[string]int{}
	for k, v := range f.counts {
		counts[k] = v
	}
	return map[string]any{"state": "running", "run": nil, "tick": nil, "counts": counts, "clients": cl,
		"restart_needed": f.restartNeeded}
}

// Surprise writes a kernel surprise into the workspace (the SDK's drop), then signals surprise.new.
func (f *Feed) Surprise(domain string, reply map[string]any) {
	f.mu.Lock()
	f.counts["surprise"]++
	f.mu.Unlock()
	rec := map[string]any{"member": domain, "who": "world", "source": "runtime:" + f.Name}
	if r, ok := reply["record"].(map[string]any); ok {
		for k, v := range r {
			if k != "id" && k != "ts" && k != "status" {
				rec[k] = v
			}
		}
	}
	if rec["reading"] == nil && rec["atoms"] == nil {
		if d, ok := reply["difference"]; ok {
			rec["reading"] = d
		} else {
			rec["reading"] = map[string]any{}
		}
	}
	rec["member"] = domain
	if f.Python == "" {
		f.Emit("surprise.new", map[string]any{"id": nil, "key": nil, "member": domain, "note": "not written: no SDK python"})
		return
	}
	b, _ := json.Marshal(rec)
	cmd := exec.Command(f.Python, "-m", "metis.catalog.workspace", "drop", "-", "--who", "world")
	cmd.Dir = f.Workspace
	cmd.Env = append(os.Environ(), "METIS_CATALOG="+f.Workspace)
	cmd.Stdin = strings.NewReader(string(b))
	out, err := cmd.Output()
	var got struct {
		ID string `json:"id"`
	}
	if err == nil {
		err = json.Unmarshal(lastLine(out), &got)
	}
	if err != nil || got.ID == "" {
		msg := fmt.Sprint(err)
		if ee, ok := err.(*exec.ExitError); ok {
			msg += ": " + strings.TrimSpace(string(ee.Stderr))
		}
		log.Printf("[runtime] surprise of %s not written: %s", domain, msg)
		f.Emit("finding.refused", map[string]any{"member": domain, "error": msg})
		return
	}
	f.Emit("surprise.new", map[string]any{"id": got.ID, "ref": ".metis/surprises/" + got.ID + ".json",
		"key": "home:" + domain + ":" + got.ID, "member": domain})
}

func lastLine(b []byte) []byte {
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	return []byte(lines[len(lines)-1])
}

func (f *Feed) signalIn(sig map[string]any) (int, map[string]any) {
	t, _ := sig["type"].(string)
	if t == "" {
		return 400, map[string]any{"ok": false, "error": "a signal is a JSON object with a type"}
	}
	if v, ok := sig["v"].(float64); ok && int(v) > protocol {
		return 400, map[string]any{"ok": false, "error": fmt.Sprintf("protocol %v: this runtime speaks %d", v, protocol)}
	}
	who := fmt.Sprint(sig["from"])
	switch {
	case t == "hello":
		f.mu.Lock()
		f.clients[who] = map[string]any{"since": now(), "hello": sig}
		f.mu.Unlock()
		out := map[string]any{"ok": true}
		for k, v := range f.hello() {
			out[k] = v
		}
		return 200, out
	case t == "bye":
		f.mu.Lock()
		delete(f.clients, who)
		f.mu.Unlock()
		return 200, map[string]any{"ok": true}
	case feedback[t]:
		if t == "world.changed" {
			f.mu.Lock()
			f.restartNeeded = true
			f.mu.Unlock()
		}
		if f.OnSignal != nil {
			f.OnSignal(sig)
		}
		f.mu.Lock()
		f.seq++
		relay := map[string]any{}
		for k, v := range sig {
			relay[k] = v
		}
		relay["relay_of"], relay["seq"] = sig["seq"], f.seq
		f.backlog = append(f.backlog, relay)
		f.mu.Unlock()
		return 200, map[string]any{"ok": true}
	}
	return 200, map[string]any{"ok": true, "ignored": t}
}

func (f *Feed) after(since int, client string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, s := range f.backlog {
		n, _ := s["seq"].(int)
		if n > since && (client == "" || s["from"] != client) {
			out = append(out, s)
		}
	}
	return out
}

func (f *Feed) authorized(r *http.Request) bool {
	got := r.URL.Query().Get("token")
	if a := r.Header.Get("Authorization"); got == "" && strings.HasPrefix(a, "Bearer ") {
		got = a[len("Bearer "):]
	}
	return f.token != "" && subtle.ConstantTimeCompare([]byte(got), []byte(f.token)) == 1
}

// Start serves on loopback with a fresh token and writes the discovery file.
func (f *Feed) Start(addr string) error {
	f.clients, f.counts = map[string]map[string]any{}, map[string]int{}
	tb := make([]byte, 24)
	rand.Read(tb)
	f.token = hex.EncodeToString(tb)
	f.started = now()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	f.url = "http://" + ln.Addr().String()
	mux := http.NewServeMux()
	writeJSON := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(v)
	}
	gate := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !f.authorized(r) {
				writeJSON(w, 401, map[string]any{"ok": false, "error": "the runtime's token is required"})
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("/feed", gate(func(w http.ResponseWriter, r *http.Request) {
		since, _ := strconv.Atoi(r.URL.Query().Get("since"))
		w.Header().Set("Content-Type", "application/x-ndjson")
		for _, s := range f.after(since, r.URL.Query().Get("client")) {
			b, _ := json.Marshal(s)
			w.Write(append(b, '\n'))
		}
	}))
	mux.HandleFunc("/signal", gate(func(w http.ResponseWriter, r *http.Request) {
		var sig map[string]any
		b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err := json.Unmarshal(b, &sig); err != nil {
			writeJSON(w, 400, map[string]any{"ok": false, "error": "a signal is a JSON object"})
			return
		}
		code, body := f.signalIn(sig)
		writeJSON(w, code, body)
	}))
	mux.HandleFunc("/status", gate(func(w http.ResponseWriter, r *http.Request) {
		out := map[string]any{"ok": true}
		for k, v := range f.state() {
			out[k] = v
		}
		for k, v := range f.hello() {
			out[k] = v
		}
		writeJSON(w, 200, out)
	}))
	go http.Serve(ln, mux)
	h := f.hello()
	delete(h, "v")
	delete(h, "type")
	delete(h, "from")
	f.Emit("hello", h)
	return f.discover()
}

func (f *Feed) discover() error {
	d := filepath.Join(f.Workspace, ".metis", "runtimes")
	if err := os.MkdirAll(d, 0o755); err != nil {
		return err
	}
	f.discovery = filepath.Join(d, slug(f.Name)+".json")
	tmp := filepath.Join(d, fmt.Sprintf(".%s.%d.tmp", slug(f.Name), os.Getpid()))
	b, _ := json.Marshal(map[string]any{"name": f.Name, "url": f.url, "token": f.token, "pid": os.Getpid(),
		"domain": f.Domain, "workspace": f.Workspace, "protocol": protocol, "started": f.started, "run": nil})
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	log.Printf("runtime %q on %s (%s)", f.Name, f.url, f.discovery)
	return os.Rename(tmp, f.discovery)
}

// Stop removes the discovery file when it is still this runtime's.
func (f *Feed) Stop() {
	if f.discovery == "" {
		return
	}
	b, err := os.ReadFile(f.discovery)
	var d struct {
		Pid int `json:"pid"`
	}
	if err == nil && json.Unmarshal(b, &d) == nil && d.Pid == os.Getpid() {
		os.Remove(f.discovery)
	}
}
