// console.js — the metis-agent console (agent-console, s-7f3d1088): the
// orchestrator's chat with voice, every line labelled by its source; the
// installed catalog; a tab per domain (its stream, its REPL log, its world
// ports); the dlg-* journal live. The floor and the voice are fsttm-web's
// library (lib/dialog.js), served at lib/ (run.sh LIB_DIR); the shell — the
// top bar, the docks and their strip, the colours — is fsttm-web's
// lib/shell.css + shell.js, the loop console's own (domain-catalog step 3).
//
//   /                      live, on the service's /ws (run.sh)
//   ?replay=fixtures/live-1710.jsonl[&id=D1-c][&speed=10][&voice]
//                          a recorded journal, no service (the static mock)
//
// The wire (docs/WIRE.md): → hello{since} · say_text{id,text} · world{domain,atoms}
// · activate{domain} · deactivate{domain} · reload{domain} · ports{domain} · heard{id,text} · cut{id,heard}
// ← journal{rec} · catalog{domains} · ports{…} · world_ack{…} · domain_ack{…} · reload_ack{…}
// · narration{…,say} · intent{id,voice} · error
import { Journal, parseJournal, episodes, parseAtoms, wakeLabel } from './journal.js';
import { FSTTM } from 'fsttm-web/fsttm';
import { Attention } from 'fsttm-web/attention';
import { Dialog, attachRecognizer } from 'fsttm-web/dialog';
import { BrowserTTS, splitSentences } from 'fsttm-web/tts';

const $ = id => document.getElementById(id);
const el = (tag, attrs = {}, ...kids) => {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === 'class') e.className = v; else if (k.startsWith('on')) e[k] = v; else if (v != null) e.setAttribute(k, v);
  }
  for (const c of kids.flat()) if (c != null) e.append(c.nodeType ? c : String(c));
  return e;
};
const q = new URLSearchParams(location.search);
const REPLAYS = ['fixtures/live-1710.jsonl', 'fixtures/step1-smoke-1728.jsonl', 'fixtures/smoke.jsonl', 'fixtures/int-step2b.jsonl'];
// the catalog when no service says it (a replay): the live config's two loaded domains
const FALLBACK_CATALOG = {
  domains: [
    { name: 'dog', kind: 'domain', display: 'the dog', line: 'a robot dog: walks to places, finds things, sits and stands', loaded: true, state: 'idle',
      has: { program: true, host: true, notch: false, agent: true, intent: true, readme: true } },
    { name: 'kitchen', kind: 'domain', display: 'the kitchen', line: 'the kitchen: the first cook, its orders and soups', loaded: true, state: 'idle',
      has: { program: true, host: true, notch: true, agent: true, intent: false, readme: true } },
  ],
};
const DISPLAY = { kitchen_shared: 'second cook' };

// ── state ────────────────────────────────────────────────────────────────
const J = new Journal({ display: DISPLAY });
let catalog = FALLBACK_CATALOG, catalogLive = false;
const ports = {};             // domain → the ports reply
let rendered = 0;             // J.chat rows on the page
let tab = 'dog', sub = 'stream';

// ── the shell: the docks and their strip, the splitter (fsttm-web lib/shell.js) ──
const dockUI = MetisShell.docks({
  names: ['chat', 'catalog', 'domain', 'world'], key: 'agentconsole.docks.v1',
  defaults: { chat: { shown: true, open: true }, catalog: { shown: true, open: true },
              domain: { shown: true, open: true }, world: { shown: true, open: false } },
});
dockUI.apply();
MetisShell.splitter();
const rowEl = new Map();      // chat seq → its element
const jEl = new Map();        // journal seq → its element
const turnLines = new Map();  // a turn id → its chat row (the line the intent frame voiced)
let ws = null;
const sess = Math.random().toString(36).slice(2, 7);
const replay = q.get('replay');

// ── the voice: fsttm-web's floor ────────────────────────────────────────
const table = await (await fetch('lib/fsttm.json')).json();
const fsttm = new FSTTM(table);
const attention = new Attention({ enabled: false, startAsleep: false });
const tts = new BrowserTTS();
const waiting = new Map();    // wire id → resolve(frame)
const dialog = new Dialog({
  fsttm, attention, tts, bargeIn: q.get('barge_in') ?? 'words', log: onDialog,
  llm: async (text, { id }) => ({ voice: await turn(id, text) }),
  // what was heard of a line: a narration's goes back as heard{id}, a turn's as cut{id}
  onHeard: (id, heard) => {
    if (id?.startsWith('n:')) send({ ev: 'heard', id: id.slice(2), text: heard });
    else if (id) send({ ev: 'cut', id: `${sess}-${id}`, heard });
  },
});
const voiceOn = () => $('voiceOn').checked && (!replay || q.has('voice'));
if (replay && !q.has('voice')) $('voiceOn').checked = false;

