// journal.js — the console's model of the orchestrator's journal (the dlg-*
// JSON lines go/internal/agent writes, results/live/session.jsonl): every
// record in, the views' rows out. Pure (no DOM): node tests it on the real
// journals (test/console.mjs).
//
// The one question the console must answer for every spoken line is WHERE
// IT CAME FROM. A dlg-say carries src{by, gen, wake, domain, branch} when
// the service writes it (agent-console step 1); for a journal without it
// (every journal before the console) the source is derived from the
// records before the say, and the label says it was derived.
//
//   by        what the label says
//   harness   harness · template — the harness's words, no model
//   model     model · <branch> — a generation (dlg-gen voice / ask)
//   relay     model · <branch>, copying <domain>'s line — the generation is
//             the domain's own text word for word (its narrator's sentence,
//             or its %% line when the narrator's did not hold: wake.voice)
//   rollback  harness · rollback — a forged line rolled back to "Okay."

export const SAY_KINDS = new Set(['dlg-say']);

const norm = s => (s ?? '').toLowerCase().replace(/[^a-z0-9 ]+/g, ' ').replace(/\s+/g, ' ').trim();

// the branch a voice generation answers, when the gen did not name it: the
// wake it follows
function branchOf(gen, wake) {
  if (gen?.branch) return gen.branch;
  if (!wake) return '?';
  if (wake.what === 'person') return 'person';
  return (wake.what ?? '?').replace(':', ' ');
}

// a move's call by name: {"intent":"CANCEL"} → CANCEL, call:explain{…} → explain
export function moveName(t) {
  const m = /call:(\w+)/.exec(t ?? '') ?? /"(?:intent|call)":"(\w+)"/.exec(t ?? '');
  return m ? m[1] : (t ?? '').slice(0, 30);
}

export function wakeLabel(w) {
  if (!w) return '';
  if (w.what === 'person') return 'your turn';
  const dom = w.domain ? `${w.domain} ` : '';
  return `${dom}${(w.what ?? '').replace(':', ' ')}${w.record != null ? ' #' + w.record : ''}`;
}

export class Journal {
  constructor({ display = {} } = {}) {
    this.display = display;  // domain → its display name (kitchen_shared → second cook)
    this.recs = [];
    this.bySeq = new Map();
    this.lastWake = null;    // the wake being handled
    this.lastGen = null;     // the last generation (any label)
    this.lastVoiceGen = null;
    this.lastMove = null;
    this.chat = [];          // {seq, who: you|agent|note, text, src?, t}
    this.domains = new Map();// name → {name, state, task, surprise, stream: [], trace: [], world: []}
    this.pendingVoice = null;// a person-branch voice gen with no say yet (the turn's own line)
  }

  name(d) { return this.display[d] ?? d; }

  domain(d) {
    if (!this.domains.has(d)) this.domains.set(d, { name: d, state: 'idle', task: null, surprise: null, stream: [], trace: [], world: [], last: null });
    return this.domains.get(d);
  }

  // add one record; returns the rows it made ({chat?, stream?}) for a live view
  add(r) {
    this.recs.push(r);
    if (r.seq != null) this.bySeq.set(r.seq, r);
    const out = {};
    switch (r.kind) {
      case 'dlg-wake': out.chat = this.wake(r); break;
      case 'dlg-gen': out.chat = this.gen(r); break;
      case 'dlg-say': out.chat = this.say(r); break;
      case 'dlg-dispatch': out.chat = this.dispatch(r); break;
      case 'dlg-world': out.chat = this.world(r); break;
      case 'dlg-trace': {
        const d = this.domain(r.domain);
        d.trace.push(r); out.trace = r;
        for (const x of r.records ?? []) d.stream.push({ seq: r.seq, what: `record ${x.status}`, record: x.record, text: x.mined_text || `record #${x.record} ${x.status} (sunk)` });
        break;
      }
      case 'dlg-domain': out.chat = this.note(r, `${r.verb ?? r.op ?? 'activate'} ${r.domain}${r.by ? ' (by ' + r.by + ')' : ''}${r.error ? ': ' + r.error : r.key ? ': reloaded (' + String(r.key).slice(0, 12) + ')' : ''}`); break;
    }
    return out;
  }

  push(row) { this.chat.push(row); return row; }

  note(r, text) { return this.push({ seq: r.seq, t: r.t, who: 'note', text }); }

