// parity.js — the 54-row dog corpus through twoPass (or the agent's wire), each row
// compared with the harness's own answer on the same model (assets/harness-e2b.jsonl:
// llmbench, internal/llm, E2B Q4, prompt = dog-prompt + pass2-examples-gemma4). Copied
// from fsttm-web's former web/parity.js with the bench (fsttm-cleanup s-d7c6705a).
import { twoPass, intentOf } from './twopass.js';

export function parseCorpus(tsv) {
  return tsv.split('\n').filter(l => l.trim() && !l.startsWith('#')).map(l => {
    const [utt, want, src] = l.split('\t');
    return { utt, want, src };
  });
}

export function parseRef(jsonl) {
  const m = new Map();
  for (const l of jsonl.split('\n')) {
    if (!l.trim()) continue;
    const r = JSON.parse(l);
    if (r.kind === 'turn') m.set(r.utt, r);
  }
  return m;
}

// runParity runs every row and yields one record per row as it lands.
// class = the intent class matches the corpus; same = the pass-1 JSON is
// byte-identical to the harness's (any diff is a rendering bug: prompt,
// grammar and model are the same).
// ws: an AgentWs (agentws.js) — the rows go through the agent's intent turn
// instead of llama-server's /completion.
export async function* runParity(base, assets, rows, ref, { ws = null } = {}) {
  let n = 0;
  for (const row of rows) {
    const r = ws ? await viaWs(ws, `p${++n}`, row.utt) : await twoPass(base, { ...assets, user: row.utt });
    const h = ref.get(row.utt);
    const got = intentOf(r.json);
    yield {
      utt: row.utt, want: row.want, src: row.src, got, json: r.json, voice: r.voice,
      harnessJson: h?.json, harnessVoice: h?.voice,
      class: got === row.want, same: h ? r.json === h.json : null,
      firstMs: Math.round(r.p1.firstMs ?? -1), jsonMs: Math.round(r.p1.totalMs), ttsMs: Math.round(r.p2.totalMs),
      p1PromptN: r.p1.timings.prompt_n, p1Cached: r.p1.tokensCached,
      p2PromptN: r.p2.timings.prompt_n,
    };
  }
}

// viaWs shapes the agent's intent answer like twoPass's (the agent's
// timings: tail eval, pass 1, pass 2; no prompt-token count on the wire)
async function viaWs(ws, id, utt) {
  const r = await ws.intent(id, utt);
  const f = r.frame ?? {};
  return { json: r.json, voice: f.model_voice ?? r.voice,
    p1: { firstMs: f.t_eval_ms ?? r.firstMs, totalMs: (f.t_eval_ms ?? 0) + (f.t_json_ms ?? 0), timings: {}, tokensCached: undefined },
    p2: { totalMs: f.t_tts_ms ?? 0, timings: {} } };
}

export function summarize(recs) {
  const n = recs.length, pct = (a, p) => {
    const s = [...a].sort((x, y) => x - y);
    return s.length ? s[Math.min(s.length - 1, Math.floor(p * s.length))] : 0;
  };
  const first = recs.map(r => r.firstMs);
  return {
    n, classOK: recs.filter(r => r.class).length, same: recs.filter(r => r.same).length,
    firstP50: pct(first, 0.5), firstP90: pct(first, 0.9),
    p1PromptNMax: Math.max(...recs.slice(1).map(r => r.p1PromptN ?? 0)),
    p1PromptNFirst: recs[0]?.p1PromptN,
  };
}
