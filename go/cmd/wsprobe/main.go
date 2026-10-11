// wsprobe — drives the harness over its websocket transport from the shell:
// the CLI stand-in for the browser (HARNESS.md §6 wire) and the regression
// hook for §7 outside Chrome.
//
//	wsprobe -url ws://localhost:8392 -say "stand up"            # chat turn
//	wsprobe -url ws://localhost:8392 -say "stand up" -task refused:not armed
//	wsprobe -url ws://localhost:8392 -wav utterance.wav           # remote mic
//	wsprobe -url ws://localhost:8392 -attention ASLEEP -say "sit"
//	wsprobe -url ws://localhost:8392 -runtime-intent "reset 0"   # runtime{…} events print too
//
// It prints every event, plays TTS streams virtually (acks tts_done after
// the audio length, or at once with -fast) and, with -task, answers the
// first command it sees with that outcome so the second beat can be heard.
// Exits after -wait seconds of silence.
package main

import (
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	url := flag.String("url", "ws://localhost:8392", "harness base url (ws://host:port)")
	say := flag.String("say", "", "chat text to send (say_text)")
	announce := flag.String("announce", "", "external text to announce (say)")
	rtIntent := flag.String("runtime-intent", "", "a runtime intent for the kernel, e.g. \"reset 0\" (runtime_intent)")
	attention := flag.String("attention", "", "AWAKE | ASLEEP before anything else")
	task := flag.String("task", "", "answer the first command: result[:detail] (done|failed|refused|expired)")
	taskDelay := flag.Duration("task-delay", 500*time.Millisecond, "delay before the task answer")
	wav := flag.String("wav", "", "16 kHz mono s16le WAV to stream as the remote mic")
	wavDelay := flag.Duration("wav-delay", 0, "start the WAV this long after connecting (e.g. to barge in)")
	fast := flag.Bool("fast", false, "ack tts_done at once instead of after the audio length")
	wait := flag.Duration("wait", 4*time.Second, "exit after this much silence")
	flag.Parse()
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	conn, _, err := websocket.DefaultDialer.Dial(*url+"/ws", nil)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	send := func(m map[string]any) {
		b, _ := json.Marshal(m)
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("→ %s\n", b)
	}

	type ttsHdr struct {
		id      string
		rate    int
		samples int
		got     int
	}
	var cur *ttsHdr
	answered := false
	events := make(chan map[string]any, 64)
	var got atomic.Int64 // samples of the current stream, counted on the socket order
	go func() {
		for {
			mt, b, err := conn.ReadMessage()
			if err != nil {
				close(events)
				return
			}
			if mt == websocket.BinaryMessage {
				got.Add(int64(len(b) / 2))
				continue
			}
			var m map[string]any
			if json.Unmarshal(b, &m) == nil {
				if m["ev"] == "tts" {
					got.Store(0)
				}
				if m["ev"] == "tts_end" {
					m["_got"] = got.Load()
				}
				events <- m
			}
		}
	}()

	if *attention != "" {
		send(map[string]any{"ev": "attention", "state": *attention})
	}
	if *say != "" {
		send(map[string]any{"ev": "say_text", "text": *say})
	}
	if *rtIntent != "" {
		send(map[string]any{"ev": "runtime_intent", "text": *rtIntent})
	}
	if *announce != "" {
		send(map[string]any{"ev": "say", "text": *announce})
	}
	tokCh := make(chan string, 1)
	if *wav != "" {
		go func() {
			time.Sleep(*wavDelay)
			streamWav(*url, *wav, send, tokCh)
		}()
	}

	idle := time.NewTimer(*wait)
	for {
		select {
		case m, ok := <-events:
			if !ok {
				fmt.Println("connection closed")
				return
			}
			idle.Reset(*wait)
			gotSamples, _ := m["_got"].(int64)
			delete(m, "_got")
			b, _ := json.Marshal(m)
			fmt.Printf("← %s\n", b)
			switch m["ev"] {
			case "role":
				if m["master"] == true {
					if tok, _ := m["mic_tok"].(string); tok != "" {
						select {
						case tokCh <- tok:
						default:
						}
					}
				}
			case "tts":
				cur = &ttsHdr{id: m["id"].(string), rate: int(m["rate"].(float64)),
					samples: int(m["samples"].(float64))}
			case "tts_end":
				if cur == nil {
					break
				}
				cur.got = int(gotSamples)
				dur := time.Duration(cur.samples) * time.Second / time.Duration(cur.rate)
				fmt.Printf("   (tts %s: %d of %d samples received, %.2fs)\n", cur.id, cur.got,
					cur.samples, dur.Seconds())
				id := cur.id
				cur = nil
				go func() {
					if !*fast {
						time.Sleep(dur)
					}
					send(map[string]any{"ev": "tts_done", "id": id,
						"played_ms": dur.Milliseconds()})
				}()
			case "tts_stop":
				if cur != nil {
					send(map[string]any{"ev": "tts_done", "id": m["id"],
						"played_ms": 100})
					cur = nil
				}
			case "command":
				if *task != "" && !answered {
					answered = true
					res, detail, _ := strings.Cut(*task, ":")
					id := m["id"].(string)
					go func() {
						time.Sleep(*taskDelay)
						send(map[string]any{"ev": "task", "id": id, "result": res, "detail": detail})
					}()
				}
			}
		case <-idle.C:
			fmt.Printf("(quiet for %s — done)\n", *wait)
			return
		}
	}
}