  wake(r) {
    this.flushPending();
    this.lastWake = r;
    if (r.what === 'person') {
      return this.push({ seq: r.seq, t: r.t, who: 'you', text: r.person, src: { by: 'person', label: 'you' } });
    }
    if (!r.domain) return null;
    const d = this.domain(r.domain);
    d.last = r;
    const what = r.what ?? '';
    if (what !== 'event' || r.text) d.stream.push(r);
    else { d.events = (d.events ?? 0) + 1; }
    if (what.startsWith('result:')) {
      const st = what.slice(7);
      if (st === 'progress' || st === 'accepted') d.task = r.cid || d.task, d.state = `task ${r.cid || '?'}`;
      else { if (d.task === r.cid || !r.cid) d.task = null; d.state = d.surprise ? d.state : 'idle'; }
    } else if (what === 'surprise') {
      d.surprise = { record: r.record ?? '?', text: r.text, difference: r.difference ?? null, seq: r.seq };
      d.state = `surprise #${d.surprise.record} open`;
    } else if (what === 'notice' && d.surprise) {
      d.surprise.closed = r.seq; d.surprise.notice = r.text;
      d.state = d.task ? `task ${d.task}` : 'idle';
      d.surprise = null;
    } else if (what === 'quiescence' && !d.surprise && !d.task) d.state = 'idle';
    return null;
  }

  gen(r) {
    this.lastGen = r;
    if (r.label === 'voice') {
      this.flushPending();
      this.lastVoiceGen = r;
      // the person turn's own line goes to the page in its intent frame, not
      // as a dlg-say (journals before the console): spoken unless a say follows
      if ((r.branch === 'person') && this.lastWake?.what === 'person') this.pendingVoice = r;
    } else if (r.label === 'move') {
      this.lastMove = r;
      // the surprise's difference, when the wake did not carry it: the
      // harness's consult question is templated from it (DESIGN c5043a6)
      const m = /differ from the model: ([^?<]+)\?/.exec(r.text ?? '');
      const d = this.lastWake?.what === 'surprise' && this.domains.get(this.lastWake.domain);
      if (m && d?.surprise && !d.surprise.difference) {
        d.surprise.difference = m[1].trim();
        this.lastWake.difference ??= m[1].trim();
        d.state = `surprise #${d.surprise.record} open`;
      }
    }
    return null;
  }

  flushPending() {
    const g = this.pendingVoice;
    if (!g) return null;
    this.pendingVoice = null;
    return this.push({ seq: g.seq, t: g.t, who: 'agent', text: g.text, src: this.source({ what: 'voice', text: g.text }, g), derived: true, viaTurn: true });
  }

  dispatch(r) {
    let j = r.j;
    try { const o = JSON.parse(r.j); j = Object.entries(o).map(([k, v]) => k === 'intent' ? v : `${k}=${typeof v === 'object' ? (v.description ?? JSON.stringify(v)) : v}`).join(' '); } catch {}
    return this.push({ seq: r.seq, t: r.t, who: 'note', text: `→ ${r.cid} ${j}`, kind: 'dispatch' });
  }

  world(r) {
    const d = this.domain(r.domain ?? 'dog');
    d.world.push(r);
    return this.push({ seq: r.seq, t: r.t, who: 'note', kind: 'world', text: `world ${r.domain}: ${(r.lines ?? []).join(' ')}${r.error ? ' — ' + r.error : ''}` });
  }

  say(r) {
    if (this.pendingVoice && norm(this.pendingVoice.text) === norm(r.text)) this.pendingVoice = null;
    else this.flushPending();
    const gen = r.src?.gen != null ? this.bySeq.get(r.src.gen) : (r.what === 'voice' ? this.lastVoiceGen : r.what === 'ask' ? this.lastMove : null);
    return this.push({ seq: r.seq, t: r.t, who: 'agent', text: r.text, src: this.source(r, gen), derived: !r.src });
  }