async function turn(id, text) {
  if (!ws) return '';
  const wid = `${sess}-${id}`;
  const f = await new Promise(res => { waiting.set(wid, res); send({ ev: 'say_text', id: wid, text, mode: 'intent' }); });
  waiting.delete(wid);
  if (f.ev === 'error') { J.chat.push({ who: 'note', text: `error: ${f.error}`, kind: 'error' }); renderChat(); return ''; }
  if (f.voice) turnLines.set(id, f.voice);
  return voiceOn() ? (f.voice ?? '') : '';
}

let speakingRow = null;
function onDialog(kind, f = {}) {
  if (kind === 'fsttm') $('floor').textContent = f.dst;
  if (kind === 'keyword_stop') send({ ev: 'say_text', id: `${sess}-stop${Date.now()}`, text: f.text, mode: 'intent' }); // the dog's stop is a person turn too
  if (kind === 'speak') markSpoken(f.sentences?.join(' '));
  if (kind === 'cut') markCut(f);
  if (kind === 'rec_error' && f.error !== 'no-speech') note(`microphone: ${f.error}`);
}

function markSpoken(text) {
  const r = lastAgentRow(text);
  if (!r) return;
  speakingRow = r;
  r.el?.querySelector('.tags')?.append(el('span', { class: 'tag spoken' }, '🔊 spoken'));
}
function markCut(f) {
  const r = speakingRow;
  const tagText = `✂ cut · heard "${f.heard ?? ''}"${f.estimated ? ' (estimated)' : ''}`;
  if (r?.el) r.el.querySelector('.tags')?.append(el('span', { class: 'src s-cut', title: f.rest ? `unsaid: ${f.rest}` : '' }, tagText));
  else note(tagText);
}
const normT = s => (s ?? '').toLowerCase().replace(/[^a-z0-9 ]+/g, ' ').replace(/\s+/g, ' ').trim();
function lastAgentRow(text) {
  const t = normT(text);
  for (let i = J.chat.length - 1; i >= Math.max(0, J.chat.length - 30); i--) {
    const r = J.chat[i];
    if (r.who === 'agent' && (normT(r.text).startsWith(t.slice(0, 40)) || t.startsWith(normT(r.text).slice(0, 40)))) return r;
  }
  return null;
}

// ── the wire ─────────────────────────────────────────────────────────────
function send(m) { if (ws?.readyState === 1) ws.send(JSON.stringify(m)); }

function connectLive() {
  const url = `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/ws`;
  ws = new WebSocket(url);
  ws.onopen = () => { setConn('live · ' + location.host, 'ok'); send({ ev: 'hello', since: lastSeq() }); };
  ws.onclose = () => { setConn('disconnected — retrying', 'bad'); for (const r of waiting.values()) r({ ev: 'error', error: 'ws closed' }); setTimeout(connectLive, 2000); };
  ws.onmessage = e => { let f; try { f = JSON.parse(e.data); } catch { return; } frame(f); };
}
const lastSeq = () => J.recs.length ? J.recs[J.recs.length - 1].seq ?? 0 : 0;
function setConn(text, cls) { $('conn').textContent = text; $('conn').className = `v ${cls ?? ''}`; }

function frame(f) {
  switch (f.ev) {
    case 'journal': if (f.rec && !(f.rec.seq != null && J.bySeq.has(f.rec.seq))) ingest(f.rec); break;
    case 'catalog': catalog = { domains: f.domains ?? [] }; catalogLive = true; renderDomains(); renderTabs(); staleNews(); break;
    case 'ports': ports[f.domain] = f; if (tab === f.domain) renderWorldDock(); break;
    case 'domain_ack': if (f.error) note(`${f.verb} ${f.domain}: ${f.error}`); renderDomains(); break;
    case 'reload_ack': renderDomains(); break; // the journal's dlg-domain reload row says it (its key or its error) in the chat
    case 'world_ack': if (f.error) note(`world ${f.domain}: ${f.error}`); else if (tab === f.domain) renderWorldDock(); break;
    case 'narration': narration(f); break;
    case 'intent': case 'error': { const w = waiting.get(f.id); if (w) w(f); else if (f.ev === 'error') note(`error: ${f.error}`); break; }
    case 'domains': break; // the pre-v3 attach list: the catalog frame replaces it
  }
}

