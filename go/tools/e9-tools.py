#!/usr/bin/env python3
"""E9 (MODEL-EXPERIMENTS.md): render the dog intents as Gemma 4 native tools.

From ../../metis-catalog/domains/dog/intent/bench/dog-tools.json (name = intent, parameters = the fields dog.gbnf
admits for it) this writes:

  ../../metis-catalog/domains/dog/intent/bench/dog-tools.gbnf         one native call, <|tool_call>call:NAME{…}<tool_call|>,
                                 arguments in the template's order (dictsort),
                                 enums / numbers / strings as declared
  ../../metis-catalog/domains/dog/intent/bench/dog-tools-prompt.txt   the T prompt: the dog persona without its JSON
                                 table, the few-shot examples rendered as calls,
                                 the pass-2 section, then the declarations
                                 (<|tool>declaration:…<tool|>, the GGUF template's
                                 format_function_declaration, ported below)
  ../../metis-catalog/domains/dog/intent/bench/dog-tools-prompt-noex.txt   the same without the intent examples (read 4)
  ../../metis-catalog/domains/dog/intent/bench/dog-tools-prompt-rt.txt     T with the pass-2 examples as round-trips: the
                                 call, <|tool_response> {status:accepted}, the line (T-rt)

The intent examples are converted from ../../metis-catalog/domains/dog/intent/dog-prompt.txt and
../../metis-catalog/domains/dog/intent/dog-spoken.txt by json_to_call, the inverse of
tools/e9-score.py's call_to_json; a check below round-trips every one.

    python3 tools/e9-tools.py            # from go/
"""
import json
import re
import sys
from pathlib import Path

GO = Path(__file__).resolve().parent.parent
Q = '<|"|>'


def load_tools(path=GO / "../../metis-catalog/domains/dog/intent/bench/dog-tools.json"):
    spec = json.loads(Path(path).read_text())
    defs = spec["$defs"]

    def expand(v):
        if isinstance(v, dict):
            if "$ref" in v:
                return expand(defs[v["$ref"]])
            return {k: expand(x) for k, x in v.items()}
        if isinstance(v, list):
            return [expand(x) for x in v]
        return v

    return [expand(t) for t in spec["tools"]]


# ── declarations: the GGUF template's macros, ported ─────────────────────────

def fmt_arg(a, escape_keys=True):
    if a is None:
        return "null"
    if isinstance(a, bool):
        return "true" if a else "false"
    if isinstance(a, str):
        return Q + a + Q
    if isinstance(a, dict):
        return "{" + ",".join((Q + k + Q if escape_keys else k) + ":" + fmt_arg(a[k], escape_keys)
                              for k in sorted(a)) + "}"
    if isinstance(a, list):
        return "[" + ",".join(fmt_arg(x, escape_keys) for x in a) + "]"
    return json.dumps(a)


def fmt_params(props, required):
    out = []
    for key in sorted(props):
        v = props[key]
        s, comma = key + ":{", False
        if v.get("description"):
            s += "description:" + Q + v["description"] + Q
            comma = True
        t = v["type"].upper()
        if t == "STRING" and v.get("enum"):
            s += ("," if comma else "") + "enum:" + fmt_arg(v["enum"])
            comma = True
        elif t == "ARRAY" and isinstance(v.get("items"), dict) and v["items"]:
            s += ("," if comma else "") + "items:{"
            comma = True
            parts = []
            for ik in sorted(v["items"]):
                iv = v["items"][ik]
                if ik == "properties":
                    parts.append("properties:{" + fmt_params(iv, v["items"].get("required", [])) + "}")
                elif ik == "required":
                    parts.append("required:[" + ",".join(Q + r + Q for r in iv) + "]")
                elif ik == "type":
                    parts.append("type:" + fmt_arg(iv.upper()))
                else:
                    parts.append(ik + ":" + fmt_arg(iv))
            s += ",".join(parts) + "}"
        if t == "OBJECT":
            if isinstance(v.get("properties"), dict):
                s += ("," if comma else "") + "properties:{" + fmt_params(v["properties"], v.get("required", [])) + "}"
                comma = True
            if v.get("required"):
                s += ("," if comma else "") + "required:[" + ",".join(Q + r + Q for r in v["required"]) + "]"
                comma = True
        s += ("," if comma else "") + "type:" + Q + t + Q + "}"
        out.append(s)
    return ",".join(out)