  // source: {by, label, domain?, branch?, gen?, wake?, detail}
  source(say, gen) {
    if (say.src) return this.labelled(say.src, say);
    const wake = this.lastWake;
    const domain = wake?.domain;
    if (say.what === 'template') {
      return { by: 'harness', label: `harness · template${wake && wake.what !== 'person' ? ' · ' + wakeLabel(wake).replace(/ #\S+$/, '') : ''}`, branch: this.lastMove?.branch, wake: wake?.seq, domain, move: this.lastMove?.seq,
        detail: `the harness's words (no model) after ${wakeLabel(wake) || 'the move'}${this.lastMove ? `, on the ${this.lastMove.harness ? "harness's" : "model's"} move #${this.lastMove.seq} (${moveName(this.lastMove.text)})` : ''}` };
    }
    if (say.what === 'rollback' || gen?.label === 'rollback') return { by: 'rollback', label: 'harness · rollback', detail: 'a forged line rolled back' };
    const branch = say.what === 'ask' ? (gen?.branch ?? 'ask') : branchOf(gen, wake);
    const harnessGen = gen?.harness === true;
    if (say.what === 'ask') {
      return { by: harnessGen ? 'harness' : 'model', label: `${harnessGen ? 'harness' : 'model'} · ask (${branch})`, branch, gen: gen?.seq, wake: wake?.seq, domain,
        detail: `the ask's text from the ${harnessGen ? 'harness' : "model's move"}${gen ? ' #' + gen.seq : ''}` };
    }
    if (wake?.text && domain && norm(say.text) === norm(wake.text)) {
      const who = wake.voice === 'line' ? `${this.name(domain)}'s %% line` : wake.voice === 'narrator' ? `${this.name(domain)}'s narrator` : `${this.name(domain)}'s text`;
      return { by: 'relay', label: `model · ${branch} · copies ${who}`, branch, gen: gen?.seq, wake: wake.seq, domain,
        detail: `the model's line #${gen?.seq ?? '?'} is ${who} word for word (wake #${wake.seq}: ${wakeLabel(wake)})` };
    }
    return { by: 'model', label: `model · ${domain && !gen?.branch ? this.name(domain) + ' ' : ''}${branch}`, branch, gen: gen?.seq, wake: wake?.seq, domain,
      detail: `generated (#${gen?.seq ?? '?'}${gen?.ms != null ? ', ' + gen.ms + ' ms' : ''}) after ${wakeLabel(wake) || 'the turn'}` };
  }

  // the service's own src (agent-console step 1) → a label
  labelled(s, say = {}) {
    const wake = s.wake != null ? this.bySeq.get(s.wake) : null;
    const gen = s.gen != null ? this.bySeq.get(s.gen) : null;
    const domain = s.domain ?? wake?.domain;
    let branch = s.branch ?? gen?.branch ?? '?';
    // the service names a voice gen with no branch "outcome": a quiescence, a
    // notice or an event is not one — the wake says what it answered
    if (branch === 'outcome' && wake && wake.what !== 'person' && !(wake.what ?? '').startsWith('result')) branch = wakeLabel(wake).replace(/ #\S+$/, '');
    if (s.by === 'harness') return { ...s, domain, label: 'harness · template', detail: `the harness's words after ${wakeLabel(wake) || 'the move'}` };
    if (s.by === 'rollback') return { ...s, label: 'harness · rollback', detail: 'a forged line rolled back' };
    if (say.what === 'ask') return { ...s, domain, branch, label: `model · ask (${branch})`, detail: `the ask's text from the model's move #${s.gen ?? '?'}` };
    const copy = wake?.text && gen && norm(gen.text) === norm(wake.text);
    if (copy) {
      const who = wake.voice === 'line' ? `${this.name(domain)}'s %% line` : wake.voice === 'narrator' ? `${this.name(domain)}'s narrator` : `${this.name(domain)}'s text`;
      return { ...s, by: 'relay', domain, branch, label: `model · ${branch} · copies ${who}`, detail: `the model's line #${s.gen} is ${who} word for word` };
    }
    return { ...s, domain, branch, label: `model · ${branch}`, detail: `generated #${s.gen ?? '?'}${gen?.ms != null ? ' (' + gen.ms + ' ms)' : ''} after ${wakeLabel(wake) || 'the turn'}` };
  }

  // finish: what is still pending at the end of a replay
  end() { return this.flushPending(); }
}

// journal lines (a JSONL text) → records; `id` picks one episode of a corpus
// journal (results/e13/int-step2b.jsonl holds D1-a … D4-c)
export function parseJournal(text, { id = null, rep = null } = {}) {
  const recs = [];
  for (const l of text.split('\n')) {
    if (!l.trim()) continue;
    let r;
    try { r = JSON.parse(l); } catch { continue; }
    if (!r.kind?.startsWith?.('dlg-')) continue;
    if (id && r.id !== id) continue;
    rep ??= r.rep ?? 0;          // one rep of a corpus episode (the first, unless asked)
    if ((r.rep ?? 0) !== rep) continue;
    recs.push(r);
  }
  return recs;
}

export function episodes(text) {
  const ids = [];
  for (const r of parseJournal(text)) if (r.id && !ids.includes(r.id)) ids.push(r.id);
  return ids;
}

// world input: "-closed(kitchen) +open(kitchen)" → the do-lines; an atom
// without a sign is added (+). ports (the domain's ports reply) names the
// predicates the host knows: any other is `bad` too, so the person does not
// send a word the world will never read
export function parseAtoms(s, ports = null) {
  const out = [], bad = [];
  const known = ports ? new Set([...(ports.pairs ?? []).flat(), ...Object.keys(ports.reports ?? {})]) : null;
  for (const w of (s ?? '').trim().split(/\s+/).filter(Boolean)) {
    const m = /^([+-]?)([a-z_][a-z0-9_]*)(\(([a-z0-9_]+(,[a-z0-9_]+)*)\))?$/i.exec(w);
    if (!m || (known && !known.has(m[2]))) { bad.push(w); continue; }
    out.push(`${m[1] || '+'}${m[2]}${m[3] ?? ''}`);
  }
  return { lines: out, bad };
}