// a line the service sends to be voiced: the floor permitting (dialog.say parks it)
function narration(f) {
  if (f.kind === 'v3-turn') return; // the turn's own line: its intent frame voices it
  const text = f.form === 'R' ? f.line : (f.sentence || f.line);
  if (text && voiceOn()) dialog.say(splitSentences(text), 'narration', `n:${f.id}`);
}

// ── the journal in ───────────────────────────────────────────────────────
function ingest(rec) {
  J.add(rec);
  renderChat();
  renderJournalRow(rec);
  if (rec.kind === 'dlg-wake' || rec.kind === 'dlg-trace' || rec.kind === 'dlg-world' || rec.kind === 'dlg-domain') {
    if (rec.domain) addDomFilter(rec.domain);
    renderDomains(); renderTabs();
    if (!rec.domain || rec.domain === tab) { renderTab(); if (rec.kind === 'dlg-world') renderWorldDock(); }
  }
  if (rec.kind === 'dlg-say') $('b-last').textContent = rec.text ?? '';
  $('b-recs').textContent = J.recs.length;
}

function note(text, act) { J.chat.push({ who: 'note', text, act, t: new Date().toISOString() }); renderChat(); }

// human-loop step 1: a served domain whose files changed on disk is STALE in
// the band; under reload: propose (the default) the chat proposes the reload,
// one click; under auto the backend reloads it at its next quiet beat (the
// journal's reload row says so)
const proposed = new Map();   // domain → the changed line it was proposed for (and its button's row)
function staleNews() {
  const stale = catalog.domains.filter(d => d.changed && (d.loaded || d.served));
  $('b-stale').textContent = stale.map(d => d.name).join(', ') || '—';
  $('b-stale').classList.toggle('on', stale.length > 0);
  if (!catalogLive) return;
  for (const d of catalog.domains) {
    const was = proposed.get(d.name);
    if (!d.changed) {      // reloaded (or edited back): its proposal is spent
      if (was?.row?.actBtn) was.row.actBtn.disabled = true;
      proposed.delete(d.name);
      continue;
    }
    if (was?.changed === d.changed || !(d.loaded || d.served)) continue;
    if (was?.row?.actBtn) was.row.actBtn.disabled = true;
    if (d.reload === 'auto') {
      note(`${d.name} changed on disk: ${d.changed} — it reloads by itself at its next quiet beat (reload: auto)`);
      proposed.set(d.name, { changed: d.changed });
    } else {
      note(`${d.name} changed on disk: ${d.changed} — reload?`, { label: `reload ${d.name}`, fn: () => send({ ev: 'reload', domain: d.name }) });
      proposed.set(d.name, { changed: d.changed, row: J.chat[J.chat.length - 1] });
    }
  }
}

// ── chat ─────────────────────────────────────────────────────────────────
const hhmmss = t => t ? new Date(t).toLocaleTimeString([], { hour12: false }) : '';
function renderChat() {
  const box = $('chat');
  const atEnd = box.scrollTop + box.clientHeight >= box.scrollHeight - 30;
  for (; rendered < J.chat.length; rendered++) {
    const r = J.chat[rendered];
    const body = el('div', { class: 'body' }, el('div', { class: 'text' }, r.text));
    const meta = el('div', { class: 'meta tags' });
    if (r.src && r.who !== 'you') {
      const chip = el('span', { class: `src s-${r.src.by}`, title: r.src.detail ?? '' }, r.src.label);
      chip.onclick = () => showSource(r, body);
      meta.append(chip);
      if (r.derived) meta.append(el('span', { class: 'tag derived', title: 'the journal had no src on this line: read off the records before it' }, 'derived'));
      if (r.viaTurn) meta.append(el('span', { class: 'tag', title: 'the person turn\'s own line (its intent frame)' }, 'turn line'));
    } else if (r.who === 'you') {
      meta.append(el('span', { class: 'src s-person' }, 'you'));
    }
    if (r.kind) meta.append(el('span', { class: 'tag' }, r.kind));
    if (r.act) {     // a one-click proposal (the reload a changed domain asks for)
      const b = el('button', { class: 'btn act' }, r.act.label);
      b.onclick = () => { b.disabled = true; r.act.fn(); };
      r.actBtn = b;
      meta.append(b);
    }
    if (meta.childNodes.length) body.append(meta);
    const m = el('div', { class: `msg ${r.who}` }, el('span', { class: 't' }, hhmmss(r.t)), body);
    r.el = m;
    if (r.seq != null) rowEl.set(r.seq, m);
    box.append(m);
  }
  if (atEnd) box.scrollTop = box.scrollHeight;
}

