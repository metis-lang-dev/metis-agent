package pipeline

import (
	"fmt"

	"github.com/metis-lang-dev/metis-agent/go/internal/wire"
)

// Bench hooks (cmd/llmbench -events, MODEL-EXPERIMENTS.md step 4): the
// harness's own task-narration framing and guards, exported unchanged so the
// narrate bench never re-frames an event.

func TaskCue(it wire.Item, t Task) string         { return taskCue(it, t) }
func TaskVoiceOK(s string) bool                   { return taskVoiceOK(s) }
func SanitizeAck(s string) string                 { return sanitizeAck(s) }
func NarrateTemplate(it wire.Item, t Task) string { return narrateTemplate(it, t) }

// E7/E8 (docs/RAG.md §6, §11; cmd/llmbench -e8): the rack's own cues, the
// focus line's fact cleaning and the number guard, unchanged.

func RackAskCue() string                    { return cueRackAsk }
func RackSurpriseCue(where string) string   { return fmt.Sprintf(cueRackSurprise, where) }
func SurpriseCue(where string) string       { return fmt.Sprintf(cueSurprise, where) }
func CleanFact(s string) string             { return cleanText(s, factCap) }
func InventedNumbers(s, fact string) string { return inventedNumbers(s, fact) }
