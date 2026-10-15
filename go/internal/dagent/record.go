package dagent

// The surprise record (metispy metis/kernel/record.py surprise_record,
// env_record; nets.py program_key): what the watch logs when a reading
// differs from the model, as py writes it field for field.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/metis-lang-dev/metis-agent/go/internal/host"
)

// Meta is a record's meta (the watch's).
type Meta struct {
	Path  string         `json:"path"`
	Beat  int            `json:"beat"`
	Fired int            `json:"fired"`
	Stage string         `json:"stage"`
	Since []string       `json:"since"`
	Host  *string        `json:"host"`
	Diff  map[string]int `json:"diff,omitempty"`
}

// EnvRecord is the record's env: the snapshot's hash, and the snapshot
// itself when its canonical text is at most 64 KiB.
type EnvRecord struct {
	Format string          `json:"format"`
	Hash   string          `json:"hash"`
	Data   json.RawMessage `json:"data,omitempty"`
}

// Record is one surprise record; the optional fields are the watch's
// stamps (a resolution, the bounds' refusals and facts, the escalation,
// the repeat notice, the mined text).
type Record struct {
	Format     string         `json:"format"`
	Version    int            `json:"version"`
	ProgramKey string         `json:"program_key"`
	Steps      int            `json:"steps"`
	Init       map[string]int `json:"init"`
	Observed   map[string]int `json:"observed"`
	Likelihood float64        `json:"likelihood"`
	Kind       *string        `json:"kind"`
	Meta       Meta           `json:"meta"`
	Env        *EnvRecord     `json:"env,omitempty"`
	Resolution map[string]any `json:"resolution,omitempty"`
	Refusals   []string       `json:"refusals,omitempty"`
	Facts      []string       `json:"facts,omitempty"`
	MinedText  *string        `json:"mined_text,omitempty"`
	Escalated  map[string]int `json:"escalated,omitempty"`
	Notice     map[string]any `json:"notice,omitempty"`
}

// KindOf is the record's kind ("" for None).
func (r *Record) KindOf() string {
	if r.Kind == nil {
		return ""
	}
	return *r.Kind
}

// envRecord is py's env_record (data_cap 65536).
func envRecord(snap json.RawMessage) (*EnvRecord, error) {
	text, err := host.EnvText(snap)
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256([]byte(text))
	out := &EnvRecord{Format: "json", Hash: hex.EncodeToString(h[:])}
	if len(text) <= 65536 {
		out.Data = json.RawMessage(text)
	}
	return out, nil
}