// a label clicked: its detail, and the records it rests on lit in the journal
function showSource(r, body) {
  body.querySelector('.detail')?.remove();
  const s = r.src;
  const seqs = [r.seq, s.gen, s.wake, s.move].filter(x => x != null);
  const gen = s.gen != null ? J.bySeq.get(s.gen) : null;
  const wake = s.wake != null ? J.bySeq.get(s.wake) : null;
  const lines = [s.detail];
  if (wake) lines.push(`answers #${wake.seq} ${wakeLabel(wake)}${wake.text ? ': "' + wake.text + '"' : ''}${wake.voice ? ' (the domain\'s text: ' + wake.voice + ')' : ''}`);
  if (gen) lines.push(`generation #${gen.seq}: ${gen.label}/${gen.branch || '—'}${gen.harness ? ' (harness)' : ''}${gen.ms != null ? ', ' + gen.ms + ' ms' : ''}${gen.n_ctx ? ', at ' + gen.n_ctx + ' tok' : ''}`);
  body.append(el('div', { class: 'detail' }, lines.filter(Boolean).join(' · ')));
  for (const e of document.querySelectorAll('.j.hl')) e.classList.remove('hl');
  for (const q of seqs) { const e = jEl.get(q); if (e) { e.classList.add('hl'); e.scrollIntoView({ block: 'nearest' }); } }
}

// ── domains ──────────────────────────────────────────────────────────────
function domState(name) {
  const d = J.domains.get(name);
  if (!d) return { text: 'idle', cls: '' };
  if (d.surprise) return { text: `surprise #${d.surprise.record} open${d.surprise.difference ? ': ' + diffText(d.surprise.difference) : ''}`, cls: 'surprise' };
  if (d.task) return { text: `task ${d.task}`, cls: 'task' };
  return { text: d.state ?? 'idle', cls: '' };
}
// the catalog: metis_catalog's index (the loop console's rows: the display
// line, what each domain has), each with its state here. One click opens a
// domain: an active one shows its tab; an inactive one is activated.
const HAS = ['agent', 'intent', 'host', 'notch', 'readme'];
// a domain no agent serves is installed by its activate (hot-install from its manifest): the backend says if it cannot
function canActivate(d) { return ws && catalogLive && !d.loaded && d.kind !== 'partner' && !/pair/.test(d.state ?? ''); }
function stateOf(d) {
  let text = d.state ?? '';
  if (!catalogLive) text = d.loaded ? domState(d.name).text : 'not loaded';
  const diff = J.domains.get(d.name)?.surprise?.difference;
  if (diff && /surprise/.test(text) && !/: /.test(text)) text += ': ' + diffText(diff);
  const cls = /surprise/.test(text) ? 'surprise' : /task/.test(text) ? 'task' : d.loaded ? 'active' : '';
  return { text: d.loaded && /^idle$/.test(text) ? 'active · idle' : text, cls };
}
function openDomain(d) {
  if (d.loaded || /pair/.test(d.state ?? '')) { selectTab(d.partner_of || (/pair/.test(d.state ?? '') ? 'kitchen' : d.name)); return; }
  if (!canActivate(d)) {
    note(!catalogLive ? `${d.name}: activating needs the live service`
      : d.kind === 'partner' ? `${d.name} is stepped with its pair: activate ${d.partner_of ?? 'its lead'}` : `${d.name}: cannot be activated`);
    return;
  }
  send({ ev: 'activate', domain: d.name });
  pendingTab = d.name;
}
let pendingTab = null;
function renderDomains() {
  const box = $('domains');
  box.replaceChildren();
  const active = [];
  for (const d of catalog.domains) {
    if (d.loaded) active.push(d.name);
    const st = stateOf(d);
    const tags = HAS.filter(k => d.has && k in d.has).map(k => el('span', { class: d.has[k] ? 'tag' : 'tag off', title: d.has[k] ? `it has ${k}` : `no ${k}` }, k));
    const acts = el('span', { class: 'acts' });
    if (d.loaded && !/pair/.test(d.state ?? '')) {
      const off = el('button', { class: 'btn', title: 'deactivate(domain): detach it from the runtime tree' }, 'deactivate');
      off.disabled = !ws || !catalogLive;
      off.onclick = e => { e.stopPropagation(); send({ ev: 'deactivate', domain: d.name }); off.disabled = true; };
      acts.append(off);
    }
    if (d.served !== false && (d.loaded || d.served) && !(d.partner_of || /pair/.test(d.state ?? ''))) {
      const rl = el('button', { class: 'btn', title: d.changed ? `reload: the files changed — ${d.changed}` : 'reload(domain): the domain agent recompiles from its files' }, d.changed ? 'reload ●' : 'reload');
      rl.disabled = !ws || !catalogLive;
      rl.onclick = e => { e.stopPropagation(); send({ ev: 'reload', domain: d.name }); rl.disabled = true; };
      acts.append(rl);
    }
    const row = el('div', { class: `dm${d.name === tab ? ' cur' : ''}`, tabindex: '0', role: 'button', 'data-dom': d.name,
      title: d.loaded ? `open ${d.name}'s tab` : canActivate(d) ? (d.served === false ? `activate ${d.name}: its agent is installed from the manifest` : `activate ${d.name}`) : '' },
      el('div', { class: 'hd' }, el('span', { class: 'nm' }, d.name), d.display && d.display !== d.name ? el('span', { class: 'empty' }, d.display) : null,
        el('span', { class: `st ${st.cls}`, title: st.text }, st.text.length > 46 ? st.text.slice(0, 44) + '…' : st.text || '—'), ...tags, acts),
      d.line ? el('div', { class: 'ln' }, d.line) : null,
      d.changed ? el('div', { class: 'chg' }, `changed on disk: ${d.changed}`) : null);
    row.onclick = () => openDomain(d);
    row.onkeydown = e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); openDomain(d); } };
    box.append(row);
  }
  box.append(el('div', { class: 'foot empty' }, catalogLive
    ? `${catalog.domains.length} domain(s) from metis_catalog — the program-only catalogs open in the loop console`
    : 'the catalog comes from the live service (a replay shows the loaded domains)'));
  $('b-active').textContent = active.join(', ') || '—';
  if (pendingTab && active.includes(pendingTab)) { const t = pendingTab; pendingTab = null; selectTab(t); }
}