def declaration(tool):
    s = "declaration:" + tool["name"] + "{description:" + Q + tool["description"] + Q
    p = tool.get("parameters")
    if p:
        s += ",parameters:{"
        if p.get("properties"):
            s += "properties:{" + fmt_params(p["properties"], p.get("required", [])) + "},"
        if p.get("required"):
            s += "required:[" + ",".join(Q + r + Q for r in p["required"]) + "],"
        if p.get("type"):
            s += "type:" + Q + p["type"].upper() + Q + "}"
    return "<|tool>" + s + "}<tool|>"


# ── the call grammar ─────────────────────────────────────────────────────────

class Gbnf:
    def __init__(self):
        self.rules = {}

    def lit(self, s):
        return '"' + s.replace("\\", "\\\\").replace('"', '\\"') + '"'

    def rule(self, name, body):
        if self.rules.get(name, body) != body:
            raise ValueError("rule %s defined twice differently" % name)
        self.rules[name] = body
        return name

    def value(self, v, name):
        t = v["type"].upper()
        if t == "STRING" and v.get("enum"):
            return self.rule(name, " | ".join(self.lit(Q + e + Q) for e in v["enum"]))
        if t == "STRING":
            return "str"
        if t == "INTEGER":
            return "int"
        if t == "NUMBER":
            return "num"
        if t == "ARRAY":
            item = self.value(v["items"], name + "-item")
            return self.rule(name, '"[" %s ("," %s)* "]"' % (item, item))
        if t == "OBJECT":
            return self.rule(name, '"{" %s "}"' % self.body(v.get("properties", {}), v.get("required", []), name))
        raise ValueError(t)

    def body(self, props, required, name):
        """Keys in dictsort order, the required ones present, a comma between
        any two present ones — the template's rendering of a mapping."""
        keys = [(k, self.value(props[k], name + "-" + k.replace("_", "-")), k in required) for k in sorted(props)]
        kv = lambda k, r: '%s %s' % (self.lit(k + ":"), r)

        def tail(i):  # something was emitted before keys[i:]
            parts = []
            for k, r, req in keys[i:]:
                parts.append('"," %s' % kv(k, r) if req else '("," %s)?' % kv(k, r))
            return " ".join(parts)

        alts = []
        for j, (k, r, req) in enumerate(keys):  # keys[j] is the first present
            alts.append(("%s %s" % (kv(k, r), tail(j + 1))).strip())
            if req:
                break
        else:
            alts.append("")  # all optional, none present
        alts = [a if a else '""' for a in alts]
        return "(" + " | ".join(alts) + ")" if len(alts) > 1 else alts[0]

    def call_grammar(self, tools):
        calls = []
        for t in tools:
            p = t.get("parameters")
            nm = "call-" + t["name"].lower().replace("_", "-")
            body = self.body(p["properties"], p.get("required", []), "a-" + t["name"].lower().replace("_", "-")) if p else '""'
            calls.append(self.rule(nm, '%s "{" %s "}"' % (self.lit(t["name"]), body)))
        head = [
            "# E9: one Gemma 4 native call of one dog intent (generated by tools/e9-tools.py",
            "# from ../../metis-catalog/domains/dog/intent/bench/dog-tools.json; do not edit). Arguments in the GGUF template's",
            "# dictsort order; strings between <|\"|> tokens.",
            'root ::= "<|tool_call>call:" call "<tool_call|>"',
            "call ::= " + " | ".join(calls),
        ]
        prims = [
            'str ::= %s [^<\\n]* %s' % (self.lit(Q), self.lit(Q)),
            'int ::= "-"? [0-9]+',
            'num ::= "-"? [0-9]+ ("." [0-9]+)?',
        ]
        return "\n".join(head + ["%s ::= %s" % (k, v) for k, v in self.rules.items()] + prims) + "\n"


# ── prompts ──────────────────────────────────────────────────────────────────

def json_to_call(j):
    j = dict(j)
    name = j.pop("intent")
    return "<|tool_call>call:" + name + "{" + ",".join(k + ":" + fmt_arg(j[k], False) for k in sorted(j)) + "}<tool_call|>"


EX = re.compile(r'^(".*") → (\{.*\})$')

PERSONA = """You are Nina, a quadruped robot dog. The person is talking to YOU: you
answer as Nina, in the first person ("I", "me"); never greet or address
"Nina" yourself. Map what the person says to exactly one call of one of the
tools below per utterance. Respond with only the call.

You NEVER output coordinates, velocities, or device commands — only one of
the tool calls below. The robot's planner and perception do the rest.

Targets are open vocabulary: put the person's words in `description`
("the black office chair with wheels" stays as said); `attributes` carries
color/size/material when stated. Spatial phrases become `constraints`
(NEAR, NEXT_TO, IN_FRONT_OF, BEHIND, ON, UNDER, LEFT_OF, RIGHT_OF,
CLOSEST_TO) with a `reference` target. NAVIGATE uses `goal`; QUERY, FIND
and FOLLOW use `target`. Omit fields you don't need.
"""


