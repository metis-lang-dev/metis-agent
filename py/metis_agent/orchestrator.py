"""The orchestrator agent (DESIGN v3 "The orchestrator agent", -loop v3), in
Python: a port of go/internal/agent orch.go, orch3.go, v3text.go and
v3reply.go, which stay the reference it must match (agent-console
s-7f3d1088 step 2; DESIGN "The process shape": t3 parity 114/114, the 14
timelines' reads, the integration replay's reads, the same dlg-* journal).

The generic reply in; the branch derived from it and named in a cue; the
next move generated under the branch's grammar (J for the intent interface
and the person's ask, T for the REPL API); the repeat guard; the move
handed to the domains, their answer rendered and the line spoken. The
dialog is one context: what the agent renders is decoded as it goes, the
model generates only where it moves or speaks.

Byte parity with the Go reference is the contract: every chunk the agent
decodes and every grammar it generates under is the Go agent's, checked
without the model against a trace the Go side dumps
(tests/test_orchestrator_parity.py), then gen for gen on the model.
"""
import json
import logging
import re
import time

log = logging.getLogger("orchestrator")

# ── JSON as Go reads it ───────────────────────────────────────────────────────


class Num(str):
    """A JSON number kept as its literal (Go's json.Number)."""


def jloads(s):
    """JSON with numbers as their literals (Go's Decoder.UseNumber)."""
    return json.loads(s, parse_int=Num, parse_float=Num)


def jdump(v):
    """Compact JSON (no spaces, key order as given, Num as its literal)."""
    if isinstance(v, Num):
        return str(v)
    if v is None:
        return "null"
    if v is True:
        return "true"
    if v is False:
        return "false"
    if isinstance(v, str):
        return json.dumps(v, ensure_ascii=False)
    if isinstance(v, (int, float)):
        return gofloat(v)
    if isinstance(v, dict):
        return "{" + ",".join(json.dumps(k, ensure_ascii=False) + ":" + jdump(x) for k, x in v.items()) + "}"
    if isinstance(v, (list, tuple)):
        return "[" + ",".join(jdump(x) for x in v) + "]"
    raise TypeError("jdump: %r" % (v,))


def compact_json(text):
    """Go's json.Compact: the whitespace outside strings dropped, nothing else."""
    out, in_str, esc = [], False, False
    for ch in text:
        if in_str:
            out.append(ch)
            if esc:
                esc = False
            elif ch == "\\":
                esc = True
            elif ch == '"':
                in_str = False
        elif ch == '"':
            in_str = True
            out.append(ch)
        elif ch not in " \t\r\n":
            out.append(ch)
    return "".join(out)


def gofloat(x):
    """A number as Go's %v / %g prints a float64: the shortest form."""
    if isinstance(x, bool):
        return "true" if x else "false"
    if isinstance(x, int):
        x = float(x)
    if x == int(x) and abs(x) < 1e21:
        return str(int(x))
    r = repr(x)
    if "e" in r:
        m, e = r.split("e")
        sign = "-" if e.startswith("-") else "+"
        e = e.lstrip("+-").rjust(2, "0")
        return m + "e" + sign + e
    return r


def goval(v):
    """%v of a value json.Unmarshal put in an any."""
    if isinstance(v, (int, float)) and not isinstance(v, bool):
        return gofloat(v)
    if v is None:
        return "<nil>"
    if v is True or v is False:
        return "true" if v else "false"
    return str(v)


def _jmap(raw):
    """json.Unmarshal(raw, &map[string]any): numbers as float64, {} on error."""
    try:
        m = json.loads(raw) if raw else {}
    except ValueError:
        return {}
    return m if isinstance(m, dict) else {}


# ── calls and replies (v3reply.go) ────────────────────────────────────────────

Q3 = '<|"|>'   # the template's string quote


class Call:
    """One outbound call: a J object (an intent, or the person's ask; `j`
    is its JSON text) or a T call of the REPL API (`name` + `args`)."""
    __slots__ = ("name", "args", "j")

    def __init__(self, name="", args=None, j=None):
        self.name, self.args, self.j = name or "", args, j or None

    def __repr__(self):
        return "Call(%s)" % self.text()

    def is_j(self):
        return bool(self.j)

    def text(self):
        return compact_json(self.j) if self.is_j() else tool_call(self.name, self.args)

    def verb(self):
        if not self.is_j():
            return self.name
        m = _jmap(self.j)
        for k in ("call", "intent"):
            if isinstance(m.get(k), str):
                return m[k]
        return ""

    def arg(self, k):
        if self.is_j():
            s = _jmap(self.j).get(k)
        else:
            s = (self.args or {}).get(k)
        return s if isinstance(s, str) else ""

    @classmethod
    def of(cls, obj):
        """A Call from its JSON form ({name, args} | {j})."""
        if not obj:
            return cls()
        j = obj.get("j")
        return cls(obj.get("name", ""), obj.get("args"), jdump(j) if j is not None and not isinstance(j, str) else j)


def fmt_arg(v):
    """The template's format_argument: dict keys sorted and bare, strings in <|"|>."""
    if v is None:
        return "null"
    if v is True:
        return "true"
    if v is False:
        return "false"
    if isinstance(v, Num):
        return str(v)
    if isinstance(v, str):
        return Q3 + v + Q3
    if isinstance(v, (int, float)):
        return gofloat(v)
    if isinstance(v, dict):
        return "{" + ",".join(k + ":" + fmt_arg(v[k]) for k in sorted(v)) + "}"
    if isinstance(v, (list, tuple)):
        return "[" + ",".join(fmt_arg(x) for x in v) + "]"
    return str(v)


def tool_call(name, args):
    s = "{}" if args is None else fmt_arg(dict(args))
    return "<|tool_call>call:" + name + s + "<tool_call|>"


def flex_text(v):
    """FlexText: a string as it is, a number as its literal, an object as "key ±n" words."""
    if v is None:
        return ""
    if isinstance(v, Num):
        return str(v).strip()
    if isinstance(v, str):
        return v
    if isinstance(v, dict):
        parts = []
        for k in sorted(v):
            n = str(v[k]) if isinstance(v[k], Num) else gofloat(v[k])
            parts.append(k.replace("_", " ") + " " + (n if n.startswith("-") else "+" + n))
        return ", ".join(parts)
    if isinstance(v, (int, float)):
        return gofloat(v)
    return jdump(v)


_ABSENT = object()


class Reply:
    """DESIGN v3's generic reply as the Go agent reads it (the Reply struct:
    its fields and their omitempty, FlexText for record and difference)."""
    STR = ("domain", "kind", "id", "status", "reason", "tool", "card", "move", "error", "answer_kind", "text")
    RAW = ("blocker", "output", "counts", "goal_pending", "cards", "ref", "unsettled")

    def __init__(self, obj=None):
        obj = obj or {}
        for k in self.STR:
            v = obj.get(k)
            setattr(self, k, v if isinstance(v, str) else ("" if v is None else str(v)))
        for k in self.RAW:   # a RawMessage: absent, or the value (None for a JSON null)
            setattr(self, k, obj[k] if k in obj else _ABSENT)
        self.alternatives = list(obj.get("alternatives") or [])
        self.record = flex_text(obj.get("record"))
        self.difference = flex_text(obj.get("difference"))
        c = obj.get("cause")
        self.cause = {"card": str(c.get("card", "")), "move": str(c.get("move", ""))} if isinstance(c, dict) else None
        r = obj.get("resource")
        self.resource = ({"atom": str(r.get("atom", "")), "words": str(r.get("words", "")), "key": str(r.get("key", "") or "")}
                         if isinstance(r, dict) else None)
        self.evidence = [str(x) for x in (obj.get("evidence") or [])]
        try:
            self.calls = int(obj.get("calls") or 0)
        except (TypeError, ValueError):
            self.calls = 0
        self.over = bool(obj.get("over"))

    def blocker_text(self):
        """string(r.Blocker): "" absent, "null", or the raw JSON."""
        return "" if self.blocker is _ABSENT else jdump(self.blocker)

    def to_map(self):
        """replyMap: json.Marshal of the struct, decoded with numbers kept."""
        m = {"domain": self.domain, "kind": self.kind}
        for k in ("id", "status", "reason"):
            if getattr(self, k):
                m[k] = getattr(self, k)
        if self.alternatives:
            m["alternatives"] = self.alternatives
        for k in ("blocker", "tool", "output", "counts", "goal_pending", "record", "difference", "cards",
                  "card", "move", "error", "answer_kind"):
            v = getattr(self, k)
            if k in self.RAW:
                if v is not _ABSENT:
                    m[k] = v
            elif v:
                m[k] = v
        if self.cause is not None:
            m["cause"] = dict(self.cause)
        if self.resource is not None:
            m["resource"] = {k: v for k, v in self.resource.items() if v or k != "key"}
        if self.evidence:
            m["evidence"] = list(self.evidence)
        if self.calls:
            m["calls"] = Num(str(self.calls))
        if self.ref is not _ABSENT:
            m["ref"] = self.ref
        if self.text:
            m["text"] = self.text
        if self.over:
            m["over"] = True
        if self.unsettled is not _ABSENT:
            m["unsettled"] = self.unsettled
        return m


def tool_response(name, r, note):
    m = r.to_map()
    if note:
        m["note"] = note
    return "<|tool_response>response:" + name + fmt_arg(m) + "<tool_response|>"


