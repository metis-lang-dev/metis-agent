// twopass.js — the harness's two-pass intent turn over llama-server's /completion
// (metis-agent internal/llm TwoPass), for the parity bench (parity.mjs). A copy of
// the two-pass part of fsttm-web's former web/backend/llama.js (fsttm-cleanup
// s-d7c6705a: the dog's bench came back here, where it was made; copies, not a
// dependency). The talk mode and the dialog adapter stay in fsttm-web.
//
// Rendering is the harness's TemplateGemma4, byte for byte:
//   <|turn>system\n{system}<turn|>\n<|turn>user\n{user}<turn|>\n<|turn>model\n
// The system turn is the constant prefix; cache_prompt keeps it in the KV
// slot, so a turn evaluates only the user tail. The BOS comes from the server.

export const PASS2 = { temperature: 0.4, top_k: 40, n_predict: 50 }; // config.ws.yaml pass2
const PASS1_MAX = 80;                  // the harness's pass-1 token cap
const CUE = '\nSpoken response:';      // pass 2: bare continuation (Pass2Turn off)
const EOT = '<turn|>';

export function renderSystem(system) {
  return `<|turn>system\n${system}${EOT}\n`;
}

export function renderTurn(system, user) {
  // the harness trims \r\n from the user text (llm.tail)
  const u = user.replace(/^[\r\n]+|[\r\n]+$/g, '');
  return `${renderSystem(system)}<|turn>user\n${u}${EOT}\n<|turn>model\n`;
}

// clean is the harness's LLM.clean: trim, drop thought-channel markup, drop
// a trailing end-of-turn.
export function clean(text) {
  let t = dropChannel(text.trim());
  if (t.endsWith(EOT)) t = t.slice(0, -EOT.length).trim();
  if (t.endsWith('<|end|>')) t = t.slice(0, -'<|end|>'.length).trim();
  return t.trim();
}

function dropChannel(t) {
  const open = '<|channel>', close = '<channel|>';
  if (t.startsWith(open)) {
    const i = t.indexOf(close);
    if (i < 0) return '';
    t = t.slice(i + close.length).trim();
  }
  for (const m of [open, close]) {
    const i = t.indexOf(m);
    if (i >= 0) t = t.slice(0, i).trim();
  }
  return t;
}

// complete POSTs one /completion with stream:true and returns the text, the
// client-side first-token ms, and the server's timings (prompt_n is the
// number of prompt tokens evaluated — the cache_prompt evidence).
export async function complete(base, body, { signal } = {}) {
  const t0 = performance.now();
  const res = await fetch(`${base}/completion`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ...body, stream: true, cache_prompt: true }),
    signal,
  });
  if (!res.ok) throw new Error(`/completion ${res.status}: ${await res.text()}`);
  const reader = res.body.getReader();
  const dec = new TextDecoder();
  let buf = '', text = '', firstMs = null, last = null;
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buf += dec.decode(value, { stream: true });
    let i;
    while ((i = buf.indexOf('\n\n')) >= 0) {
      const ev = buf.slice(0, i);
      buf = buf.slice(i + 2);
      for (const line of ev.split('\n')) {
        if (!line.startsWith('data: ')) continue;
        const d = JSON.parse(line.slice(6));
        if (d.content) {
          if (firstMs === null) firstMs = performance.now() - t0;
          text += d.content;
        }
        if (d.stop) last = d;
      }
    }
  }
  return { text, firstMs, totalMs: performance.now() - t0, timings: last?.timings ?? {},
    tokensCached: last?.tokens_cached, stopType: last?.stop_type };
}

// twoPass is one intent turn: pass 1 greedy under the grammar, pass 2 the
// spoken ack continued after the JSON and the cue, cut at the first newline.
export async function twoPass(base, { system, gbnf, user, pass2 = PASS2, signal }) {
  const prompt = renderTurn(system, user);
  const p1 = await complete(base, {
    prompt, grammar: gbnf, n_predict: PASS1_MAX,
    temperature: 0, top_k: 1, samplers: ['top_k', 'temperature'],
  }, { signal });
  const json = clean(p1.text);
  const p2 = await complete(base, {
    prompt: prompt + p1.text + CUE, n_predict: pass2.n_predict,
    temperature: pass2.temperature, top_k: pass2.top_k, stop: ['\n'],
  }, { signal });
  let voice = p2.text;
  const nl = voice.indexOf('\n');
  if (nl >= 0) voice = voice.slice(0, nl);
  voice = clean(voice);
  return { json, voice, p1, p2 };
}

export function intentOf(json) {
  try { return JSON.parse(json).intent ?? '?'; } catch { return '?'; }
}

// loadAssets reads (by basename: assets/ beside this file) the harness prompt (dog-prompt + the gemma4 pass-2
// examples, concatenated byte for byte like prompt_extra) and the grammar.
export async function loadAssets(read) {
  const [p, x, g] = await Promise.all([
    read('dog-prompt.txt'), read('pass2-examples-gemma4.txt'), read('dog.gbnf')]);
  return { system: p + x, gbnf: g };
}
