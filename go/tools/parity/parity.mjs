// node tools/parity/parity.mjs [base]  — the dog's 54-row intent parity: each row of
// assets/dog-corpus.tsv through the two-pass intent turn, compared with the harness's
// own answer (assets/harness-e2b.jsonl). base: a llama-server serving gemma4 E2B Q4
// (http://host:port, default http://127.0.0.1:8393), or ws://host/ws for the agent's
// intent turn over its console wire (node 20: --experimental-websocket). Writes
// tools/parity/out/parity.jsonl (git-ignored) and prints the table; exit 1 below 54/54.
// It came from fsttm-web (test/parity.mjs, web/parity.js, the dog's prompt, grammar
// and corpus) when that became a generic library again (fsttm-cleanup s-d7c6705a).
import { readFile, writeFile, mkdir } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { loadAssets } from './twopass.js';
import { parseCorpus, parseRef, runParity, summarize } from './parity.js';
import { AgentWs } from './agentws.js';

const here = dirname(fileURLToPath(import.meta.url));
const base = process.argv[2] ?? 'http://127.0.0.1:8393';
const ws = base.startsWith('ws') ? await new AgentWs(base).opened : null;
const assets = await loadAssets(f => readFile(join(here, 'assets', f), 'utf8'));
const rows = parseCorpus(await readFile(join(here, 'assets', 'dog-corpus.tsv'), 'utf8'));
const ref = parseRef(await readFile(join(here, 'assets', 'harness-e2b.jsonl'), 'utf8'));

const recs = [];
for await (const r of runParity(base, assets, rows, ref, { ws })) {
  recs.push(r);
  const mark = r.class ? (r.same ? 'same' : 'DIFF') : 'MISS';
  console.log(`${mark.padEnd(4)} ${r.utt.padEnd(40)} ${r.got.padEnd(13)} first=${String(r.firstMs).padStart(4)}ms ` +
    `p1 eval=${r.p1PromptN} cached=${r.p1Cached}  ${r.json}${r.same === false ? `  harness=${r.harnessJson}` : ''}`);
  console.log(`${' '.repeat(5)}voice → ${JSON.stringify(r.voice)}   (harness ${JSON.stringify(r.harnessVoice)})`);
}
const s = summarize(recs);
await mkdir(join(here, 'out'), { recursive: true });
await writeFile(join(here, 'out', 'parity.jsonl'), recs.map(r => JSON.stringify(r)).join('\n') + '\n');
console.log(`\n== intent class ${s.classOK}/${s.n}, pass-1 JSON identical to the harness ${s.same}/${s.n}; ` +
  `first token p50=${s.firstP50}ms p90=${s.firstP90}ms; prompt tokens evaluated: row 1 ${s.p1PromptNFirst}, later rows max ${s.p1PromptNMax}`);
ws?.close();
process.exit(s.classOK === s.n ? 0 : 1);