# ── reading a generation ─────────────────────────────────────────────────────

_T_CALL = re.compile(r"call:([A-Za-z_]+)\{(.*)\}", re.S)
_T_STR = re.compile(r'<\|"\|>(.*?)<\|"\|>', re.S)


def call_of(text):
    """The first call in a generation, (name, key): a native call
    (call:NAME{k:<|"|>v<|"|>,…}) or a JSON object ({"call"|"intent":NAME,…});
    the key is NAME and the top-level string values in order, ':'-joined."""
    i = text.find("<|tool_call>")
    if i >= 0 or text.startswith("call:"):
        s = text[max(i, 0):]
        j = s.find("<tool_call|>")
        if j >= 0:
            s = s[:j]
        m = _T_CALL.search(s)
        if not m:
            return "", ""
        parts, depth, args, k = [m.group(1)], 0, m.group(2), 0
        while k < len(args):
            ch = args[k]
            if ch in "{[":
                depth += 1
            elif ch in "}]":
                depth -= 1
            elif depth == 0 and args.startswith(Q3, k):
                v = _T_STR.match(args, k)
                if v:
                    parts.append(v.group(1))
                    k += len(v.group(0)) - 1
            k += 1
        return m.group(1), ":".join(parts)
    i = text.find("{")
    if i < 0:
        return "", ""
    return _json_call_key(text[i:])


def _json_tokens(s):
    """Go's json.Decoder.Token over s: (kind, value) for delimiters and
    strings, numbers/literals as ('lit', text); raises at an error."""
    dec = json.JSONDecoder()
    k, n = 0, len(s)
    ws = " \t\r\n"
    while True:
        while k < n and s[k] in ws + ",:":
            k += 1
        if k >= n:
            raise ValueError("eof")
        ch = s[k]
        if ch in "{}[]":
            yield ("delim", ch)
            k += 1
        elif ch == '"':
            v, k = dec.raw_decode(s, k)
            yield ("str", v)
        else:
            m = re.compile(r"-?\d+(\.\d+)?([eE][-+]?\d+)?|true|false|null").match(s, k)
            if not m:
                raise ValueError("bad token")
            yield ("lit", m.group(0))
            k = m.end()


def _json_call_key(s):
    name, vals, depth, want_key, key = "", [], 0, False, ""
    try:
        for kind, t in _json_tokens(s):
            if kind == "delim":
                if t in "{[":
                    depth += 1
                    if depth == 1:
                        want_key = True
                else:
                    depth -= 1
                if depth == 0:
                    if not name:
                        return "", ""
                    return name, ":".join([name] + vals)
                if depth == 1 and t in "}]":
                    want_key = True
                continue
            if kind == "str":
                if depth == 1 and want_key:
                    key, want_key = t, False
                    continue
                if depth == 1:
                    if key in ("call", "intent"):
                        name = t
                    else:
                        vals.append(t)
            if depth == 1:
                want_key = True
    except (ValueError, StopIteration):
        return "", ""
    return "", ""


def _first_json(text):
    """The first JSON value from the first '{' (Go's Decoder.Decode), or raises."""
    i = text.find("{")
    if i < 0:
        raise ValueError("no object")
    v, end = json.JSONDecoder().raw_decode(text, i)
    return v, text[i:end]


def canon_json(text):
    """The first JSON object, keys sorted, compact, HTML unescaped (Python's
    json.dumps(sort_keys=True, separators=(",", ":"), ensure_ascii=False))."""
    try:
        v, _ = _first_json(text)
    except ValueError:
        return ""

    def norm(x):    # Go decodes numbers to float64 and encodes 5.0 as 5
        if isinstance(x, float) and x == int(x) and abs(x) < 1e21:
            return int(x)
        if isinstance(x, dict):
            return {k: norm(y) for k, y in x.items()}
        if isinstance(x, list):
            return [norm(y) for y in x]
        return x
    return json.dumps(norm(v), sort_keys=True, separators=(",", ":"), ensure_ascii=False)


def parse_call(text):
    """A generation as a Call: a native call with its arguments, or the
    first JSON object; None when it is neither."""
    i = text.find("<|tool_call>call:")
    if i >= 0:
        s = text[i + len("<|tool_call>call:"):]
        j = s.find("{")
        if j <= 0:
            return None
        p = _NativeParser(s[j:])
        ok, v = p.value()
        if not ok or not isinstance(v, dict):
            return None
        return Call(s[:j], v)
    try:
        _, raw = _first_json(text)
    except ValueError:
        return None
    return Call(j=raw)


class _NativeParser:
    """The template's argument format: {k:v,…}, [v,…], <|"|>s<|"|>, bare literals."""

    def __init__(self, s):
        self.s, self.i = s, 0

    def value(self):
        s = self.s
        if self.i >= len(s):
            return False, None
        if s.startswith(Q3, self.i):
            k = s.find(Q3, self.i + len(Q3))
            if k < 0:
                return False, None
            v = s[self.i + len(Q3):k]
            self.i = k + len(Q3)
            return True, v
        if s[self.i] == "{":
            self.i += 1
            m = {}
            while self.i < len(s) and s[self.i] != "}":
                k = s.find(":", self.i)
                if k < 0:
                    return False, None
                key = s[self.i:k]
                self.i = k + 1
                ok, v = self.value()
                if not ok:
                    return False, None
                m[key] = v
                if self.i < len(s) and s[self.i] == ",":
                    self.i += 1
            self.i += 1
            return True, m
        if s[self.i] == "[":
            self.i += 1
            out = []
            while self.i < len(s) and s[self.i] != "]":
                ok, v = self.value()
                if not ok:
                    return False, None
                out.append(v)
                if self.i < len(s) and s[self.i] == ",":
                    self.i += 1
            self.i += 1
            return True, out
        m = re.compile(r"[,}\]]").search(s, self.i)
        k = m.start() if m else len(s)
        lit = s[self.i:k]
        self.i = k
        if lit in ("true", "false", "null"):
            return True, {"true": True, "false": False, "null": None}[lit]
        return True, Num(lit)


# ── the text conventions (v3text.go) ──────────────────────────────────────────

TURN_END = "<turn|>\n"
MODEL_TURN = "<|turn>model\n"
SPEAK_CUE = "Spoken response:"
RESP_TAG = "[Runtime response — not something the person said.] "
CHANGE_TAG = ("[Context change — not something the person said; answer with exactly one call: activate, "
              "deactivate or keep.] ")
TELL_NOW = " Tell the person in one short first-person sentence what you are doing now."
CUE_GENERIC = ("[Next move — exactly one JSON call: an offered alternative, else consult the domain; "
               "after a consult answer, the move its kind names; never the refused call again.]")
NOTE_REPEAT_J = ("[Runtime note — not something the person said.] Your last call repeats the call that was just "
                 "refused. Choose a different move. " + CUE_GENERIC)
NOTE_REPEAT_T = "this call repeats one already answered; choose a different move"
G_TEXT = "[a-zA-Z0-9 ,.;:'()?!+-]{8,200}"


def user_turn(text):
    return "<|turn>user\n" + text.strip("\r\n") + TURN_END


STATE_CUE = {
    "alts": "[Next move — alternatives were offered: call one of them, exactly as offered. Exactly one call.]",
    "none": "[Next move — no alternatives were offered: consult the domain this call belongs to, asking why. Exactly one call.]",
    "answer": "[Next move — the consult answered: make the move its answer_kind names. Exactly one call.]",
    "again": "[Next move — the same refusal came back after your move: ask the person or escalate. Exactly one call.]",
}


def cue_ask(words):
    return ("[Next move — the consult answered missing: ask the person, in one short question, for " + words +
            ". Exactly one call.]")


def cue_ask_offer(words, offers):
    o = [describe(c).removeprefix("your command to ") for c in offers]
    return ("[Next move — the consult answered missing: ask the person, in one short question, for " + words +
            ", or whether to " + " or ".join(o) + " instead. Exactly one call.]")


def cue_ask_again(words, lead_in):
    if lead_in:
        return ("[Next move — the same refusal came back after your move. You have just said: \"" + line_still(words) +
                "\" Now ask the person one short question for it. Exactly one call.]")
    return ("[Next move — the same refusal came back after your move: " + words + " is still missing. "
            "Ask the person once more, in one short question, saying it is still missing. Exactly one call.]")


def line_still(words):
    return "I asked you to " + words + " before, and it is still not done."


def note_ask_words(words):
    return ("[Runtime note — not something the person said.] Your question does not name " + words +
            ". Ask again, in one short question that names " + words + ". Exactly one call.")


LINE_ESCALATE = "I've passed this to the people who revise the model."
LINE_RESET = "That reading was noise, so I'm carrying on."
LINE_LEAVE_IT = "Alright, I'll leave it."


def line_explain(display, domain, card, move):
    who = (display or {}).get(card) or words(card)
    return "The %s explains it: %s, %s. The %s carries on." % (who, words(card), words(move), domain)


def words(ident):
    return ident.replace("_", " ")


# the REPL API, declared — from metispy, where the REPL lives (repl-tools
# s-0c6879a1 step 2): metis.lang.repl_api is the one source; nothing here
# restates a verb, a description or a parameter