// ── per-domain tabs ──────────────────────────────────────────────────────
function tabNames() {
  const names = catalog.domains.filter(d => d.loaded).map(d => d.name);
  for (const n of J.domains.keys()) if (!names.includes(n)) names.push(n);
  return names;
}
function selectTab(n) {
  tab = n; renderTabs(); renderTab(); renderWorldDock(); renderDomains();
  if (n !== 'kitchen_shared') send({ ev: 'ports', domain: n });
  if (!dockUI.state.domain?.shown || !dockUI.state.domain?.open) dockUI.set('domain', { shown: true, open: true });
}
function renderTabs() {
  const box = $('tabs');
  const names = tabNames();
  if (!names.includes(tab) && names.length) tab = names[0];
  box.replaceChildren(...names.map(n => {
    const d = J.domains.get(n);
    const b = el('button', { class: 'chip', 'aria-pressed': String(n === tab) }, DISPLAY[n] ? `${n} (${DISPLAY[n]})` : n, d?.surprise ? ' ⚠' : '');
    b.onclick = () => selectTab(n);
    return b;
  }));
  $('domTitle').textContent = `DOMAIN · ${tab}`;
  $('worldTitle').textContent = `WORLD PORTS · ${tab}`;
  $('b-tab').textContent = tab;
}
for (const b of document.querySelectorAll('#subtabs button')) b.onclick = () => {
  sub = b.dataset.sub;
  for (const x of document.querySelectorAll('#subtabs button')) x.setAttribute('aria-pressed', String(x === b));
  renderTab();
};

const diffText = d => typeof d === 'string' ? d : Object.entries(d ?? {}).map(([a, n]) => `${a.replace(/_/g, ' ')} ${n > 0 ? '+' : ''}${n}`).join(', ');

function renderTab() {
  const box = $('tabBody');
  const d = J.domains.get(tab) ?? { stream: [], trace: [], world: [], events: 0 };
  if (sub === 'stream') {
    const rows = d.stream.map(w => {
      const what = (w.what ?? '').replace(':', ' ');
      const cls = w.what === 'surprise' ? 'surprise' : w.what === 'notice' ? 'notice' : '';
      const extra = w.what === 'surprise'
        ? el('div', { class: 'diff' }, `record #${w.record ?? '?'}${w.difference ? ' · difference: ' + diffText(w.difference) : ''}`)
        : null;
      return el('div', { class: `row ${cls}` }, el('span', { class: 'k' }, `#${w.seq}`), el('span', { class: 'k' }, `${what}${w.cid ? ' ' + w.cid : ''}`),
        el('span', {}, w.text || '—', w.voice ? el('span', { class: 'tag' }, `  (${w.voice})`) : null, extra));
    });
    box.replaceChildren(...(rows.length ? rows : [el('div', { class: 'empty' }, 'nothing from this domain yet')]),
      d.events ? el('div', { class: 'empty' }, `+ ${d.events} clause events (shown in the journal with "clause events")`) : '');
  } else {
    const rows = d.trace.map(traceEl);
    box.replaceChildren(...(rows.length ? rows : [el('div', { class: 'empty' },
      'no REPL log yet: the domain agent sends a trace per line with --trace (py 86c6c98), journalled as dlg-trace')]));
  }
  const body = box.closest('.dbody');
  if (body) body.scrollTop = body.scrollHeight;
}
function renderWorldDock() {
  renderWorld($('worldBody'), J.domains.get(tab) ?? { stream: [], trace: [], world: [], events: 0 });
}

