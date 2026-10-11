// Package voiceid imprints the robot to ONE owner's voice.
//
// A speaker-embedding model (sherpa-onnx, CPU, bundled runtime) turns each
// utterance into a vector; cosine similarity against the enrolled owner
// profile gates who the robot listens to:
//
//   - waking the robot requires the OWNER's voice, not just the wake word —
//     the robot imprints;
//   - barge-in confirmation requires the owner too, which kills the last
//     echo failure mode (the robot's own TTS voice can never match);
//   - ownership transfer = replace the profile file (re-run imprinting, or
//     drop in another profile.json) — nothing else changes.
//
// The profile is a mean embedding over N enrollment utterances, stored as
// plain JSON so it is portable and inspectable.
package voiceid

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sync"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

// Verifier gates utterances on the enrolled owner's voice. Implementations
// must be safe for use from the orchestrator goroutine.
type Verifier interface {
	// IsOwner scores one utterance (16 kHz mono s16le PCM) against the
	// enrolled profile. Utterances too short to verify return (true, NaN)
	// — not enough signal, and it keeps short wake words responsive.
	IsOwner(pcm []byte) (bool, float64)
}

// Profile is the imprint: who the robot belongs to.
type Profile struct {
	Owner     string    `json:"owner"`
	Model     string    `json:"model"`     // embedding model it was made with
	Embedding []float32 `json:"embedding"` // mean over enrollment utterances
}

type Config struct {
	Model     string  // speaker-embedding ONNX
	Profile   string  // profile.json path (the imprint)
	Threshold float64 // cosine acceptance (default 0.40)
	MinSpeech float64 // seconds below which verification is bypassed (0.5)
}

type SpeakerVerifier struct {
	mu        sync.Mutex // sherpa streams are not goroutine-safe
	ex        *sherpa.SpeakerEmbeddingExtractor
	owner     []float32
	ownerName string
	threshold float64
	minSpeech float64
}

func New(cfg Config) (*SpeakerVerifier, error) {
	if cfg.Threshold == 0 {
		cfg.Threshold = 0.40
	}
	if cfg.MinSpeech == 0 {
		cfg.MinSpeech = 0.5
	}
	ex := sherpa.NewSpeakerEmbeddingExtractor(&sherpa.SpeakerEmbeddingExtractorConfig{
		Model: cfg.Model, NumThreads: 2, Provider: "cpu",
	})
	if ex == nil {
		return nil, fmt.Errorf("voiceid: cannot load embedding model %s", cfg.Model)
	}
	v := &SpeakerVerifier{ex: ex, threshold: cfg.Threshold,
		minSpeech: cfg.MinSpeech}
	if cfg.Profile != "" {
		p, err := LoadProfile(cfg.Profile)
		if err != nil {
			return nil, fmt.Errorf("voiceid: %w (imprint first: fsttm-imprint)", err)
		}
		v.owner = p.Embedding
		v.ownerName = p.Owner
	}
	return v, nil
}

func (v *SpeakerVerifier) Owner() string { return v.ownerName }

// Embed computes the embedding of one utterance (16 kHz mono s16le).
func (v *SpeakerVerifier) Embed(pcm []byte) []float32 {
	samples := make([]float32, len(pcm)/2)
	for i := range samples {
		samples[i] = float32(int16(uint16(pcm[2*i])|uint16(pcm[2*i+1])<<8)) / 32768.0
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	stream := v.ex.CreateStream()
	defer sherpa.DeleteOnlineStream(stream)
	stream.AcceptWaveform(16000, samples)
	stream.InputFinished()
	return v.ex.Compute(stream)
}

func (v *SpeakerVerifier) IsOwner(pcm []byte) (bool, float64) {
	if v.owner == nil {
		return true, math.NaN() // no imprint yet → open
	}
	if float64(len(pcm)/2)/16000.0 < v.minSpeech {
		return true, math.NaN() // too short to verify — bypass
	}
	emb := v.Embed(pcm)
	score := Cosine(emb, v.owner)
	return score >= v.threshold, score
}

// Cosine similarity of two embeddings.
func Cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// Mean of several embeddings — the enrollment aggregate.
func Mean(embs [][]float32) []float32 {
	if len(embs) == 0 {
		return nil
	}
	out := make([]float32, len(embs[0]))
	for _, e := range embs {
		for i := range e {
			out[i] += e[i]
		}
	}
	for i := range out {
		out[i] /= float32(len(embs))
	}
	return out
}

func LoadProfile(path string) (*Profile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Profile
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	if len(p.Embedding) == 0 {
		return nil, fmt.Errorf("profile %s has no embedding", path)
	}
	return &p, nil
}

// SaveProfile writes the imprint. Ownership transfer IS this call — the new
// profile atomically replaces the old owner.
func SaveProfile(path string, p *Profile) error {
	b, err := json.MarshalIndent(p, "", " ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