def repl_api(catalog, loaded):
    """The REPL API's verbs from the installed metispy, as the renderer and
    alt_call take them: (name, description, [{name, type, desc, enum}],
    required); a parameter's enum_from fills its enum — "loaded" from the
    domains served, "catalog" from the domains activate can attach."""
    from metis.lang.repl_api import repl_api as published
    fill = {"loaded": list(loaded), "catalog": list(catalog)}
    out = []
    for v in published():
        ps = v.get("parameters") or {}
        params = [{"name": k, "type": p["type"], "desc": p.get("description", ""),
                   "enum": list(fill[p["enum_from"]]) if p.get("enum_from") else list(p.get("enum") or [])}
                  for k, p in (ps.get("properties") or {}).items()]
        out.append((v["name"], v["description"], params, list(ps.get("required") or [])))
    return out


# ── several intent domains (Go v3intents.go; e2e-workflow step 4) ─────────────
# The orchestrator dispatches to a domain, and THAT domain's intent material
# maps the words (its manifest's intent.prompt + examples[template] and
# intent.grammar). One intent domain: the turn and the grammar are its own,
# byte for byte as before; two or more: every J names its domain first —
# {"domain":"plant","intent":"WATER"} — under one grammar whose branches are
# the domains' grammars, so the pick and the intent are one generation.

class IntentDomain:
    __slots__ = ("name", "display", "prompt", "grammar", "asks")

    def __init__(self, name, display, prompt, grammar, asks=()):
        self.name, self.display, self.prompt, self.grammar = name, display, prompt, grammar
        self.asks = list(asks or ())     # its verbs that ask the domain something (intent.yaml asks:)

    def __eq__(self, o):
        return isinstance(o, IntentDomain) and all(getattr(self, k) == getattr(o, k) for k in self.__slots__)

    def __repr__(self):
        return "IntentDomain(%s)" % self.name


_SHARED_VERBS = ("", "CHITCHAT", "UNKNOWN", "TIME", "DATE", "STOP", "CANCEL")
_EXAMPLE_LINE = re.compile(r'^"([^"]*)" → (\{.*\})$', re.M)


def _go_quote(s):
    """Go's %q for an utterance (no '"' in it: the example regexp's): the
    backslash escaped, the rest as is."""
    return '"' + s.replace("\\", "\\\\") + '"'


def domain_examples(prompt, n):
    """The prompt's first examples, one per verb, up to n, the verbs every
    domain shares (talk, the clock, stop) left out."""
    out, seen = [], set()
    for m in _EXAMPLE_LINE.finditer(prompt):
        try:
            j = json.loads(m.group(2))
        except ValueError:
            continue
        v = j.get("intent") if isinstance(j, dict) and isinstance(j.get("intent"), str) else ""
        if v in _SHARED_VERBS or v in seen:
            continue
        seen.add(v)
        out.append((m.group(1), m.group(2)))
        if len(out) == n:
            break
    return out


def tag_j(j, domain):
    """"domain" first in a J's text: {"intent":…} → {"domain":"d","intent":…}."""
    t = j.strip()
    return '{"domain":"' + domain + '",' + (t[1:] if t.startswith("{") else t)


def j_domain(c):
    """The domain a J names ("" untagged)."""
    if not c.is_j():
        return ""
    try:
        j = json.loads(c.j)
    except ValueError:
        return ""
    d = j.get("domain") if isinstance(j, dict) else None
    return d if isinstance(d, str) else ""


def tag_call(c, domain):
    """A J named for its domain (an untagged one; a tagged one stands)."""
    if not c.is_j() or j_domain(c):
        return c
    return Call(j=tag_j(c.j, domain))


def intent_turn(ds):
    """The system turn's intent part: the first domain's material as it
    stands, then each other domain's under its heading, then how a J names
    its domain (Go IntentTurn, byte for byte)."""
    if not ds:
        return ""
    b = [ds[0].prompt]
    if len(ds) == 1:
        return b[0]
    for d in ds[1:]:
        b.append("\n## The person's words for " + d.display + " (domain " + d.name + ")\n")
        b.append(d.prompt)
    names = ", ".join(d.display + " (" + d.name + ")" for d in ds)
    b.append("\n## Several domains\nThe person's words are for one of these domains: " + names +
             ". Every intent JSON names that domain first, \"domain\", then the intent in that domain's own words,"
             " as its examples above show:\n")
    for d in ds:
        for utt, j in domain_examples(d.prompt, 3):
            b.append("%s → %s\n" % (_go_quote(utt), tag_j(j, d.name)))
    b.append("\n")
    return "".join(b)


def rule_prefix(name):
    """A GBNF-safe prefix for a domain's rules (kitchen_shared → kitchen-shared-)."""
    return name.replace("_", "-").replace(".", "-") + "-"


_RULE_HEAD = re.compile(r"^([a-zA-Z0-9-]+) ::= ")


def _ident(c):
    return c == "-" or "0" <= c <= "9" or "a" <= c <= "z" or "A" <= c <= "Z"


def prefix_grammar(g, p):
    """Every rule of g renamed, where defined and where named, outside its
    strings and character classes (one rule per line)."""
    out = []
    for line in g.rstrip("\n").split("\n"):
        if not line.strip() or line.strip().startswith("#"):
            continue
        m = _RULE_HEAD.match(line)
        if not m:
            raise ValueError("not one rule per line: %r" % line)
        o = [p + m.group(1) + " ::= "]
        body, i = line[m.end():], 0
        while i < len(body):
            c = body[i]
            if c in "\"[":
                close = '"' if c == '"' else "]"
                j = i + 1
                while j < len(body) and body[j] != close:
                    if body[j] == "\\":
                        j += 1
                    j += 1
                o.append(body[i:j + 1])
                i = j + 1
            elif _ident(c):
                j = i
                while j < len(body) and _ident(body[j]):
                    j += 1
                o.append(p + body[i:j])
                i = j
            else:
                o.append(c)
                i += 1
        out.append("".join(o) + "\n")
    return "".join(out)


def intent_grammar(ds):
    """The person turn's grammar: one domain's own, or the union of each
    domain's — its rules under the domain's prefix, its root's object opened
    with "domain" first (Go IntentGrammar, byte for byte)."""
    if not ds:
        raise ValueError("no intent domain")
    if len(ds) == 1:
        return ds[0].grammar
    alts, rules = [], []
    for d in ds:
        p = rule_prefix(d.name)
        try:
            g = prefix_grammar(d.grammar, p)
        except ValueError as e:
            raise ValueError("%s's intent grammar: %s" % (d.name, e))
        opn = '"{" ' + p + "space "
        head = p + "root ::= " + opn
        i = g.find("\n" + head)
        if i < 0 and g.startswith(head):
            i = -1
        elif i < 0:
            raise ValueError('%s\'s intent grammar: its root is not an object opened by "{" space' % d.name)
        at = i + 1 + len(head)
        g = (g[:at] + '"\\"domain\\"" ' + p + 'space ":" ' + p + 'space "\\"' + d.name + '\\"" ' + p +
             'space "," ' + p + "space " + g[at:])
        alts.append(p + "root")
        rules.append(g if g.endswith("\n") else g + "\n")
    return "root ::= " + " | ".join(alts) + "\n" + "".join(rules)


def asks_of(ds):
    """Per intent domain its asking verbs (Go asksOf); None with no intent
    material (the E13 fixtures: the dog's QUERY)."""
    if not ds:
        return None
    return {d.name: list(d.asks) for d in ds if d.asks}


LINE_NOTHING_RUNNING = "Nothing is running."     # the stop words with no task in flight anywhere


def installed_cards(turn, catalog, lines):
    """The system turn's "## Domains" card list written from the catalog's
    display lines (v3text.go InstalledCards; the manifests are the source,
    the config selects): one "- name: line" per selected domain, in the
    selection's order, replacing the list the prompt file carries."""
    i = turn.find("## Domains")
    if i < 0:
        raise ValueError('the system turn has no "## Domains" section')
    a = turn.find("\nCards:\n", i)
    if a < 0:
        raise ValueError('the "## Domains" section has no Cards: list')
    a += len("\nCards:\n")
    b = turn.find("\n\n", a)
    if b < 0:
        raise ValueError("the Domains card list does not end")
    cards = []
    for n in catalog:
        if not lines.get(n):
            raise ValueError("no display line for %r" % n)
        cards.append("- %s: %s" % (n, lines[n]))
    return turn[:a] + "\n".join(cards) + turn[b:]


def system_turn(prompt, catalog, loaded, lines=None):
    """The orchestrator's system turn (v3service.go SystemTurn): the prompt
    files, their "## Domains" cards written from the catalog's lines (when
    given), the REPL API's declarations over the catalog and the loaded."""
    if lines is not None:
        prompt = installed_cards(prompt, catalog, lines)
    return prompt + api_declarations(catalog, loaded)


def api_declarations(catalog, loaded):
    """The REPL API as the template declares tools (go/tools/gemma_tools.py's macros)."""
    b = []
    for name, desc, params, required in repl_api(catalog, loaded):
        b.append("<|tool>declaration:" + name + "{description:" + Q3 + desc + Q3)
        if params:
            b.append(",parameters:{properties:{")
            for i, p in enumerate(sorted(params, key=lambda x: x["name"])):
                if i:
                    b.append(",")
                b.append(p["name"] + ":{description:" + Q3 + p["desc"] + Q3)
                if p["enum"]:
                    b.append(",enum:[" + ",".join(Q3 + e + Q3 for e in p["enum"]) + "]")
                b.append(",type:" + Q3 + p["type"] + Q3 + "}")
            b.append("},")
            if required:
                b.append("required:[" + ",".join(Q3 + r + Q3 for r in required) + "],")
            b.append("type:" + Q3 + "OBJECT" + Q3 + "}")
        b.append("}<tool|>")
    return "".join(b)


