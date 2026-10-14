package llm

// Template parity, gated on the model files: the rendered prefix/tail for
// Phi-3 must be byte-identical to the strings the shim hard-coded before
// (KV-prefix reuse unchanged), and the gemma GGUF ollama holds must resolve
// to llama.cpp's gemma template with <end_of_turn> as the stop marker.

import (
	"os"
	"strings"
	"testing"

	"github.com/metis-lang-dev/metis-agent/go/internal/gputest"
)

const (
	phi3Path = "/home/axadmin/archive/vox/FSTTM/models/Phi-3-mini-4k-instruct-Q4_K_M.gguf"
	// text-only repack of the ollama gemma3:4b blob (tools/gguf-text-only.py)
	gemmaPath = "../../models/gemma3-4b-it-Q4_K_M.ollama-a2af6cc3.gguf"
)

func loadOrSkip(t *testing.T, path string) *LLM {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Skip("model not present:", path)
	}
	l, err := Load(Config{ModelPath: path, NCtx: 512, NBatch: 512, NThreads: 4,
		NGpuLayers: 0})
	gputest.LoadOK(t, err)
	t.Cleanup(l.Close)
	return l
}

func TestPhi3TemplateIsByteIdenticalToTheOldHardcode(t *testing.T) {
	l := loadOrSkip(t, phi3Path)
	// the Phi-3 GGUF ships its own Jinja template ("gguf"); llama.cpp's
	// engine recognizes it as phi3 — the rendering is what must match
	if tp := l.Template(); tp != "gguf" && tp != "phi3" {
		t.Fatalf("template %q", tp)
	}
	prefix, tailFmt, err := l.splitPrompt("SYS")
	if err != nil {
		t.Fatal(err)
	}
	if want := "<|system|>\nSYS<|end|>\n"; prefix != want {
		t.Errorf("prefix %q want %q", prefix, want)
	}
	l.tailFmt = tailFmt
	if want := "<|user|>\nhello<|end|>\n<|assistant|>\n"; l.tail("hello") != want {
		t.Errorf("tail %q want %q", l.tail("hello"), want)
	}
	if l.EOT() != "<|end|>" {
		t.Errorf("eot %q", l.EOT())
	}
}

func TestGemmaTemplateFromArchitecture(t *testing.T) {
	l := loadOrSkip(t, gemmaPath)
	if l.Template() != "gemma" {
		t.Fatalf("template %q (the ollama blob has no chat_template; the "+
			"architecture fallback must pick gemma)", l.Template())
	}
	prefix, tailFmt, err := l.splitPrompt("SYS")
	if err != nil {
		t.Fatal(err)
	}
	if want := "<start_of_turn>user\nSYS\n\n"; prefix != want {
		t.Errorf("prefix %q want %q", prefix, want)
	}
	l.tailFmt = tailFmt
	if want := "hello<end_of_turn>\n<start_of_turn>model\n"; l.tail("hello") != want {
		t.Errorf("tail %q want %q", l.tail("hello"), want)
	}
	if l.EOT() != "<end_of_turn>" {
		t.Errorf("eot %q", l.EOT())
	}
	if len(l.stopIDs) < 2 {
		t.Errorf("stop ids %v", l.stopIDs)
	}
}

func TestExplicitTemplateOverrideAndUnknown(t *testing.T) {
	if _, err := os.Stat(gemmaPath); err != nil {
		t.Skip("model not present")
	}
	l, err := Load(Config{ModelPath: gemmaPath, NCtx: 512, NBatch: 512, NThreads: 4,
		Template: "chatml"})
	gputest.LoadOK(t, err)
	defer l.Close()
	if l.Template() != "chatml" {
		t.Errorf("override ignored: %q", l.Template())
	}
	if _, err := Load(Config{ModelPath: gemmaPath, NCtx: 512, NBatch: 512,
		Template: "no-such-template"}); err == nil {
		t.Error("unknown template must fail at Load")
	}
}

func TestNarrateProducesASentence(t *testing.T) {
	if _, err := os.Stat(gemmaPath); err != nil {
		t.Skip("model not present")
	}
	prompt, err := os.ReadFile("../../../../metis-catalog/domains/dog/intent/dog-prompt.txt")
	if err != nil {
		t.Skip("prompt not present")
	}
	// CPU: the GPU belongs to a running harness or a bench
	l, err := Load(Config{ModelPath: gemmaPath, NCtx: 2048, NBatch: 512, NThreads: 8})
	gputest.LoadOK(t, err)
	defer l.Close()
	out, err := l.Narrate(string(prompt),
		"Task feedback: the command LOCAL_ACTION (action=STAND_UP) refused. Detail: the robot is not armed.")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("narration: %q", out)
	if out == "" || len(out) > 200 || strings.ContainsAny(out, "{}[]") {
		t.Errorf("not a speakable sentence: %q", out)
	}
}
