package agent

import (
	"fmt"

	"github.com/metis-lang-dev/metis-agent/go/internal/wire"
)

// Bench hooks (cmd/llmbench: -events, -e8, the cap check): the dialog's
// own framing, cues and guards, exported unchanged (moved from
// internal/pipeline's bench_export.go when pipeline left, FRAMEWORK §7.3).

const (
	cueSurprise = "[The line above is the robot's runtime report, not something the person said; do not classify it.] " +
		"Tell the person in one short sentence, in your own words, what happened in the %s and what it means. Use only numbers that are above."
	cueRackSurprise = "[The lines above are the robot's runtime report, not something the person said; do not classify them.] " +
		"Tell the person in one short sentence, in your own words, what happened in the %s, what it means, and where it came from if the lines say. Use only numbers that are above."
)

func TaskCue(it wire.Item, t Task) string         { return taskCue(it, t) }
func TaskVoiceOK(s string) bool                   { return taskVoiceOK(s) }
func SanitizeAck(s string) string                 { return sanitizeAck(s) }
func NarrateTemplate(it wire.Item, t Task) string { return narrateTemplate(it, t) }
func RackAskCue() string                          { return cueRackAsk }
func RackSurpriseCue(where string) string         { return fmt.Sprintf(cueRackSurprise, where) }
func SurpriseCue(where string) string             { return fmt.Sprintf(cueSurprise, where) }
func CleanFact(s string) string                   { return cleanText(s, factCap) }
func InventedNumbers(s, fact string) string       { return inventedNumbers(s, fact) }
