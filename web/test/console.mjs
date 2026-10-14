// node web/test/console.mjs — the console's journal model on the real
// journals (fixtures/): every spoken line gets a source, and the sources
// are the ones the records say.
import { readFileSync } from 'fs';
import { Journal, parseJournal, parseAtoms, episodes } from '../journal.js';
const fx = f => readFileSync(new URL(`../fixtures/${f}`, import.meta.url), 'utf8');
let pass = 0, fail = 0;
const ok = (c, m) => { if (c) pass++; else { fail++; console.log('FAIL', m); } };
const run = (f, opt) => { const J = new Journal({ display: { kitchen_shared: 'second cook' } }); for (const r of parseJournal(fx(f), opt)) J.add(r); J.end(); return J; };

// every dlg-say becomes an agent row with a source, in every fixture and episode
for (const f of ['live-1710.jsonl', 'step1-smoke-1728.jsonl', 'smoke.jsonl']) {
  const J = run(f), says = J.recs.filter(r => r.kind === 'dlg-say').length;
  const agent = J.chat.filter(r => r.who === 'agent' && !r.viaTurn);
  ok(agent.length === says && agent.every(r => r.src?.label), `${f}: ${says} says, ${agent.length} labelled`);
}
for (const id of episodes(fx('int-step2b.jsonl'))) {
  const J = run('int-step2b.jsonl', { id });
  ok(J.chat.filter(r => r.who === 'agent').every(r => r.src?.by && r.src.label), `int-step2b ${id}: unlabelled line`);
}

// the step-1 smoke: the person turn's own line, a relay, the kitchen's template
const S = run('step1-smoke-1728.jsonl');
const by = t => S.chat.find(r => r.text?.startsWith(t));
ok(by('go to the door')?.who === 'you', 'person turn');
ok(by('I am just standing here')?.src.label === 'model · person' && by('I am just standing here').viaTurn, 'the turn line: model · person, via its intent frame');
ok(by('I set off for the door')?.src.by === 'relay' && by('I set off for the door').src.domain === 'dog', 'the dog\'s text relayed');
const tmpl = by('The second cook explains it');
ok(tmpl?.src.by === 'harness' && /kitchen surprise/.test(tmpl.src.label) && /explain/.test(tmpl.src.detail), 'the surprise template, after the model\'s explain');
ok(by('The kitchen is quiet after one move')?.src.label === 'model · kitchen quiescence', 'a quiescence the model wrote');
ok(S.domains.get('dog').state === 'idle' && S.domains.get('dog').stream.length === 2, 'the dog\'s stream');

// the 17:10 live session: the surprise with its record and difference, closed by the notice
const L = run('live-1710.jsonl');
const sur = L.domains.get('kitchen').stream.find(w => w.what === 'surprise');
ok(sur?.record === '0' && sur.difference === 'pot onion +1, pot space -1', 'surprise record + difference');
ok(L.domains.get('kitchen').surprise === null, 'the notice closed it');
ok(!L.chat.some(r => r.who === 'you'), 'no person turn in the 17:10 session (the brief\'s finding)');

// corpus: an ask and a bound "no"
const D3 = run('int-step2b.jsonl', { id: 'D3-b' });
ok(D3.chat.find(r => r.text.startsWith('Can you open'))?.src.label === 'model · ask (answer)', 'the ask, the model\'s');
ok(D3.chat.find(r => r.text.startsWith("Alright, I'll leave it"))?.src.by === 'harness', 'the line after a bound no: the harness');
ok(run('int-step2b.jsonl', { id: 'D3-b' }).chat.length === D3.chat.length && parseJournal(fx('int-step2b.jsonl'), { id: 'D3-b' }).every(r => r.rep === 0), 'one rep');

// a service src wins over the derivation
const T = new Journal();
[{ kind: 'dlg-wake', seq: 1, what: 'result:done', domain: 'dog', text: 'I have arrived at the door.', voice: 'narrator' },
 { kind: 'dlg-gen', seq: 2, label: 'voice', branch: 'outcome', text: 'I have arrived at the door.' },
 { kind: 'dlg-say', seq: 3, what: 'voice', text: 'I have arrived at the door.', src: { by: 'model', gen: 2, wake: 1, domain: 'dog', branch: 'outcome' } },
 { kind: 'dlg-say', seq: 4, what: 'template', text: 'Alright.', src: { by: 'harness', wake: 1 } }].forEach(r => T.add(r));
ok(T.chat[0].src.label === "model · outcome · copies dog's narrator" && !T.chat[0].derived, 'src: relay of the narrator');
ok(T.chat[1].src.label === 'harness · template', 'src: harness');

// world atoms
ok(JSON.stringify(parseAtoms('-closed(kitchen) +open(kitchen) arrived(kitchen)')) === JSON.stringify({ lines: ['-closed(kitchen)', '+open(kitchen)', '+arrived(kitchen)'], bad: [] }), 'atoms');
const P = { pairs: [['open', 'closed']], reports: { arrived: 'place', timeout: null } };
ok(parseAtoms('open(the door)').bad.length === 2, 'not an atom');
ok(JSON.stringify(parseAtoms('open the door', P).bad) === '["the","door"]', 'not a port');
ok(parseAtoms('-closed(kitchen) +open(kitchen) +arrived(sofa) +timeout', P).lines.length === 4, 'ports');

console.log(`${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
