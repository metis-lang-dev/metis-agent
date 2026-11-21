package main

// FSTTM's two local protocols, the contract with the voice engine (Python
// fsttm/streams.py and fsttm/feeds/metis.py; the Go engine on FSTTM's go-nuc
// branch speaks the same):
//
//	the stream door  127.0.0.1:8766  JSON lines in: {"stream": "reasoning", "text": ...}
//	the tool port    127.0.0.1:8768  JSON lines, one answer per line:
//	                 {"op": "tools"}                          -> {"tools": [OpenAI function specs]}
//	                 {"op": "call", "domain", "args": {...}}  -> {"status", "text"}
//
// Kept in this small file, shaped so FSTTM's own Go package can replace it
// once it is importable (today FSTTM's are internal/ packages of its module).

import (
	"bufio"
	"encoding/json"
	"log"
	"net"
	"sync"
	"time"
)

// Door is a client of the stream door, reopened when the voice process restarts.
type Door struct {
	Addr string
	mu   sync.Mutex
	conn net.Conn
}

// Reason writes one REASONING entry; false when the door is closed (the voice is not up).
func (d *Door) Reason(text string) bool {
	return d.send(map[string]any{"stream": "reasoning", "text": text})
}

func (d *Door) send(ev map[string]any) bool {
	b, _ := json.Marshal(ev)
	b = append(b, '\n')
	d.mu.Lock()
	defer d.mu.Unlock()
	for try := 0; try < 2; try++ {
		if d.conn == nil {
			c, err := net.DialTimeout("tcp", d.Addr, 2*time.Second)
			if err != nil {
				return false
			}
			d.conn = c
		}
		d.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if _, err := d.conn.Write(b); err == nil {
			return true
		}
		d.conn.Close()
		d.conn = nil
	}
	return false
}

// ToolCall is one call from the tool port: the package and the intent's arguments.
type ToolCall struct {
	Domain string         `json:"domain"`
	Args   map[string]any `json:"args"`
}

// Outcome is a call's answer: done | refused | error | pending, with the rule's words.
type Outcome struct {
	Status string `json:"status"`
	Text   string `json:"text"`
}

// ServeTools answers the tool port: tools() lists, call() runs one call (blocking until its outcome).
func ServeTools(addr string, tools func() []Tool, call func(ToolCall) Outcome) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				sc := bufio.NewScanner(c)
				sc.Buffer(make([]byte, 1<<16), 1<<20)
				for sc.Scan() {
					var req struct {
						Op string `json:"op"`
						ToolCall
					}
					var out any
					if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
						out = Outcome{"error", "a request is a JSON object: " + err.Error()}
					} else {
						switch req.Op {
						case "tools":
							out = map[string]any{"tools": tools()}
						case "call":
							out = call(req.ToolCall)
						default:
							out = Outcome{"error", "op is tools or call"}
						}
					}
					b, _ := json.Marshal(out)
					if _, err := c.Write(append(b, '\n')); err != nil {
						return
					}
				}
			}(c)
		}
	}()
	log.Printf("tools on %s", ln.Addr())
	return ln, nil
}