function traceEl(t) {
  const parts = [el('div', { class: 'hd' }, `#${t.seq} · beat ${t.beat ?? '?'}`)];
  if (t.fired?.length) parts.push(el('div', { class: 'fired' }, ...t.fired.map(f => el('div', { class: f.kind === 'exo' ? 'exo' : '' },
    `${String(f.seq).padStart(3)} ${f.event}  `, f.consume?.length ? `−${f.consume.join(' −')} ` : '', f.produce?.length ? `+${f.produce.join(' +')}` : '',
    f.line ? el('span', { class: 'line' }, `  “${f.line}”`) : null)), t.more ? el('div', { class: 'exo' }, `… ${t.more} more`) : null));
  for (const n of t.narrated ?? []) parts.push(el('div', {}, `narrated ${n.kind} #${n.seq}: `, el('i', {}, n.sentence || '—'),
    n.held === false ? el('span', { class: 'held-no' }, ` — not held; the line was spoken: “${n.line}”`) : '', n.ms != null ? el('span', { class: 'tag' }, ` ${n.ms} ms`) : ''));
  for (const c of t.consults ?? []) parts.push(el('div', {}, `consult (${c.situation}, ${c.stop ?? ''}${c.ms != null ? ', ' + c.ms + ' ms' : ''}): `, el('i', {}, c.text || ''),
    el('pre', {}, JSON.stringify(c.answer ?? {}), c.calls?.length ? '\n' + c.calls.join('\n') : '')));
  for (const r of t.reads ?? []) parts.push(el('div', {}, `${r.tool} ${r.arg}`, el('pre', {}, r.out)));
  for (const w of t.world ?? []) parts.push(el('div', {}, `world: ${w.join(' ')}`));
  // the records sink (e2e step 1): each surprise record and its close, as the loop console reads it
  for (const r of t.records ?? []) parts.push(el('div', { class: r.status === 'escalated' ? 'held-no' : '' },
    `record #${r.record} ${r.status} → ${String(r.file).split('/').slice(-3).join('/')}`, r.mined_text ? el('pre', {}, r.mined_text) : ''));
  return el('div', { class: 'trace' }, ...parts);
}

