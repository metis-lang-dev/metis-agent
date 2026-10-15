#!/usr/bin/env python3
"""The Gemma 4 chat template's tool macros, ported (format_argument, the
parameter block, the declaration): how a tool is declared to the model and
how an argument is written in a call or a response. The orchestrator's
REPL interface is T (tool calls) — E13's corpus renders its declarations
with these; the catalog carries none of it (catalog-entry s-65dd0863).
Moved out of tools/e9-tools.py when E9's dog-intent T bench was removed;
internal/agent's v3text.go/v3reply.go port the same macros to Go."""
import json

Q = '<|"|>'


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