def alt_call(raw, api):
    """An offered alternative: a J object as is (a typed {verb, args} in the
    domain's dictionary as its J), or a REPL call "verb(v, v)" with the
    values on the verb's required parameters in their sorted order."""
    if not isinstance(raw, str):
        if isinstance(raw, dict) and isinstance(raw.get("verb"), str) and raw.get("verb"):
            inner = jdump(raw["args"]) if "args" in raw else ""
            inner = inner.strip()
            inner = inner[1:] if inner.startswith("{") else inner
            inner = inner[:-1] if inner.endswith("}") else inner
            j = '{"intent":' + json.dumps(raw["verb"], ensure_ascii=False)
            if inner and inner != "null":
                j += "," + inner
            return Call(j=j + "}")
        return Call(j=jdump(raw))
    s = raw
    i, j = s.find("("), s.rfind(")")
    if i <= 0 or j < i:
        raise ValueError("v3: alternative %r is not verb(args)" % s)
    name = s[:i]
    inner = s[i + 1:j].strip()
    vals = inner.split(", ") if inner else []
    for t_name, _, _, required in api:
        if t_name != name:
            continue
        req = sorted(required)
        if len(req) != len(vals):
            raise ValueError("v3: alternative %r: %s takes %d values" % (s, name, len(req)))
        return Call(name, dict(zip(req, vals)))
    raise ValueError("v3: alternative %r: no verb %s in the API" % (s, name))


# the next move's grammar, per branch

def g_lit(s):
    return '"' + s.replace("\\", "\\\\").replace('"', '\\"') + '"'


def g_str(rule):
    return g_lit(Q3) + " " + rule + " " + g_lit(Q3)


def g_rules(*lines):
    return "\n".join(lines) + "\n"


def grammar_alts(calls):
    return g_rules("root ::= " + " | ".join(g_lit(c.text()) for c in calls))


def grammar_consult(dom):
    return g_rules("root ::= " + g_lit("<|tool_call>call:consult{domain:" + Q3 + dom + Q3 + ",question:") + " " +
                   g_str("text") + " " + g_lit("}<tool_call|>"), "text ::= " + G_TEXT)


def grammar_explains(dom, record):
    return g_rules("root ::= " + g_lit("<|tool_call>call:explain{card:") + " " + g_str("ident") + " " +
                   g_lit(",domain:" + Q3 + dom + Q3 + ",move:") + " " + g_str("ident") + " " +
                   g_lit(",record:" + Q3 + record + Q3 + "}<tool_call|>"), "ident ::= [a-z_]{3,32}")


def grammar_noise(dom, record):
    return g_rules("root ::= " + g_lit(tool_call("reset", {"domain": dom, "record": record})))


def escalate_rule(dom, record):
    return (g_lit("<|tool_call>call:escalate{domain:" + Q3 + dom + Q3 + ",record:" + Q3 + record + Q3 + ",text:") +
            " " + g_str("text") + " " + g_lit("}<tool_call|>"))


def grammar_unexplained(dom, record):
    return g_rules("root ::= " + escalate_rule(dom, record), "text ::= " + G_TEXT)


ASK_HEAD = '"{\\"call\\":\\"ask\\",\\"text\\":\\""'


def grammar_ask_literal(w):
    return g_rules("root ::= " + ASK_HEAD + " pre " + g_lit(w) + ' post "\\"}"',
                   "pre ::= [a-zA-Z0-9 ,;:'+-]{0,100}", "post ::= [a-zA-Z0-9 ,;:'+-]{0,40} \"?\"")


def grammar_ask():
    return g_rules("root ::= " + ASK_HEAD + ' q "\\"}"',
                   "q ::= qhead [a-zA-Z0-9 ,;:'+-]{4,150} \"?\"",
                   'qhead ::= "Could you " | "Can you " | "Would you " | "Will you " | "Do you " | "Did you " | '
                   '"Shall I " | "Should I " | "Is " | "Are " | "Where " | "What " | "When " | "How "')


def grammar_again(dom, record):
    return g_rules("root ::= ask | " + escalate_rule(dom, record), "ask ::= " + ASK_HEAD + ' text "\\"}"',
                   "text ::= " + G_TEXT)


GRAMMAR_ANSWER = 'root ::= "{\\"answer\\":\\"yes\\"}" | "{\\"answer\\":\\"no\\"}"\n'
GRAMMAR_ANSWER3 = 'root ::= "{\\"answer\\":\\"yes\\"}" | "{\\"answer\\":\\"no\\"}" | "{\\"answer\\":\\"neither\\"}"\n'
GRAMMAR_INFO = 'root ::= "{\\"turn\\":\\"info\\"}" | "{\\"turn\\":\\"command\\"}"\n'


# the command's identity in the runtime's words

def identity(c):
    j = _jmap(c.j)

    def st(m, k):
        v = (m or {}).get(k)
        return v if isinstance(v, str) else ""
    name = st(j, "call")
    if name:
        args = [st(j, k) for k in ("card", "move", "domain") if st(j, k)]
        return "your call %s(%s)" % (name, ", ".join(args))
    i = st(j, "intent")
    if i == "LOCAL_ACTION":
        s = {"STAND_UP": "stand up", "SIT_DOWN": "sit down", "LIE_DOWN": "lie down", "SHAKE": "give your paw",
             "STRETCH": "stretch", "TURN": "turn", "MOVE": "move"}.get(st(j, "action"), "")
        if st(j, "direction"):
            s += " " + st(j, "direction").lower()
        a = j.get("angle_deg")
        if isinstance(a, (int, float)) and not isinstance(a, bool) and a != 0:
            s += " %s degrees" % gofloat(a)
        d = j.get("distance_m")
        if isinstance(d, (int, float)) and not isinstance(d, bool) and d != 0:
            s += " %s m" % gofloat(d)
        return "your command to " + s
    if i == "NAVIGATE":
        g = j.get("goal") if isinstance(j.get("goal"), dict) else {}
        if st(g, "type") == "TAG":
            if "tag_id" in j:
                return "your command to go to tag %s" % goval(j["tag_id"])
            return "your command to go to the tag"
        return "your command to go to the " + st(g, "description")
    if i in ("FIND", "QUERY", "FOLLOW"):
        t = j.get("target") if isinstance(j.get("target"), dict) else {}
        d = st(t, "description")
        if not d.startswith("the ") and not d.startswith("my "):
            d = "the " + d
        return "your command to %s %s" % ({"FIND": "find", "QUERY": "look at", "FOLLOW": "follow"}[i], d)
    if i in ("go", "look"):
        what = {"go": "go to ", "look": "look at "}[i]
        p = st(j, "place").replace("_", " ")
        if not p.startswith("tag"):
            p = "the " + p
        return "your command to " + what + p
    if i in ("find", "query"):
        return "your command to " + {"find": "find", "query": "look for"}[i] + " the " + st(j, "thing").replace("_", " ")
    if i in ("stand", "sit", "lie"):
        return "your command to " + {"stand": "stand up", "sit": "sit down", "lie": "lie down"}[i]
    return "your command " + i.lower()


def cap_first(s):
    return s[:1].upper() + s[1:] if s else s


# the ask's words, checked

_WORD = re.compile(r"[a-z0-9]+")
_STOP_WORD = {"the", "a", "an", "my", "me", "you", "in", "of", "is", "where", "more", "another", "some"}
_STOP_TEMPLATE = {"here", "that", "whatever", "once", "so", "to", "on", "it", "i", "can", "for"}
_NUM_WORD = {"five": "5", "three": "3"}


def content_words(s):
    out = []
    for w in _WORD.findall(s.lower()):
        w = _NUM_WORD.get(w, w)
        if w in _STOP_WORD:
            continue
        if len(w) > 3:
            w = w.rstrip("s")
        out.append(w)
    return out


def names_words_any(ask, wds):
    for alt in wds.split(", or "):
        keep = [w for w in _WORD.findall(alt.lower()) if w not in _STOP_TEMPLATE]
        if names_words(ask, " ".join(keep)):
            return True
    return False


def names_words(ask, wds):
    have = set(content_words(ask)) | set(_WORD.findall(ask.lower()))
    want = content_words(wds)
    if not want:
        want = [w for w in re.findall(r"[a-z]+", wds.lower()) if w not in ("the", "a", "of", "me", "my")]
    return all(w in have or w + "s" in have for w in want)


# ── the dog path's guards and templates the orchestrator borrows (speech.go) ──

ACK_TEMPLATE = {"UNKNOWN": "I can't do that.", "CHITCHAT": "Hello! I'm listening.", "CANCEL": "Okay, cancelled.",
                "STOP": "Stopping.", "NAVIGATE": "On my way.", "FIND": "I'll look for it.",
                "FOLLOW": "Following you.", "QUERY": "Let me look."}


def keyword_stop(text, stop_words):
    f = [w for w in re.split(r"[^a-z0-9']+", text.lower()) if w]
    if not f or len(f) > 3:
        return False
    return any(w in stop_words for w in f)


