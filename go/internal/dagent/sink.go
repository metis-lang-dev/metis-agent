package dagent

// The records sink (py DomainAgent.sink_record, e2e-workflow step 1): every
// surprise record and its close appended as one JSON line to the domain's
// records/<session>.jsonl — the kernel's record, the record's index, the
// domain, the session, the catalog's hash and the close (open · explained ·
// reset · escalated · operator), so the development stack replays it.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Sunk is what a trace reply lists of a sunk line.
type Sunk struct {
	Record    int    `json:"record"`
	Status    string `json:"status"`
	Beat      int    `json:"beat"`
	File      string `json:"file"`
	MinedText string `json:"mined_text"`
}

// Sink appends to one session's records file; Path "" is off.
type Sink struct {
	Path    string
	Session string
	Domain  string
	Key     string // the catalog's hash (the bundle key)
	Now     func() time.Time
	Sunk    []Sunk
}

// pyTS is time.strftime("%Y-%m-%dT%H:%M:%S%z").
func pyTS(t time.Time) string { return t.Format("2006-01-02T15:04:05-0700") }

// Line is one sink line for record k closed with status at the beat. The
// generic agent's AgentHost wraps the catalog's host: the line carries the
// catalog host's own snapshot (what the loop's REPL restores), not the
// wrapper's {host, queues}.
func (s *Sink) Line(rec *Record, k int, status string, beat int, extra []KV, wrapped bool) (map[string]any, error) {
	b, err := json.Marshal(rec)
	if err != nil {
		return nil, err
	}
	var line map[string]any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := d.Decode(&line); err != nil {
		return nil, err
	}
	if wrapped && rec.Env != nil && rec.Env.Data != nil {
		var data map[string]json.RawMessage
		if json.Unmarshal(rec.Env.Data, &data) == nil {
			if h, ok := data["host"]; ok {
				env, err := envRecord(h)
				if err != nil {
					return nil, err
				}
				line["env"] = env
			}
		}
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	ts := pyTS(now())
	cl := map[string]any{"status": status, "beat": beat, "ts": ts}
	for _, kv := range extra {
		if kv.V != nil && kv.V != "" {
			cl[kv.K] = kv.V
		}
	}
	line["record"], line["domain"], line["session"], line["ts"] = k, s.Domain, s.Session, ts
	line["catalog_hash"], line["close"] = s.Key, cl
	return line, nil
}

// Record appends record k's line; an unwritable file is reported, never
// fatal (py prints it and goes on).
func (s *Sink) Record(rec *Record, k int, status string, beat int, extra []KV, wrapped bool) error {
	if s.Path == "" {
		return nil
	}
	line, err := s.Line(rec, k, status, beat, extra, wrapped)
	if err != nil {
		return err
	}
	b, err := json.Marshal(line)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o777); err != nil {
		return fmt.Errorf("[records] %s: %w", s.Path, err)
	}
	f, err := os.OpenFile(s.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666)
	if err != nil {
		return fmt.Errorf("[records] %s: %w", s.Path, err)
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("[records] %s: %w", s.Path, err)
	}
	mt := ""
	if rec.MinedText != nil {
		mt = *rec.MinedText
	}
	s.Sunk = append(s.Sunk, Sunk{k, status, beat, s.Path, mt})
	return nil
}
