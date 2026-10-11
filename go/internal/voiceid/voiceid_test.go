package voiceid

// Live imprinting e2e without a human: two RHVoice voices play the roles —
// "Alan" is enrolled as the owner, "Clb" is the impostor. CAVEAT on the
// numbers: all RHVoice voices share one HTS vocoder whose artifacts inflate
// cross-voice cosine (measured on this box: same-voice 0.84–0.96,
// cross-voice 0.58–0.86), so the test uses a 0.72 threshold on the
// best-separated pair. REAL human voices separate far wider — production
// keeps the 0.40 default — and a human owner vs the robot's SLT TTS voice
// is a much easier margin than any synthetic-vs-synthetic pair here.
// Gated on the embedding model and RHVoice being installed.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const (
	modelPath = "/home/axadmin/repo/vox/FSTTM/models/speaker/wespeaker_en_voxceleb_CAM++.onnx"
	// synthetic-voice test threshold (see caveat above); production human
	// default stays 0.40
	testThreshold = 0.72
)

// rhvoicePCM synthesizes text with the given voice and returns 16 kHz mono
// s16le PCM (RHVoice emits 24 kHz; naive 3→2 linear resample is fine for
// embedding purposes).
func rhvoicePCM(t *testing.T, voice, text string) []byte {
	t.Helper()
	cmd := exec.Command("RHVoice-client", "-s", voice, "-r", "0", "-v", "0")
	cmd.Stdin = bytes.NewReader([]byte(text))
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("RHVoice-client unavailable: %v", err)
	}
	i := bytes.Index(out, []byte("data"))
	if i < 0 {
		t.Fatal("no data chunk")
	}
	pcm24 := out[i+8:]
	n24 := len(pcm24) / 2
	s24 := make([]int16, n24)
	for j := 0; j < n24; j++ {
		s24[j] = int16(uint16(pcm24[2*j]) | uint16(pcm24[2*j+1])<<8)
	}
	// 24 kHz → 16 kHz: 2 output samples per 3 input samples
	n16 := n24 * 2 / 3
	out16 := make([]byte, n16*2)
	for j := 0; j < n16; j++ {
		src := float64(j) * 1.5
		i0 := int(src)
		frac := src - float64(i0)
		i1 := i0 + 1
		if i1 >= n24 {
			i1 = n24 - 1
		}
		v := int16(float64(s24[i0])*(1-frac) + float64(s24[i1])*frac)
		out16[2*j] = byte(v)
		out16[2*j+1] = byte(v >> 8)
	}
	return out16
}

func newVerifier(t *testing.T, profile string) *SpeakerVerifier {
	t.Helper()
	if _, err := os.Stat(modelPath); err != nil {
		t.Skip("speaker embedding model not present")
	}
	v, err := New(Config{Model: modelPath, Profile: profile, Threshold: testThreshold})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

var enrollLines = []string{
	"The robot listens only to its owner.",
	"Navigation and mapping are running normally today.",
	"Please remember where we parked the charging dock.",
}

func imprint(t *testing.T, v *SpeakerVerifier, voice, owner, path string) {
	t.Helper()
	var embs [][]float32
	for _, line := range enrollLines {
		embs = append(embs, v.Embed(rhvoicePCM(t, voice, line)))
	}
	if err := SaveProfile(path, &Profile{Owner: owner, Model: modelPath,
		Embedding: Mean(embs)}); err != nil {
		t.Fatal(err)
	}
}

func TestImprintOwnerAcceptedImpostorRejected(t *testing.T) {
	dir := t.TempDir()
	profile := filepath.Join(dir, "owner.json")

	// enroll Alan as the owner
	enroller := newVerifier(t, "")
	imprint(t, enroller, "Alan", "alan", profile)

	v := newVerifier(t, profile)
	if v.Owner() != "alan" {
		t.Fatalf("owner = %q", v.Owner())
	}

	ownerUtt := rhvoicePCM(t, "Alan", "Go to the kitchen and wait for me there.")
	ok, score := v.IsOwner(ownerUtt)
	if !ok {
		t.Fatalf("owner rejected (score %.3f)", score)
	}

	echoUtt := rhvoicePCM(t, "Clb", "Go to the kitchen and wait for me there.")
	ok2, score2 := v.IsOwner(echoUtt)
	if ok2 {
		t.Fatalf("impostor accepted (score %.3f)", score2)
	}
	if score < score2+0.1 {
		t.Fatalf("margin too thin: owner %.3f vs impostor %.3f", score, score2)
	}
	t.Logf("owner score %.3f, impostor score %.3f", score, score2)
}

func TestOwnershipTransferByReplacingProfile(t *testing.T) {
	dir := t.TempDir()
	profile := filepath.Join(dir, "owner.json")
	enroller := newVerifier(t, "")

	imprint(t, enroller, "Alan", "alan", profile)
	v1 := newVerifier(t, profile)
	utterance := "Follow me to the workshop please."
	if ok, _ := v1.IsOwner(rhvoicePCM(t, "Clb", utterance)); ok {
		t.Fatal("Clb accepted before transfer")
	}

	// ownership transfer: replace the imprint — nothing else changes
	imprint(t, enroller, "Clb", "clb", profile)
	v2 := newVerifier(t, profile)
	if ok, s := v2.IsOwner(rhvoicePCM(t, "Clb", utterance)); !ok {
		t.Fatalf("new owner rejected after transfer (%.3f)", s)
	}
	if ok, s := v2.IsOwner(rhvoicePCM(t, "Alan", utterance)); ok {
		t.Fatalf("previous owner still accepted after transfer (%.3f)", s)
	}
}

func TestShortUtteranceBypasses(t *testing.T) {
	v := newVerifier(t, "")
	v.owner = make([]float32, 192) // fake imprint
	ok, score := v.IsOwner(make([]byte, 2000)) // 62 ms
	if !ok || score == score {                 // score must be NaN
		t.Fatalf("short utterance must bypass (ok=%v score=%v)", ok, score)
	}
}
