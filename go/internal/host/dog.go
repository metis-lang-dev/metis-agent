package host

import (
	"encoding/json"
	"fmt"
	"strings"
)

// DogTables are dog_host.py's.
var DogTables = Tables{
	Outcomes: []string{"done", "refused", "progress", "expired", "cancelled"},
	Pairs: [][2]string{
		{"battery_ok", "battery_low"}, {"floor_dry", "floor_wet"}, {"free", "obstacle"},
		{"clear", "wall"}, {"leg_ok", "leg_hot"}, {"mapped", "unmapped"}, {"open", "closed"},
		{"person_in_view", "nobody"}, {"seen", "unseen"}, {"spotted", "locked_away"},
	},
	Words: map[string]string{
		"battery_low": "put me on my charger",
		"floor_wet":   "dry the floor here, or move me to a dry spot",
		"obstacle":    "move whatever is beside me on that side",
		"wall":        "turn me away from the wall",
		"leg_hot":     "give my front leg a minute to cool",
		"unmapped":    "walk me to {P} once so I can map it",
		"closed":      "open the way to {P}",
		"nobody":      "come where I can see you",
		"unseen":      "show me {T} once so I know it",
		"locked_away": "unlock where {T} is kept",
	},
	Keys: map[string]string{
		"battery_low": "charger", "floor_wet": "floor", "obstacle": "side", "wall": "wall",
		"leg_hot": "leg", "unmapped": "{P}", "closed": "{P}", "nobody": "see",
		"unseen": "{T}", "locked_away": "{T}",
	},
	Standing: []string{"elsewhere", "tag_near"},
}

const DogLast = 18

// DogScript is the dog's hosted day (beat -> do-lines).
func DogScript() map[int][]string {
	return map[int][]string{
		1: {"+cmd_sit"}, 2: {"+cmd_go(kitchen)"}, 3: {"+cmd_stand"}, 4: {"+cmd_go(tag3)"},
		5: {"+cmd_find(keys)"}, 7: {"+cmd_move(backward)"}, 8: {"+cmd_find(extinguisher)"},
		9: {"+cmd_query(chair)"}, 11: {"+cmd_follow"}, 12: {"+cmd_stop"}, 13: {"+cmd_unknown"},
		14: {"+cmd_paw"}, 15: {"-battery_ok", "+battery_low", "+cmd_find(bottle)"},
		16: {"+cmd_sit"}, 17: {"+cmd_turn(left)"},
	}
}

type answer struct {
	kind  string
	delay int
}

// dogAnswers: what the world says back to a long command, and when.
var dogAnswers = map[string]map[string]answer{
	"go":   {"tag3": {"arrived", 2}, "door": {"arrived", 2}, "window_chair": {"blocked", 2}},
	"find": {"extinguisher": {"locked_away", 2}, "keys": {"spotted", 3}, "bottle": {"spotted", 3}},
}

// Dog is the dog's host: no sensor face (the watch checkpoints).
type Dog struct{ day }

// NewDog is the dog on its hosted day.
func NewDog() *Dog {
	return &Dog{day{Script: DogScript(), Last: DogLast, tables: &DogTables}}
}

func (d *Dog) Tick(counts map[string]int) ([]string, bool) {
	dos, ok := d.begin(counts)
	if !ok {
		return nil, true
	}
	return append(dos, d.Script[d.beat]...), false
}

func (d *Dog) OnEvent(name string, args []string) {
	base := d.actuate(name, args)
	if answers, ok := dogAnswers[base]; ok && len(args) > 0 {
		a, ok := answers[args[0]]
		if !ok {
			a = answer{"timeout", 3}
		}
		line := "+timeout"
		if a.kind != "timeout" {
			line = fmt.Sprintf("+%s(%s)", a.kind, args[0])
		}
		d.due = append(d.due, due{d.beat + a.delay, line})
	} else if base == "follow" {
		d.due = append(d.due, due{d.beat + 4, "+lost"})
	} else if strings.HasPrefix(base, "stop_") || strings.HasPrefix(base, "cancel_") {
		d.due = nil // the task is over: the world owes it no answer
	}
}

// Observe: the dog has no sensor face.
func (d *Dog) Observe(map[string]int) map[string]int { return nil }

// Snapshot is [beat, due, acted] (py's tuple).
func (d *Dog) Snapshot() (json.RawMessage, error) {
	return json.Marshal([]any{d.beat, nonNil(d.due), nonNil(d.acted)})
}

func (d *Dog) Restore(b json.RawMessage) error {
	var x []json.RawMessage
	if err := json.Unmarshal(b, &x); err != nil || len(x) != 3 {
		return fmt.Errorf("dog snapshot: want 3 fields, got %s", b)
	}
	for i, dst := range []any{&d.beat, &d.due, &d.acted} {
		if err := json.Unmarshal(x[i], dst); err != nil {
			return fmt.Errorf("dog snapshot field %d: %w", i, err)
		}
	}
	return nil
}