def convert_examples(text, keep_examples, n):
    out = []
    for ln in text.splitlines():
        m = EX.match(ln)
        if m:
            n[0] += 1
            if keep_examples:
                out.append("%s → %s" % (m.group(1), json_to_call(json.loads(m.group(2)))))
            continue
        out.append(ln.replace("(utterance → JSON)", "(utterance → call)")
                     .replace("After the JSON comes", "After the call comes")
                     .replace("never JSON and never an intent name", "never a call and never an intent name"))
    return "\n".join(out)


RT_STATUS = "accepted"  # the dispatcher's acknowledgement: handed over, not done


def rt_response(call):
    """The result turn after a call (the template's format_tool_response_block)."""
    name = re.search(r"call:([A-Za-z_]+)\{", call).group(1)
    return "<|tool_response>response:%s{status:%s}<tool_response|>" % (name, fmt_arg(RT_STATUS))


def rt_pass2(p2):
    """The pass-2 section rendered as round-trips (E9 rerun, T-rt): the call,
    the result turn, then the spoken line in the same model turn."""
    lines, out = p2.splitlines(), []
    k = 0
    while k < len(lines):
        m = EX.match(lines[k])
        if m and k + 1 < len(lines) and lines[k + 1].startswith("Spoken response: "):
            call = json_to_call(json.loads(m.group(2)))
            out.append("%s → %s%s%s" % (m.group(1), call, rt_response(call), lines[k + 1][len("Spoken response: "):]))
            k += 2
            continue
        out.append(lines[k].replace("## Spoken response (after the JSON)", "## Spoken response (after the robot's answer)")
                   .replace('After the JSON comes "Spoken response:" and one short sentence',
                            "After the call the robot answers it; then comes one short sentence")
                   .replace("never JSON and never an intent name", "never a call and never an intent name"))
        k += 1
    return "\n".join(out)


def t_prompt(tools, keep_examples, rt=False):
    dog = (GO / "../../metis-catalog/domains/dog/intent/dog-prompt.txt").read_text()
    p2 = (GO / "../../metis-catalog/domains/dog/intent/dog-spoken.txt").read_text()
    ex = dog[dog.index("## Examples"):]
    n = [0]
    body = PERSONA + "\n" + convert_examples(ex, keep_examples, n).rstrip("\n") + "\n"
    # the pass-2 section keeps its three examples in both prompts: they teach
    # the spoken line, not the intent; T-rt's shows them as round-trips
    body += rt_pass2(p2) if rt else convert_examples(p2, True, [0])
    if not keep_examples:
        body = body.replace("## Examples (utterance → call)\n", "## Rules\n")
    return body.rstrip() + "".join(declaration(t) for t in tools), n[0]


def main():
    tools = load_tools()
    g = Gbnf().call_grammar(tools)
    (GO / "../../metis-catalog/domains/dog/intent/bench/dog-tools.gbnf").write_text(g)
    full, n = t_prompt(tools, True)
    noex, _ = t_prompt(tools, False)
    (GO / "../../metis-catalog/domains/dog/intent/bench/dog-tools-prompt.txt").write_text(full)
    (GO / "../../metis-catalog/domains/dog/intent/bench/dog-tools-prompt-noex.txt").write_text(noex)
    rt, _ = t_prompt(tools, True, rt=True)
    (GO / "../../metis-catalog/domains/dog/intent/bench/dog-tools-prompt-rt.txt").write_text(rt)
    # round trip: every example's call maps back to its JSON
    sys.path.insert(0, str(GO / "tools"))
    score = __import__("e9-score")
    bad = 0
    for src in ("../../metis-catalog/domains/dog/intent/dog-prompt.txt", "../../metis-catalog/domains/dog/intent/dog-spoken.txt"):
        for ln in (GO / src).read_text().splitlines():
            m = EX.match(ln)
            if m:
                j = json.loads(m.group(2))
                back, err = score.call_to_json(json_to_call(j))
                if err or json.loads(back) != j:
                    bad += 1
                    print("ROUND TRIP", ln, back, err, file=sys.stderr)
    print("tools %d, intent examples %d, gbnf %d rules, prompt %d / noex %d bytes, round-trip misses %d"
          % (len(tools), n, len(g.splitlines()), len(full), len(noex), bad))
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
