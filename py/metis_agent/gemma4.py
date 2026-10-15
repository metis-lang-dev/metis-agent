"""Gemma 4 native tool calls: the declarations and the call grammar.

Ported from go/tools/gemma_tools.py (E4/E9; was e9-tools.py), which ports the GGUF chat
template's macros (format_function_declaration, format_argument,
format_tool_response_block). Copied, not imported: go/ and py/ share no
code (SPECIALIST.md §2).

    system turn:  {system}<|tool>declaration:NAME{…}<tool|>…<turn|>
    a call:       <|tool_call>call:NAME{key:value,…}<tool_call|>
    its result:   <|tool_response>response:NAME{key:value,…}<tool_response|>

The result follows the call inside the same model turn; the model then
calls again or answers.
"""
import json
import re

Q = '<|"|>'
CALL_OPEN, CALL_CLOSE = "<|tool_call>", "<tool_call|>"
RESP_OPEN, RESP_CLOSE = "<|tool_response>", "<tool_response|>"


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


def system_turn(system, tools):
    """The system turn's content as the template renders it: the text
    trimmed, then the declarations, no separator."""
    return system.strip() + "".join(declaration(t) for t in tools)


def call_text(name, args):
    return CALL_OPEN + "call:" + name + "{" + ",".join(k + ":" + fmt_arg(args[k], False) for k in sorted(args)) + "}" + CALL_CLOSE


def response_text(name, response):
    """format_tool_response_block: a mapping as keys in dictsort order."""
    return RESP_OPEN + "response:" + name + "{" + ",".join(
        k + ":" + fmt_arg(response[k], False) for k in sorted(response)) + "}" + RESP_CLOSE


CALL = re.compile(r"call:([A-Za-z_][A-Za-z0-9_]*)\{(.*)\}\s*(?:<tool_call\|>)?\s*$", re.S)
KV = re.compile(r'([A-Za-z_][A-Za-z0-9_]*):(?:<\|"\|>(.*?)<\|"\|>|(-?[0-9.]+|true|false|null))')


def parse_call(text):
    """'call:NAME{key:<|"|>v<|"|>,…}' (with or without the markers) →
    (name, {key: value}); string and scalar values only, which is all the
    REPL tools declare. None when it does not parse."""
    t = text.strip()
    if t.startswith(CALL_OPEN):
        t = t[len(CALL_OPEN):]
    m = CALL.match(t)
    if not m:
        return None
    args = {}
    for k, s, v in KV.findall(m.group(2)):
        args[k] = s if v == "" else json.loads(v)
    return m.group(1), args


class Gbnf:
    """The call grammar over the declarations (E9's e9-tools.py Gbnf, the
    parts the REPL tools use: string / enum / number parameters)."""

    def __init__(self):
        self.rules = {}

    @staticmethod
    def lit(s):
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
        raise ValueError(t)

    def body(self, props, required, name):
        """Keys in dictsort order, the required ones present, a comma
        between any two present ones — the template's mapping."""
        keys = [(k, self.value(props[k], name + "-" + k.replace("_", "-")), k in required) for k in sorted(props)]
        kv = lambda k, r: '%s %s' % (self.lit(k + ":"), r)

        def tail(i):
            return " ".join('"," %s' % kv(k, r) if req else '("," %s)?' % kv(k, r) for k, r, req in keys[i:])

        alts = []
        for j, (k, r, req) in enumerate(keys):
            alts.append(("%s %s" % (kv(k, r), tail(j + 1))).strip())
            if req:
                break
        else:
            alts.append("")
        alts = [a if a else '""' for a in alts]
        return "(" + " | ".join(alts) + ")" if len(alts) > 1 else alts[0]

    def call_grammar(self, tools, with_open=False):
        """One native call. with_open=False starts after the <|tool_call>
        token (the loop samples that token freely, then pins the rest)."""
        calls = []
        for t in tools:
            p = t.get("parameters")
            nm = "call-" + t["name"].lower().replace("_", "-")
            body = self.body(p["properties"], p.get("required", []), "a-" + nm[5:]) if p else '""'
            calls.append(self.rule(nm, '%s "{" %s "}"' % (self.lit(t["name"]), body)))
        head = ['root ::= %s"call:" call %s' % (self.lit(CALL_OPEN) + " " if with_open else "", self.lit(CALL_CLOSE)),
                "call ::= " + " | ".join(calls)]
        prims = ['str ::= %s [^<\\n]* %s' % (self.lit(Q), self.lit(Q)),
                 'int ::= "-"? [0-9]+',
                 'num ::= "-"? [0-9]+ ("." [0-9]+)?']
        return "\n".join(head + ["%s ::= %s" % (k, v) for k, v in self.rules.items()] + prims) + "\n"