def forged_fact(t):
    return "⟪" in t or "⟫" in t or "metis seq=" in t.lower()


def unspeakable_line(t):
    return any(ch in t for ch in "{}[]<>") or forged_fact(t)


# ── the orchestrator (orch.go, orch3.go) ──────────────────────────────────────

class V3Config:
    def __init__(self, catalog=(), loaded=(), display=None, ask_check=False, again_pick=False,
                 escalate_template=False, mirror=False, rollback=False, sample_moves=False,
                 on_gen=None, on_say=None, defer=None, intent_domains=(), asks=None, line_voice=()):
        self.catalog, self.loaded, self.display = list(catalog), list(loaded), dict(display or {})
        # the domains whose results are spoken as the fired rule's %% line, never re-voiced by the
        # model (manifest voice: line — a page host or the REPL: console-nav step 2 F4)
        self.line_voice = set(line_voice)
        # the served domains with intent material; two or more: every J names
        # its domain, an alternative a domain offers is named for it
        self.intent_domains = list(intent_domains)
        # per intent domain its verbs that ask (intent.yaml asks:): words in
        # question form mapped to one are the domain's question, never talk;
        # None: no intent material (the dog's QUERY)
        self.asks = None if asks is None else dict(asks)
        self.ask_check, self.again_pick, self.escalate_template = ask_check, again_pick, escalate_template
        self.mirror, self.rollback, self.sample_moves = mirror, rollback, sample_moves
        self.on_gen, self.on_say, self.defer = on_gen, on_say, defer


class GenInfo:
    __slots__ = ("label", "try_", "text", "key", "n_ctx", "first", "all", "harness", "branch", "note")

    def __init__(self, label="", try_=0, text="", key="", n_ctx=0, first=0.0, all=0.0, harness=False, branch="", note=""):
        self.label, self.try_, self.text, self.key, self.n_ctx = label, try_, text, key, n_ctx
        self.first, self.all, self.harness, self.branch, self.note = first, all, harness, branch, note

    def __repr__(self):
        return "GenInfo(%s/%s try %d%s %r)" % (self.branch, self.label, self.try_, " harness" if self.harness else "", self.text)


class Branch:
    __slots__ = ("name", "domain", "record", "refused", "reply", "alts", "pick", "words", "alt", "key", "offers", "again")

    def __init__(self, name="", domain="", record="", refused=None, reply=None, alts=None, pick="", words="",
                 alt="", key="", offers=None, again=None):
        self.name, self.domain, self.record = name, domain, record
        self.refused, self.reply = refused or Call(), reply or Reply()
        self.alts, self.pick, self.words, self.alt, self.key = list(alts or []), pick, words, alt, key
        self.offers, self.again = list(offers or []), again

    def copy(self):
        return Branch(self.name, self.domain, self.record, self.refused, self.reply, self.alts, self.pick,
                      self.words, self.alt, self.key, self.offers, self.again)


class Inbound:
    """One wake: the person's words or one generic reply."""
    __slots__ = ("person", "reply")

    def __init__(self, person="", reply=None):
        self.person, self.reply = person, reply


def floor_rank(inb):
    r = inb.reply
    if r is None:
        return 0
    if r.kind in ("surprise", "consult"):
        return 1
    if r.kind == "result":
        if r.status in ("refused", "failed"):
            return 1
        if r.status in ("done", "cancelled", "expired"):
            return 2
        return 4
    if r.kind in ("quiescence", "error"):
        return 2
    if r.kind == "notice":
        return 3
    return 4


def floor_order(inbs):
    return sorted(inbs, key=floor_rank)       # stable, as sort.SliceStable


def hashed(r):
    return r if (not r or r.startswith("#")) else "#" + r


def describe(c):
    if c.is_j():
        return identity(c)
    vals = [c.args[k] for k in sorted(c.args or {}) if isinstance(c.args[k], str)]
    return "your call " + c.name + "(" + ", ".join(vals) + ")"


def object_words(c):
    j = _jmap(c.j)
    for k in ("target", "goal"):
        m = j.get(k)
        if isinstance(m, dict):
            d = m.get("description")
            if isinstance(d, str) and d and d != "tag":
                return d
    if "tag_id" in j:
        return "tag %s" % goval(j["tag_id"])
    return identity(c).removeprefix("your command to ")


def info_query(c):
    if c.verb() != "QUERY":
        return False
    t = _jmap(c.j).get("target")
    return isinstance(t, dict) and t.get("type") == "PERSON"


def binds_object(text, c):
    j = _jmap(c.j)
    if not any(j.get(k) is not None for k in ("target", "goal", "tag_id", "place", "thing")):
        return True
    return names_words(text, object_words(c))


def own_voice(text, domain):
    low = " " + text.lower() + " "
    d = domain.lower()
    if " the " + d + " " in low or " the " + d + "'" in low:
        return False
    flat = low
    for ch in ".,;:":
        flat = flat.replace(ch, " ")
    return any(w in flat for w in (" i ", " i'm ", " i've ", " i'll ", " my ", " me "))


def negated(wds):
    ws = [w for w in re.split(r"[^a-z']+", wds.replace("’", "'").lower()) if w]
    for w in ws:
        if w in ("no", "not", "nope", "never", "don't", "can't", "cannot", "won't", "isn't", "wasn't", "couldn't",
                 "shouldn't", "nah", "neither", "nor") or w.endswith("n't"):
            return True
    return False


def answer_class(text):
    for c in ("yes", "no"):
        if '"%s"' % c in text:
            return c
    return "no"


def answer_class3(text):
    for c in ("yes", "no", "neither"):
        if '"%s"' % c in text:
            return c
    return "neither"


def status_words(s):
    return {"done": "is done", "expired": "expired: no outcome came in time", "cancelled": "was cancelled",
            "admitted": "was admitted", "applied": "was applied", "progress": "is under way"}.get(s, s)


def clock_line(verb):
    """TIME and DATE are the harness's, from the clock (integration step 4 ruling)."""
    t = time.localtime()
    if verb == "DATE":
        return "It's " + time.strftime("%A, %B ", t) + str(t.tm_mday) + "."
    h = t.tm_hour % 12 or 12
    return "It's %d:%02d %s." % (h, t.tm_min, "AM" if t.tm_hour < 12 else "PM")


class Mirror:
    """One line per loaded domain, the harness's from the replies."""

    def __init__(self):
        self.tasks, self.last, self.surprise = {}, {}, {}

    def see(self, r, c):
        d = r.domain
        if r.kind == "result":
            if r.status == "accepted":
                if c.is_j() and r.id:
                    self.tasks.setdefault(d, []).append((r.id, c))
            elif r.status in ("done", "refused", "failed", "expired", "cancelled"):
                ts = self.tasks.get(d, [])
                for i, (tid, _) in enumerate(ts):
                    if tid == r.id:
                        self.tasks[d] = ts[:i] + ts[i + 1:]
                        break
        elif r.kind in ("event", "quiescence"):
            if r.text:
                self.last[d] = r.text
        elif r.kind == "surprise":
            self.surprise[d] = hashed(r.record) + " open"
            if r.difference:
                self.surprise[d] += ": " + r.difference
        elif r.kind == "notice":
            self.surprise.pop(d, None)

    def live_in(self, d):
        return bool(self.tasks.get(d))

    def any_live(self):
        return any(self.tasks.values())

    def live(self, d, tid):
        for t in self.tasks.get(d, []):
            if t[0] != tid:
                return t, True
        return ("", Call()), False

    def render(self, loaded):
        parts = []
        for d in loaded:
            s = [describe(c).removeprefix("your command to ") + " (" + tid + ") in flight" for tid, c in self.tasks.get(d, [])]
            if self.surprise.get(d):
                s.append("surprise " + self.surprise[d])
            if self.last.get(d):
                s.append("last report: " + self.last[d].removesuffix("."))
            parts.append(d + ": " + "; ".join(s or ["idle"]) + ".")
        return "[State — not something the person said. " + " ".join(parts) + "]"


class Question:
    def __init__(self, qid, text, kind, reason, refused, refused_id, alts):
        self.id, self.text, self.kind, self.reason = qid, text, kind, reason
        self.alt, self.alts, self.refused, self.refused_id = "", list(alts), refused, refused_id
        self.live = ("", Call())

    def offered(self, c):
        t = canon_json(c.text())
        return any(a.is_j() and canon_json(a.text()) == t for a in self.alts)


def cue_bound(q):
    yes = "it lets that command go ahead"
    if q.kind == "cancelask":
        yes = "drop " + describe(q.live[1]).removeprefix("your command to ") + " for it"
    return ("[Judge the person's answer to " + q.id + ": yes (" + yes + ") or no. "
            "Exactly one of {\"answer\":\"yes\"}, {\"answer\":\"no\"}.]")


def cue_bound_j(q, read):
    return ("[Judge the person's words as an answer to " + q.id + ". yes: they say it is done or agree, so " +
            describe(q.refused) + " goes ahead now. neither: they ask for something else (" + describe(read) +
            "), which replaces it. no: they decline and ask for nothing else. "
            "Exactly one of {\"answer\":\"yes\"}, {\"answer\":\"neither\"}, {\"answer\":\"no\"}.]")


