package rack

import (
	"strings"

	"github.com/metis-lang-dev/metis-agent/go/internal/kernel"
)

// FromSignal turns one runtime signal's pieces (kernel.PiecesNow: plain
// words, counts, consequences) into the domain's records: one per piece,
// all with the signal's seq.
func FromSignal(s kernel.Signal, pieces []kernel.Piece) []Record {
	beat := 0
	if b, ok := s.F["beat"].(float64); ok {
		beat = int(b)
	}
	status, _ := s.F["status"].(string)
	verb, _ := s.F["verb"].(string)
	var out []Record
	for _, p := range pieces {
		r := Record{Path: s.Path, Seq: s.Seq, Beat: beat, Text: p.Text, Rec: p.Rec}
		switch p.Kind {
		case "operator":
			r.Kind, r.Closes, r.Rec = "event", true, -1
		case "result":
			r.Kind = "result"
			if p.Class == "surprise" {
				r.Kind = "refused"
			}
			r.Closes = strings.HasPrefix(status, "admitted") && (verb == "reset" || verb == "explain")
		default:
			r.Kind = p.Kind // event | reading | quiescence | surprise | notice
		}
		out = append(out, r)
	}
	return out
}