// streamWav grabs the mic token on /ws, opens /ws_mic?tok= and sends the WAV
// data as 640-byte frames at real time (20 ms each).
func streamWav(url, path string, send func(map[string]any), tokCh <-chan string) {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) < 44 || string(raw[:4]) != "RIFF" {
		log.Fatalf("wav: %v (need a RIFF file)", err)
	}
	// minimal RIFF walk: find the data chunk; assume 16 kHz mono s16le
	off := 12
	var pcm []byte
	for off+8 <= len(raw) {
		id := string(raw[off : off+4])
		n := int(binary.LittleEndian.Uint32(raw[off+4 : off+8]))
		if id == "fmt " {
			rate := binary.LittleEndian.Uint32(raw[off+12 : off+16])
			ch := binary.LittleEndian.Uint16(raw[off+10 : off+12])
			if rate != 16000 || ch != 1 {
				log.Fatalf("wav: %d Hz %d ch — need 16000 Hz mono", rate, ch)
			}
		}
		if id == "data" {
			end := off + 8 + n
			if end > len(raw) {
				end = len(raw)
			}
			pcm = raw[off+8 : end]
			break
		}
		off += 8 + n + n%2
	}
	if pcm == nil {
		log.Fatal("wav: no data chunk")
	}
	send(map[string]any{"ev": "grab_master"})
	// the role{mic_tok} answer is picked off the event stream by the main loop
	var tok string
	select {
	case tok = <-tokCh:
	case <-time.After(3 * time.Second):
		log.Fatal("no mic_tok after grab_master")
	}
	mic, _, err := websocket.DefaultDialer.Dial(url+"/ws_mic?tok="+tok, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer mic.Close()
	// a little leading silence so the segmenter's ring buffer settles, and
	// trailing silence so the utterance closes (padding_ms)
	frames := make([][]byte, 0, len(pcm)/640+100)
	for i := 0; i < 25; i++ {
		frames = append(frames, make([]byte, 640))
	}
	for off := 0; off+640 <= len(pcm); off += 640 {
		frames = append(frames, pcm[off:off+640])
	}
	for i := 0; i < 60; i++ {
		frames = append(frames, make([]byte, 640))
	}
	fmt.Printf("   (mic: streaming %d frames = %.1fs)\n", len(frames), float64(len(frames))*0.02)
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for _, f := range frames {
		<-tick.C
		if err := mic.WriteMessage(websocket.BinaryMessage, f); err != nil {
			log.Printf("mic: %v", err)
			return
		}
	}
	fmt.Println("   (mic: stream finished)")
}