def cue_info(read):
    return ("[Judge the person's words: a question asking for information (info), or a request to act (command: " +
            describe(read) + "). Exactly one of {\"turn\":\"info\"}, {\"turn\":\"command\"}.]")


def cue_cancel_ask(refused, live):
    return ("[Next move — " + describe(refused) + " cannot start while " + describe(live[1]) + " (" + live[0] +
            ") is live, and the only way is to cancel that task: ask the person whether to drop it. Exactly one call.]")


class Orch:
    """The orchestrator's loop on a dialog whose system turn is decoded.

    l: the dialog's model side — inject(text) -> n, gen(gbnf, sampled,
    max_tokens, stop) -> (text, n_ctx_before, first_s, all_s),
    rollback(mark). dom: the domains — call(c) / intent(c) -> (Reply, ok)."""

    def __init__(self, cfg, l, dom):
        self.cfg, self.l, self.dom = cfg, l, dom
        self.api = repl_api(cfg.catalog, cfg.loaded)
        self.pend = []
        self.open = False
        self.by_id, self.said, self.consults = {}, None, []
        self.answers, self.refusals, self.surprises, self.deferred = {}, {}, {}, {}
        self.n_past = 0
        self.m = Mirror()
        self.q, self.qseq = None, 0
        self.quiet, self.spoke, self.asked, self.q_heard = {}, {}, {}, {}
        self.u_heard = {}       # the domain's unsettled beat has spoken (since its activation)
        self.br = Branch()
        self.last_call, self.last_name, self.last_key, self.last_cano = Call(), "", "", ""

    # ── the dialog's past and its wakes ──

    def w(self, s):
        self.pend.append(s)

    def past(self, person, intent, iid, spoken):
        self.close_turn()
        c = Call(j=intent)
        self.by_id[iid] = c
        self.w(user_turn(person) + MODEL_TURN + c.text() + "\n" + SPEAK_CUE + " " + spoken + TURN_END)
        self.flush()

    def person(self, text):
        self.close_turn()
        self.w(user_turn(self.head() + text) + MODEL_TURN)
        self.open = True

    def context(self, text):
        self.person(CHANGE_TAG + text)

    def wake(self, r):
        self.surprises[r.record] = r
        self.br = Branch("surprise", r.domain, r.record, reply=r)
        self.person("Close SURPRISE " + hashed(r.record) + " in " + r.domain + " with exactly one call.\n" + r.text)

    def said_call(self, c):
        self.w(c.text())
        if not c.is_j():
            self.said = c
            if c.name == "consult":
                self.consults.append(c)

    def spoke_line(self, line):
        self.w(line)

    def end(self):
        self.w(TURN_END)
        self.open = False
        self.flush()

    def close_turn(self):
        if self.open:
            self.w(TURN_END)
            self.open = False

    def flush(self):
        if not self.pend:
            return
        text = "".join(self.pend)
        self.pend = []
        if text:
            self.n_past = self.l.inject(text)

    def tokens(self):
        return self.n_past

    def say(self, kind, line):
        if self.cfg.on_say and line.strip():
            self.cfg.on_say(kind, line)

    def head(self):
        return self.m.render(self.cfg.loaded) + "\n" if self.cfg.mirror else ""

    def _ongen(self, gi):
        if self.cfg.on_gen:
            self.cfg.on_gen(gi)

    # ── the person's turn ──

    def turn(self, person, gbnf):
        b = Branch("person")
        wds = person.strip()
        q = self.q
        said_no = False
        if q is not None:     # the open question: this turn answers it, bound by id
            self.q, b.name = None, "bound"
            person = "[Reply to your question %s — %s — about %s (%s), refused: %s.] %s" % (
                q.id, json.dumps(q.text, ensure_ascii=False), describe(q.refused), q.refused_id, q.reason, person)
        self.person(person)
        self.last_call = Call()
        self.gen("move", 0, gbnf, False, b)
        c = self.last_call
        verb = c.verb()
        talk = c.is_j() and (verb in ("CHITCHAT", "UNKNOWN") or info_query(c))
        clock = ""
        if c.is_j() and verb in ("TIME", "DATE") and self.cfg.mirror:
            clock, talk = clock_line(verb), True
            if q is not None:
                self.q, q = q, None
        if c.is_j() and not talk and not self.is_ask(c) and verb not in ("STOP", "CANCEL") and wds.endswith("?"):
            self.w("\n" + cue_info(c) + "\n")
            cls, _ = self.gen_capture("answer", GRAMMAR_INFO, b)
            if '"info"' in cls:
                c = Call(j='{"intent":"CHITCHAT"}')
                verb, talk = "CHITCHAT", True
                self.w("\n" + c.text())
                self._ongen(GenInfo(label="move", text=c.text(), harness=True, branch=b.name))

        st = {"c": c, "verb": verb, "talk": talk}

        def retry():
            if q.kind == "cancelask":
                self.cancel_first(q)
            st["c"], st["verb"], st["talk"] = q.refused, q.refused.verb(), False

        if q is not None:
            c, verb, talk = st["c"], st["verb"], st["talk"]
            if canon_json(c.text()) == canon_json(q.refused.text()):
                retry()
            elif q.kind != "cancelask" and verb in ("STOP", "CANCEL") and not self.m.any_live():
                st["c"], st["verb"], st["talk"], said_no = Call(), "", True, True
                self._ongen(GenInfo(label="answer", text='{"answer":"no"}', harness=True, branch="bound"))
            elif q.kind == "cancelask" and verb == q.alt:
                retry()
                self.w("\n" + st["c"].text())
                self._ongen(GenInfo(label="move", text=st["c"].text(), harness=True, branch="bound"))
            elif c.is_j() and verb != "ask" and not q.offered(c):
                self.w("\n" + cue_bound_j(q, c) + "\n")
                cls, _ = self.gen_capture("answer", GRAMMAR_ANSWER3, b)
                k = answer_class3(cls)
                if k == "yes":
                    retry()
                    self.w("\n" + st["c"].text())
                    self._ongen(GenInfo(label="move", text=st["c"].text(), harness=True, branch="bound"))
                elif k == "no" and negated(wds):
                    st["c"], st["verb"], st["talk"], said_no = Call(), "", True, True
            elif talk:
                self.w("\n" + cue_bound(q) + "\n")
                cls, _ = self.gen_capture("answer", GRAMMAR_ANSWER, b)
                if answer_class(cls) != "yes":
                    said_no = True
                else:
                    retry()
                    self.w("\n" + st["c"].text())
                    self._ongen(GenInfo(label="move", text=st["c"].text(), harness=True, branch="bound"))
        c, verb, talk = st["c"], st["verb"], st["talk"]
        intent = c.text()
        voice = ""
        if (said_no or clock) and self.cfg.mirror:
            voice = clock or LINE_LEAVE_IT
            self.w("\n" + SPEAK_CUE + " " + voice)
            self.say("template", voice)
        elif not self.cfg.mirror or talk:
            if self.cfg.mirror:
                self.w("\n" + self.m.render(self.cfg.loaded))
            self.w("\n" + SPEAK_CUE)
            voice = self.speak_line(b)
        if c.is_j() and verb != "ask" and not talk:
            r, ok = self.dom.intent(c)
            if ok and r.id:
                self.by_id[r.id] = c
                self.asked[r.domain] = True
                self.m.see(r, c)
        self.end()
        return intent, voice

    def gen_capture(self, label, g, b):
        out = {}
        hook = self.cfg.on_gen

        def cap(gi):
            out["text"], out["mark"] = gi.text, gi.n_ctx
            if hook:
                hook(gi)
        self.cfg.on_gen = cap
        try:
            self.gen(label, 0, g, False, b)
        finally:
            self.cfg.on_gen = hook
        return out.get("text", ""), out.get("mark", 0)

    def speak_as_is(self, line):
        """The rule's %% line, spoken as it is (manifest voice: line): no model pass, so a result
        keeps its meaning ("applying it is your click" is never "I have completed applying")."""
        line = line[:1].upper() + line[1:]     # a line that starts with its {W} ("editor is not on the page now.")
        self.w(SPEAK_CUE + " " + line)
        self.say("line", line)
        self.end()

    def speak(self):
        self.w(SPEAK_CUE)
        line = self.speak_line(self.br)
        self.say("voice", line)
        self.end()

    def speak_line(self, b):
        line, mark = self.gen_text("voice", b)
        if not self.cfg.rollback or not unspeakable_line(line):
            return line
        log.info("[v3] forged line %r rolled back to %d", line, mark)
        self.l.rollback(mark)
        self.n_past = mark
        self.w(" Okay.")
        self._ongen(GenInfo(label="rollback", text=line, n_ctx=mark, harness=True, branch=b.name))
        self.flush()
        return "Okay."

    def gen_text(self, label, b):
        out = {}
        hook = self.cfg.on_gen

        def cap(gi):
            out["text"], out["mark"] = gi.text, gi.n_ctx
            if hook:
                hook(gi)
        self.cfg.on_gen = cap
        try:
            self.gen(label, 0, "", True, b)
        finally:
            self.cfg.on_gen = hook
        return out.get("text", ""), out.get("mark", 0)

    # ── a reply in ──

    def reply(self, r):
        if self.said is not None:
            c, self.said = self.said, None
        else:
            c = self.by_id.get(r.id, Call())
        if r.id and r.kind == "result":
            self.by_id[r.id] = c
        if not c.is_j() and r.kind == "result" and r.status == "accepted":
            log.info("[v3] %s(%s) answered accepted: a REPL reply is final (DESIGN v3), never accepted", c.name, r.domain)
        self.m.see(r, c)
        b = self.branch_of(r, c)
        self.br = b
        note = self.cue(b)
        if c.is_j():
            self.close_turn()
            if r.status in ("refused", "failed"):
                verb = "was refused" if r.status == "refused" else "failed"
                s = RESP_TAG + "%s (%s) %s: %s." % (cap_first(identity(c)), r.id, verb, r.reason)
                if b.alts:
                    s += " Alternatives: " + ", ".join(a.text() for a in b.alts) + "."
                if b.name == "again":
                    s += " The same refusal as before."
                self.w(user_turn(self.head() + s + "\n" + note))
            elif r.status == "accepted":
                self.w(user_turn(self.head() + RESP_TAG + "%s (%s) was accepted: handed over, not done yet." % (
                    cap_first(identity(c)), r.id) + TELL_NOW))
            else:
                said = ""
                if r.text and self.cfg.mirror and binds_object(r.text, c) and own_voice(r.text, r.domain):
                    said = " The " + r.domain + " says: " + r.text
                self.w(user_turn(self.head() + RESP_TAG + "%s (%s) %s." % (cap_first(identity(c)), r.id, status_words(r.status)) +
                                 said + " Tell the person in one short first-person sentence what happened."))
            self.w(MODEL_TURN)
            self.open = True
            return b.name
        self.w(tool_response(c.name, r, note))
        return b.name

    def branch_of(self, r, c):
        if r.kind == "consult":
            b = self.br.copy()
            b.name, b.reply, b.alts, b.pick, b.words = "answer", r, [], "", ""
            if r.resource is not None:
                b.words, b.key = r.resource["words"], r.resource["key"]
            self.answers[r.domain] = r
            if self.cfg.escalate_template and r.answer_kind == "unexplained":
                b.pick = "escalate"
            return b
        if r.kind == "result" and r.status in ("refused", "failed"):
            b = Branch(domain=r.domain, refused=c, reply=r, record=c.arg("record"))
            if not b.record:
                b.record = r.id
            if r.reason == "surprise_open":
                # the domain's surprise is open (e2e step 4 ruling): the harness's
                # refusal is relayed as it is, every time — never "again", never
                # an escalation or a consult; a close reopens orders
                b.name = "speak"
                return b
            k = (r.domain, c.verb(), r.reason)
            self.refusals[k] = self.refusals.get(k, 0) + 1
            if self.refusals[k] > 1:
                b.name, b.again = "again", k
                if self.cfg.again_pick:
                    b.pick = "escalate"
                    a = self.answers.get(r.domain)
                    if a is not None and a.answer_kind == "missing" and self.refusals[k] == 2 and a.resource is not None:
                        b.pick, b.words, b.key = "ask", a.resource["words"], a.resource["key"]
                return b
            for raw in r.alternatives:
                if isinstance(raw, dict) and isinstance(raw.get("verb"), str) and raw.get("verb"):
                    wv = raw["verb"].lower()
                else:
                    wv = raw if isinstance(raw, str) else ""
                if wv in ("cancel", "stop"):
                    live, ok = self.m.live(r.domain, r.id)
                    if ok:
                        b.name, b.words, b.alt = "cancelask", object_words(live[1]), wv.upper()
                        return b
                try:
                    a = alt_call(raw, self.api)
                except ValueError as e:
                    log.info("[v3] alternative %s: %s — the reason is spoken", jdump(raw), e)
                    b.name, b.alts = "speak", []
                    return b
                if len(self.cfg.intent_domains) > 1:   # several intent domains: the offer is the replying domain's
                    a = tag_call(a, r.domain)
                b.alts.append(a)
            bl = r.blocker_text()
            if not b.alts and bl == "null":
                b.name = "speak"
                return b
            if bl and bl != "null" and b.alts:
                b.name, b.offers, b.alts = "none", b.alts, []
                return b
            b.name = "alts" if b.alts else "none"
            return b
        if r.kind == "surprise":
            self.surprises[r.record] = r
            return Branch("surprise", r.domain, r.record, reply=r)
        return Branch(domain=r.domain, reply=r)

    def cue(self, b):
        if b.pick == "escalate":
            return ""
        if b.name == "again" and b.pick == "ask":
            return cue_ask_again(b.words, self.cfg.mirror)
        if b.name == "answer" and b.reply.answer_kind == "missing" and self.cfg.ask_check and b.offers:
            return cue_ask_offer(b.words, b.offers)
        if b.name == "answer" and b.reply.answer_kind == "missing" and self.cfg.ask_check:
            return cue_ask(b.words)
        if b.name == "cancelask":
            live, _ = self.m.live(b.domain, b.reply.id)
            return cue_cancel_ask(b.refused, live)
        return STATE_CUE.get(b.name, "")

    # ── the next move ──

    def move(self):
        b = self.br
        if b.name == "surprise" and self.cfg.mirror:
            return self.harness_consult(b)
        if b.pick == "escalate":
            self.harness_escalate(b)
            return self.answer(b)
        g = self.grammar(b)
        self.gen("move", 0, g, self.cfg.sample_moves, b)
        if b.refused.is_j():
            bad = canon_json(b.refused.text())
            k = 1
            while k <= 2 and self.last_cano == bad:
                self.w(TURN_END + user_turn(NOTE_REPEAT_J) + MODEL_TURN)
                self.gen("move", k, g, self.cfg.sample_moves, b)
                k += 1
        keys = []
        if b.refused.name and not b.refused.is_j():
            keys.append(call_of(b.refused.text())[1])
        for c in self.consults:
            keys.append(call_of(c.text())[1])
        k = 1
        while k <= 2 and self.last_key in keys:
            self.w("<|tool_response>response:" + self.last_name + "{note:" + Q3 + NOTE_REPEAT_T + Q3 + ",status:" +
                   Q3 + "refused" + Q3 + "}<tool_response|>")
            self.gen("move", k, g, self.cfg.sample_moves, b)
            k += 1
        if self.cfg.ask_check and b.words:
            names, want = names_words, b.words
            if b.key:
                want = b.key
            elif self.cfg.mirror:
                names = names_words_any
            k = 1
            while k <= 2 and self.last_call.verb() == "ask" and not names(self.last_call.arg("text"), want):
                self.w(TURN_END + user_turn(note_ask_words(b.words)) + MODEL_TURN)
                self.gen("move", k, g, self.cfg.sample_moves, b)
                k += 1
        self.answer(b)

    def grammar(self, b):
        if b.name == "alts":
            return grammar_alts(b.alts)
        if b.name in ("none", "surprise"):
            return grammar_consult(b.domain)
        if b.name == "cancelask":
            return grammar_ask()
        if b.name == "again":
            return grammar_ask() if b.pick == "ask" else grammar_again(b.domain, b.record)
        if b.name == "answer":
            k = b.reply.answer_kind
            if k == "explains":
                return grammar_explains(b.domain, b.record)
            if k == "noise":
                return grammar_noise(b.domain, b.record)
            if k == "unexplained":
                return grammar_unexplained(b.domain, b.record)
            if k == "missing":
                return grammar_ask() if self.cfg.ask_check else grammar_ask_literal(b.words)
            raise ValueError("v3: no move for answer_kind %r" % k)
        raise ValueError("v3: no move in branch %r" % b.name)

    def harness_consult(self, b):
        c = self.surprise_consult(b.domain, b.record)
        text = c.text()
        self.w(text)
        self.set_last(text)
        self._ongen(GenInfo(label="move", text=text, key=call_of(text)[1], harness=True, branch=b.name))
        self.answer(b)

    def surprise_consult(self, domain, record):
        q = "why does the reading differ from the model?"
        s = self.surprises.get(record)
        if s is not None and s.difference:
            q = "why does the reading differ from the model: " + s.difference + "?"
        return Call("consult", {"domain": domain, "question": q})

    def consult_answered(self, d, r):
        surprise, call = d
        self.wake(surprise)
        text = call.text()
        self.w(text)
        self.set_last(text)
        self._ongen(GenInfo(label="move", text=text, key=call_of(text)[1], harness=True, branch="surprise"))
        self.consults.append(call)
        self.said = call
        self.reply(r)
        self.move()

    def drop_deferred(self, domain):
        self.deferred.pop(domain, None)

    def harness_escalate(self, b):
        parts = []
        a = self.answers.get(b.domain) or Reply()
        f = a.text.strip().removesuffix(".")
        if f:
            parts.append(f)
        if b.name == "again":
            if a.answer_kind == "missing" and a.resource is not None:
                parts.append("asked once for " + a.resource["words"] + ", still missing")
            elif a.answer_kind == "explains" and a.cause is not None:
                parts.append("explained as " + a.cause["card"] + "/" + a.cause["move"] + ", happened again")
            elif a.answer_kind == "noise":
                parts.append("set aside as noise, happened again")
            elif a.answer_kind == "unexplained":
                parts.append("escalated once, happened again")
        s = self.surprises.get(b.record)
        if s is not None and s.difference:
            parts.append(s.difference)
        c = Call("escalate", {"domain": b.domain, "record": b.record, "text": "; ".join(parts)})
        text = c.text()
        self.w(text)
        self.set_last(text)
        self._ongen(GenInfo(label="move", text=text, key=call_of(text)[1], harness=True, branch=b.name))

    def answer(self, b):
        c = self.last_call
        if c.is_j() and c.verb() == "ask":
            self.ask(b, c)
        elif c.is_j():
            r, ok = self.dom.intent(c)
            if ok:
                self.close_turn()
                self.w(user_turn(RESP_TAG + "%s (%s) was accepted: handed over, not done yet." % (cap_first(identity(c)), r.id) +
                                 TELL_NOW) + MODEL_TURN + SPEAK_CUE)
                self.open = True
                self.by_id[r.id] = c
                self.m.see(r, c)
                line = self.speak_line(b)
                self.say("voice", line)
        elif c.name:
            r, ok = self.dom.call(c)
            if ok:
                if r.kind == "consult":
                    self.consults.append(c)
                    self.said = c
                    self.reply(r)
                    return self.move()
                if r.status == "accepted":
                    log.info("[v3] %s(%s) answered accepted: a REPL reply is final (DESIGN v3), never accepted", c.name, r.domain)
                self.w(tool_response(c.name, r, ""))
                line = self.template(b, c)
                if line:
                    self.w(line)
                    self.flush()
                    self.say("template", line)
                else:
                    self.w(SPEAK_CUE)
                    said = self.speak_line(b)
                    self.say("voice", said)
        self.end()

    def template(self, b, c):
        if b.name not in ("answer", "again"):
            return ""
        if c.name == "explain":
            return line_explain(self.cfg.display, b.domain, c.arg("card"), c.arg("move"))
        if c.name == "reset":
            return LINE_RESET
        if c.name == "escalate":
            return LINE_ESCALATE
        return ""

    def gen(self, label, try_, g, sampled, b):
        self.flush()
        mx, stop = (60 if sampled else 120), ("\n" if label == "voice" else "")
        self.open = True
        text, nctx, first, all_ = self.l.gen(g, sampled, mx, stop)
        info = GenInfo(label=label, try_=try_, text=text, n_ctx=nctx, first=first, all=all_, branch=b.name)
        name, key = call_of(text)
        if name:
            info.key = key
            self.set_last(text)
        self._ongen(info)

    def set_last(self, text):
        name, key = call_of(text)
        if not name:
            return
        self.last_name, self.last_key, self.last_cano = name, key, canon_json(text)
        c = parse_call(text)
        self.last_call = c if c is not None else Call()

    # ── orch3: the question, the inbox ──

    def ask(self, b, c):
        text = c.arg("text")
        self.qseq += 1
        q = Question("q%d" % self.qseq, text, b.name, b.reply.reason, b.refused, b.reply.id, b.alts + b.offers)
        if b.name == "cancelask":
            q.live, _ = self.m.live(b.domain, b.reply.id)
            q.alt = b.alt
        if self.cfg.mirror:
            self.q = q
        if b.name == "again" and b.pick == "ask" and self.cfg.mirror:
            self.say("template", line_still(b.words))
        self.say("ask", text)

    def handle(self, inb, gbnf, stop_words):
        """One wake under the floor's rules; True when the model generated."""
        if inb.reply is None:
            if keyword_stop(inb.person, stop_words):
                self.keyword(inb.person, '{"intent":"STOP"}')
                return False
            self.turn(inb.person, gbnf)
            return True
        r = inb.reply
        if r.kind == "result":
            if self.quiet.get(r.id):
                self.m.see(r, Call())
                return False
            if r.status in ("refused", "failed", "done", "cancelled", "expired"):
                pass
            elif r.status == "progress" and self.cfg.mirror and not self.spoke.get(r.id):
                pass
            else:
                self.m.see(r, self.by_id.get(r.id, Call()))
                return False
            self.spoke[r.id] = True
            if r.id not in self.by_id:
                log.info("[v3] %s %s for an id this dialog did not make: mirror only", r.id, r.status)
                self.m.see(r, Call())
                return False
            br = self.reply(r)
            if br in ("alts", "none", "answer", "again", "cancelask"):
                self.move()
            elif r.domain in self.cfg.line_voice and r.text:
                self.speak_as_is(r.text)
            else:
                self.speak()
            return True
        if r.kind == "surprise":
            self.m.see(r, Call())
            if self.cfg.defer is not None and self.cfg.mirror:
                self.surprises[r.record] = r
                c = self.surprise_consult(r.domain, r.record)
                self.deferred[r.domain] = (r, c)
                self.cfg.defer(c)
                return False
            self.wake(r)
            self.move()
            return True
        if r.kind == "consult":
            d = self.deferred.pop(r.domain, None)
            if d is None:
                self.m.see(r, Call())
                return False
            self.consult_answered(d, r)
            return True
        if r.kind in ("quiescence", "error"):
            self.m.see(r, Call())
            if r.kind == "quiescence" and not self.quiescence_spoken(r):
                return False
            self.close_turn()
            what = r.text or r.error
            self.w(user_turn(self.head() + RESP_TAG + "The " + r.domain + " reports: " + what +
                             " Tell the person in one short sentence what the " + r.domain + " reported.") + MODEL_TURN)
            self.open = True
            self.br = Branch(domain=r.domain, reply=r)
            self.speak()
            return True
        self.m.see(r, Call())
        return False

    def quiescence_spoken(self, r):
        d = r.domain
        if r.unsettled is not _ABSENT:     # a beat stopped at its step cap: spoken once until (re)activation
            if self.u_heard.get(d):
                return False
            self.u_heard[d] = True
            return True
        if r.over or "day is over" in r.text.lower():
            pass
        elif not self.q_heard.get(d):
            pass
        elif self.asked.get(d) and not self.m.live_in(d):
            pass
        else:
            return False
        self.q_heard[d], self.asked[d] = True, False
        return True

    def activated(self, d):
        self.q_heard.pop(d, None)
        self.u_heard.pop(d, None)

    def is_ask(self, c):
        """The J asks its domain something (Go isAsk): a verb its domain lists
        under asks:; with no intent material configured, the dog's QUERY."""
        v = c.verb()
        if self.cfg.asks is None:
            return v == "QUERY"
        d = j_domain(c)
        if not d and self.cfg.intent_domains:
            d = self.cfg.intent_domains[0]
        return v in self.cfg.asks.get(d, [])

    def restart_dialog(self, n, cfg):
        """The dialog begun again at a new system turn of n tokens (Go's
        restartDialog: a hot-install's card and enum): the harness's state
        stands — the mirror, the journal by id, the question."""
        self.pend = []
        self.open, self.said, self.n_past = False, None, n
        self.cfg.catalog, self.cfg.loaded, self.cfg.display = list(cfg.catalog), list(cfg.loaded), dict(cfg.display)
        self.cfg.intent_domains = list(cfg.intent_domains)
        self.cfg.asks = None if cfg.asks is None else dict(cfg.asks)
        self.api = repl_api(cfg.catalog, cfg.loaded)

    def reloaded(self, d):
        """reload(domain) took (e2e-workflow step 3, Go v3reload.go): the new
        runtime does not know the old tasks; the mirror says reloaded, the
        open surprise is gone, the next quiescence is the first."""
        self.m.tasks[d] = []
        self.m.last[d] = "reloaded"
        self.m.surprise.pop(d, None)
        self.activated(d)

    def keyword(self, person, j):
        self.q = None
        _, voice = self.stop(person, head=True)
        if voice == LINE_NOTHING_RUNNING:
            self.say("template", voice)

    def stop(self, person, head=False):
        """The stop words' STOP (the architect's ruling, e2e step 4; Go
        Orch.Stop 2db8296): sent to the domain whose task is in flight, to
        every one if several (in the loaded order), never to a domain by
        position; with none in flight nothing is sent and the line says so.
        A J to a domain other than the one intent domain names it. The turn
        is kept as the dialog's past — head: the mirror before the person's
        words, as it stands after the STOP went out (the keyword path's; Go
        4a359f6). Returns (the J, the line)."""
        def said():
            return self.head() + person if head else person
        base = Call(j='{"intent":"STOP"}')
        targets = [d for d in self.cfg.loaded if self.m.live_in(d)]
        if not targets:
            self.past(said(), base.j, "", LINE_NOTHING_RUNNING)
            return base.text(), LINE_NOTHING_RUNNING
        ids = self.cfg.intent_domains
        iid, j = "", base.text()
        for d in targets:
            c = base
            if len(ids) > 1 or (len(ids) == 1 and d != ids[0]):
                c = tag_call(base, d)
            r, ok = self.dom.intent(c)
            if not ok:
                continue
            self.by_id[r.id] = c
            self.quiet[r.id] = True             # its ack is the turn's line; its outcome the journal's
            self.m.see(r, c)
            if not iid:
                iid, j = r.id, c.text()
        if not iid:
            return j, ""
        self.past(said(), j, iid, ACK_TEMPLATE["STOP"])
        return j, ACK_TEMPLATE["STOP"]

    def cancel_first(self, q):
        c = Call(j='{"intent":"' + q.alt + '"}')
        r, ok = self.dom.intent(c)
        if ok:
            self.quiet[r.id] = True
            self.m.see(r, c)
            self._ongen(GenInfo(label="move", text=c.text(), harness=True, branch="bound"))

    def open_question(self):
        if self.q is None:
            return "", "", False
        return self.q.id, canon_json(self.q.refused.text()), True