function renderWorld(box, d) {
  const p = ports[tab];
  const out = [el('div', { class: 'ports' })];
  const root = out[0];
  if (!p) {
    root.append(el('div', { class: 'empty' }, ws ? 'asking the domain agent for its ports…' : 'the ports come from the live service (ports frame)'));
    if (ws && tab !== 'kitchen_shared') send({ ev: 'ports', domain: tab });
  } else if (!p.world) {
    root.append(el('div', { class: 'empty' }, `${tab}'s host takes no world lines: it brings its own day.`));
  } else {
    const sides = new Map(), blocking = new Set(); // a side → its partner; the pairs' second sides block
    for (const [a, b] of p.pairs ?? []) { sides.set(a, b); sides.set(b, a); blocking.add(b); }
    const btn = atom => {
      const [pred, args = ''] = atom.split(/(?=\()/);
      const other = sides.get(pred);
      if (!other || ['elsewhere', 'tag_near'].some(x => args.includes(x))) return null;
      const lines = [`-${atom}`, `+${other}${args}`];
      return el('button', { title: lines.join(' '), onclick: () => world(tab, lines) }, `${atom} → ${other}${args}`);
    };
    const held = p.held ?? [];
    const now = held.filter(a => blocking.has(a.split('(')[0])).map(btn).filter(Boolean);
    const rest = held.filter(a => !blocking.has(a.split('(')[0])).map(btn).filter(Boolean);
    root.append(el('h3', {}, 'blocking now — click to clear (applied on the next beat)'));
    root.append(el('div', { class: 'flip' }, ...(now.length ? now : [el('span', { class: 'empty' }, 'nothing blocks')])));
    root.append(el('details', {}, el('summary', { class: 'quiet' }, `the other sides (${rest.length}) — click to block`), el('div', { class: 'flip' }, ...rest)));
    root.append(el('h3', {}, 'the world reports'));
    const rep = el('div', { class: 'flip' });
    for (const [r, kind] of Object.entries(p.reports ?? {})) {
      if (!kind) { rep.append(el('button', { onclick: () => world(tab, [`+${r}`]) }, `+${r}`)); continue; }
      const sel = el('select', {}, ...(p.handles?.[kind] ?? []).map(h => el('option', { value: h.id }, `${h.id} (${h.display})`)));
      rep.append(el('span', {}, el('button', { onclick: () => world(tab, [`+${r}(${sel.value})`]) }, `+${r}(`), sel, ')'));
    }
    root.append(rep);
  }
  root.append(el('h3', {}, 'atoms'));
  const inp = el('input', { placeholder: '-closed(kitchen) +open(kitchen)', value: '' });
  const form = el('form', {}, inp, el('button', {}, 'send'));
  form.onsubmit = e => {
    e.preventDefault();
    const { lines, bad } = parseAtoms(inp.value, p?.world ? p : null);
    if (bad.length) { note(`not a port of ${tab}: ${bad.join(' ')}${p ? ' — its sides and reports are listed above' : ''}`); return; }
    if (lines.length) { world(tab, lines); inp.value = ''; }
  };
  root.append(form);
  if (p?.handles) root.append(el('div', { class: 'empty' }, Object.entries(p.handles).map(([k, hs]) => `${k}: ${hs.map(h => h.id).join(', ')}`).join(' · ')));
  if (d.world?.length) {
    root.append(el('h3', {}, 'sent'));
    for (const w of d.world.slice(-12)) root.append(el('div', { class: 'diff' }, `#${w.seq} ${(w.lines ?? []).join(' ')}${w.error ? ' — ' + w.error : ''}`));
  }
  box.replaceChildren(...out);
}

function world(domain, atoms) {
  if (!ws) { note('world lines need the live service'); return; }
  send({ ev: 'world', domain, atoms, mode: domain, text: atoms.join(' ') });
}

// ── the journal pane ─────────────────────────────────────────────────────
const KINDS = ['dlg-wake', 'dlg-gen', 'dlg-say', 'dlg-dispatch', 'dlg-world', 'dlg-trace', 'dlg-domain', 'other'];
const kindOn = new Set(KINDS.filter(k => k !== 'dlg-trace'));
$('kindFilter').append(...KINDS.map(k => {
  const c = el('input', { type: 'checkbox' }); c.checked = kindOn.has(k);
  c.onchange = () => { c.checked ? kindOn.add(k) : kindOn.delete(k); refilter(); };
  return el('label', {}, c, k.replace('dlg-', ''));
}));
const domsSeen = new Set();
function addDomFilter(d) { if (domsSeen.has(d)) return; domsSeen.add(d); $('domFilter').append(el('option', { value: d }, d)); }
$('domFilter').onchange = $('textFilter').oninput = $('showEvents').onchange = () => refilter();

function jText(r) {
  switch (r.kind) {
    case 'dlg-wake': return r.what === 'person' ? `“${r.person}”` : (r.text ?? '');
    case 'dlg-gen': return `${r.harness ? '[harness] ' : ''}${r.text ?? ''}${r.ms != null ? `  (${r.ms} ms, ${r.n_ctx} tok)` : ''}`;
    case 'dlg-say': return `${r.text}${r.src ? `  ← ${r.src.by}${r.src.gen != null ? ' #' + r.src.gen : ''}` : ''}`;
    case 'dlg-dispatch': return `${r.cid} ${r.j}`;
    case 'dlg-world': return `${r.domain}: ${(r.lines ?? []).join(' ')}${r.error ? ' — ' + r.error : ''}`;
    case 'dlg-trace': return `${(r.fired ?? []).length} fired · ${(r.narrated ?? []).length} narrated · ${(r.reads ?? []).length} reads${r.consults ? ' · consult' : ''}`;
    default: return JSON.stringify({ ...r, kind: undefined, seq: undefined, t: undefined, id: undefined, rep: undefined });
  }
}
function visible(r) {
  const k = KINDS.includes(r.kind) ? r.kind : 'other';
  if (!kindOn.has(k)) return false;
  if (r.kind === 'dlg-wake' && r.what === 'event' && !r.text && !$('showEvents').checked) return false;
  const dom = $('domFilter').value;
  if (dom && r.domain !== dom) return false;
  const tf = $('textFilter').value.trim().toLowerCase();
  if (tf && !JSON.stringify(r).toLowerCase().includes(tf)) return false;
  return true;
}
function renderJournalRow(r) {
  const box = $('journal');
  const atEnd = box.scrollTop + box.clientHeight >= box.scrollHeight - 30;
  const e = el('div', { class: `j k-${r.kind} ${r.what ?? ''}` },
    el('span', { class: 'kd' }, r.seq ?? ''), el('span', { class: 'kd' }, hhmmss(r.t)), el('span', {}, r.kind),
    el('span', {}, r.what ?? r.label ?? '', r.branch ? '/' + r.branch : ''), el('span', { class: 'kd' }, r.domain ?? ''),
    el('span', { title: jText(r) }, jText(r)));
  e.hidden = !visible(r);
  e.onclick = () => { const m = rowEl.get(r.seq); if (m) { for (const x of document.querySelectorAll('.msg.hl')) x.classList.remove('hl'); m.classList.add('hl'); m.scrollIntoView({ block: 'nearest' }); } };
  if (r.seq != null) jEl.set(r.seq, e);
  e._rec = r;
  // in seq order: a live frame can come before the hello's replay of older records
  let after = box.lastElementChild;
  while (r.seq != null && after && after._rec?.seq != null && after._rec.seq > r.seq) after = after.previousElementSibling;
  if (after === box.lastElementChild) box.append(e); else if (after) after.after(e); else box.prepend(e);
  if (atEnd) box.scrollTop = box.scrollHeight;
}
function refilter() { for (const e of $('journal').children) e.hidden = !visible(e._rec); }

// ── input: typed, the mic ────────────────────────────────────────────────
$('say').onsubmit = e => {
  e.preventDefault();
  const t = $('sayText').value.trim();
  if (!t) return;
  $('sayText').value = '';
  if (!ws) { note('replay: typed turns need the live service'); return; }
  dialog.final(t);
};
let rec = null;
$('micBtn').onclick = () => {
  if (rec) { rec.stop(); rec = null; $('micBtn').textContent = 'mic'; $('micBtn').classList.remove('on'); return; }
  const SR = window.SpeechRecognition || window.webkitSpeechRecognition;
  if (!SR) { note('no SpeechRecognition in this browser'); return; }
  rec = attachRecognizer(new SR(), dialog);
  $('micBtn').textContent = 'mic on'; $('micBtn').classList.add('on');
};
$('legendBtn').onclick = () => { $('legend').hidden = !$('legend').hidden; };

// ── replay: a recorded journal, the static mock ──────────────────────────
async function startReplay(path) {
  $('replayBar').hidden = false;
  $('micBtn').disabled = true;
  setConn('replay · no service', '');
  const text = await (await fetch(path)).text();
  const eps = episodes(text);
  let id = q.get('id') ?? (eps.length > 1 ? eps[0] : null);
  const pick = $('replayPick');
  pick.replaceChildren(...REPLAYS.map(p => el('option', { value: p }, p.replace('fixtures/', ''))),
    ...(eps.length > 1 ? eps.map(e => el('option', { value: `${path}#${e}` }, `  ${path.replace('fixtures/', '')} · ${e}`)) : []));
  pick.value = id && eps.length > 1 ? `${path}#${id}` : path;
  pick.onchange = () => { const [p, e] = pick.value.split('#'); const u = new URLSearchParams({ replay: p }); if (e) u.set('id', e); location.search = u; };
  $('replayName').textContent = path + (id ? ` · ${id}` : '');
  // the dog's ports as the domain agent answers them (py 86c6c98), for the world tab's layout
  try { ports.dog = { ev: 'ports', ...(await (await fetch('fixtures/ports-dog.json')).json()) }; ports.kitchen = { ev: 'ports', domain: 'kitchen', world: false }; } catch {}
  const recs = parseJournal(text, { id, rep: q.has('rep') ? Number(q.get('rep')) : null });
  let i = 0, timer = null;
  const pos = () => { $('replayPos').textContent = `${i} / ${recs.length}`; };
  const step = () => {
    if (i >= recs.length) { stop(); J.end(); renderChat(); return false; }
    ingest(recs[i++]); pos(); return true;
  };
  const stop = () => { clearTimeout(timer); timer = null; $('replayPlay').textContent = '▶ play'; };
  const play = () => {
    $('replayPlay').textContent = '❚❚ pause';
    const tick = () => {
      const r = recs[i - 1], n = recs[i];
      if (!step()) return;
      const speed = Number($('replaySpeed').value);
      const dt = r?.t && n?.t ? Math.max(0, Date.parse(n.t) - Date.parse(r.t)) : 300;
      timer = setTimeout(tick, Math.min(4000, dt / speed));
    };
    tick();
  };
  $('replayStep').onclick = () => { stop(); step(); };
  $('replayPlay').onclick = () => timer ? stop() : play();
  $('replaySpeed').value = q.get('speed') ?? '10';
  pos();
  if (q.has('all')) { while (step()); } else play();
}

renderDomains(); renderTabs(); renderTab(); renderWorldDock();
if (replay) startReplay(replay);
else connectLive();
window.consoleJournal = J; // for CDP drivers
