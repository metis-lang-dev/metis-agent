// fsttm-imprint — enroll the robot's OWNER voice (or transfer ownership).
//
//	fsttm-imprint -model wespeaker.onnx -profile owner.json -owner pero -record 3
//	fsttm-imprint -model wespeaker.onnx -profile owner.json -owner pero -wav a.wav,b.wav
//	fsttm-imprint -model wespeaker.onnx -profile owner.json -test x.wav
//
// Ownership transfer IS re-running enrollment: the new profile atomically
// replaces the old one; the engine picks it up on next start.
package main

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/audio"
	"github.com/metis-lang-dev/metis-agent/go/internal/voiceid"
)

func readWav16k(path string) []byte {
	b, err := os.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}
	i := strings.Index(string(b), "data")
	if i < 0 {
		log.Fatalf("%s: no data chunk (need 16 kHz mono s16le wav)", path)
	}
	return b[i+8:]
}

func record(seconds int) []byte {
	fmt.Printf("  recording %ds — speak naturally …\n", seconds)
	ctx, cancel := context.WithTimeout(context.Background(),
		time.Duration(seconds)*time.Second)
	defer cancel()
	frames := make(chan []byte, 64)
	go func() { _ = audio.Capture(ctx, frames) }()
	var pcm []byte
	for f := range frames {
		pcm = append(pcm, f...)
		if ctx.Err() != nil {
			break
		}
	}
	fmt.Printf("  captured %.1fs\n", float64(len(pcm)/2)/16000.0)
	return pcm
}

func main() {
	model := flag.String("model", "", "speaker-embedding ONNX")
	profile := flag.String("profile", "", "owner profile path (the imprint)")
	owner := flag.String("owner", "", "owner name to enroll")
	rec := flag.Int("record", 0, "record N utterances from the mic")
	seconds := flag.Int("seconds", 5, "seconds per recorded utterance")
	wavs := flag.String("wav", "", "comma-separated enrollment wavs (16k mono)")
	test := flag.String("test", "", "score a wav against the current imprint")
	flag.Parse()

	v, err := voiceid.New(voiceid.Config{Model: *model})
	if err != nil {
		log.Fatal(err)
	}

	if *test != "" {
		p, err := voiceid.LoadProfile(*profile)
		if err != nil {
			log.Fatal(err)
		}
		emb := v.Embed(readWav16k(*test))
		fmt.Printf("cosine vs owner %q: %+.3f\n", p.Owner,
			voiceid.Cosine(emb, p.Embedding))
		return
	}

	if *owner == "" || (*rec == 0 && *wavs == "") {
		flag.Usage()
		os.Exit(2)
	}
	var embs [][]float32
	if *wavs != "" {
		for _, w := range strings.Split(*wavs, ",") {
			fmt.Println("  " + w)
			embs = append(embs, v.Embed(readWav16k(w)))
		}
	}
	for i := 0; i < *rec; i++ {
		fmt.Printf("[%d/%d]\n", i+1, *rec)
		embs = append(embs, v.Embed(record(*seconds)))
	}
	mean := voiceid.Mean(embs)
	// per-take self-similarity — low values mean bad takes
	for i, e := range embs {
		fmt.Printf("  take %d: self-similarity %+.3f\n", i+1,
			voiceid.Cosine(e, mean))
	}
	if err := voiceid.SaveProfile(*profile, &voiceid.Profile{
		Owner: *owner, Model: *model, Embedding: mean}); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("imprinted %q → %s (ownership transferred if a profile existed)\n",
		*owner, *profile)
	_ = binary.LittleEndian // keep import if trimmed later
}
