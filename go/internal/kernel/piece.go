package kernel

import (
	"fmt"
	"sort"
	"strings"
)

// Piece is one runtime signal as the model reads it (docs/CML.md §7, the
// user's decisions after E6): a deterministic template per signal kind, in
// plain words (atom names as words, card names as their provenance text,
// kernel.PlainWords), inserted at the tail and followed by a cue that asks
// the model to speak about it. Class says how the dialog treats it:
// "progress" waits for a free floor and is batched, thinking off;
// "surprise" (surprise, notice, refused result) takes the floor next,
// thinking on.
type Piece struct {
	Kind  string // event | reading | operator | quiescence | surprise | result | notice
	Class string // progress | surprise
	Rec   int    // the surprise record it belongs to, -1 for none
	Text  string
}

// Pieces renders one signal (a result with a notice gives two pieces).
func Pieces(s Signal, cards []Card) []Piece {
	f := s.F
	where := words(s.Path)
	if where == "" {
		where = "runtime"
	}
	rec := -1
	if r, ok := f["record"].(float64); ok {
		rec = int(r)
	}
	p := func(kind, class, text string) Piece {
		return Piece{Kind: kind, Class: class, Rec: rec, Text: PlainWords(text, cards)}
	}
	switch s.Kind {
	case "event":
		kind, _ := f["kind"].(string)
		name, _ := f["name"].(string)
		switch kind {
		case "clause":
			return []Piece{p("event", "progress", fmt.Sprintf("In the %s: %s (%s).", where, name, signedRaw(netOf(f))))}
		case "exo":
			var ops []string
			if o, ok := f["ops"].([]any); ok {
				for _, x := range o {
					if s, ok := x.(string); ok {
						ops = append(ops, strings.TrimPrefix(s, "+"))
					}
				}
			}
			return []Piece{p("event", "progress", fmt.Sprintf("In the %s, from outside: %s.", where, strings.Join(ops, ", ")))}
		case "reading":
			if f["consistent"] == true {
				return []Piece{p("reading", "progress", fmt.Sprintf("The %s reading agrees with the model again: the odd reading was a one-off.", where))}
			}
			return []Piece{p("reading", "progress", fmt.Sprintf("The %s reading changed: %s.", where, signedRaw(diffOf(f))))}
		case "operator":
			return []Piece{p("operator", "progress", fmt.Sprintf("The operator resumed the %s on its reading: it takes orders again.", where))}
		}
		return []Piece{p("event", "progress", fmt.Sprintf("In the %s: %s %s.", where, kind, name))}
	case "quiescence":
		t := fmt.Sprintf("The %s is quiet after %d moves: the order is done, it waits for the next one.", where, int(num(f["fired"])))
		if f["episode_over"] == true {
			t = fmt.Sprintf("The %s run is over after %d moves.", where, int(num(f["fired"])))
		}
		return []Piece{p("quiescence", "progress", t)}
	case "surprise":
		obs, _ := f["observed"].(map[string]any)
		mod, _ := f["model"].(map[string]any)
		d, _ := f["diff"].(map[string]any)
		keys := make([]string, 0, len(d))
		for k := range d {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var parts []string
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s is %d where the model expects %d", k, int(num(obs[k])), int(num(mod[k]))))
		}
		t := fmt.Sprintf("Surprise %d in the %s: %s.", rec, where, strings.Join(parts, "; "))
		if f["observe_only"] == true {
			t += " The runtime stopped acting: it takes no new orders until the surprise is closed."
		}
		return []Piece{p("surprise", "surprise", t)}
	case "result":
		verb, _ := f["verb"].(string)
		status, _ := f["status"].(string)
		reason, _ := f["reason"].(string)
		var out []Piece
		// the consequence, not only the state (E6b: facts that must be
		// applied are used less than facts read back)
		switch {
		case status == "applied":
			out = append(out, p("result", "progress", fmt.Sprintf("Your %s of surprise %d was applied.", verb, rec)))
		case strings.HasPrefix(status, "admitted") && verb == "reset":
			out = append(out, p("result", "progress", fmt.Sprintf("Your reset of surprise %d was accepted: the odd reading is set aside as a misread, and the runtime takes orders again.", rec)))
		case strings.HasPrefix(status, "admitted") && verb == "explain":
			out = append(out, p("result", "progress", fmt.Sprintf("Your explanation of surprise %d was accepted: %s; the runtime takes orders again.", rec, reason)))
		case strings.HasPrefix(status, "admitted") && verb == "escalate":
			out = append(out, p("result", "progress", fmt.Sprintf("Your escalation of surprise %d was accepted: it goes to the people who revise the model, and the runtime takes no new orders until the operator resumes it.", rec)))
		case strings.HasPrefix(status, "admitted"):
			out = append(out, p("result", "progress", fmt.Sprintf("Your %s of surprise %d was accepted: %s.", verb, rec, reason)))
		default:
			out = append(out, p("result", "surprise", fmt.Sprintf("Your %s of surprise %d was refused: %s. The surprise stays open: the runtime still takes no new orders.", verb, rec, reason)))
		}
		if nt, ok := f["notice"].(map[string]any); ok {
			card, _ := nt["card"].(string)
			move, _ := nt["move"].(string)
			out = append(out, p("notice", "surprise", fmt.Sprintf("Notice: %s with %s explained the reading %d orders running: the %s model lacks it, so the people who revise the model are told; orders go on.",
				card, move, int(num(nt["orders"])), where)))
		}
		return out
	}
	return []Piece{p(s.Kind, "progress", fmt.Sprintf("The %s: %s.", where, s.Kind))}
}
