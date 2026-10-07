#!/usr/bin/env python3
"""Generate the purelymail command tree from Purelymail's OpenAPI spec.

The spec is Purelymail's (proprietary licence), so it is not committed: `make
spec` fetches it from the public docs into .spec/, and this script turns it into
internal/tree/schema.json, which the Go binary embeds. What is extracted is a
factual description of the API -- operation names, paths, body fields, their
types and the one-line descriptions -- not the spec document itself.

Every operation in the spec must be mapped in OPS below. An unmapped operation
fails the build rather than silently disappearing from the CLI, which is how a
new Purelymail endpoint gets noticed.

Usage: python3 tools/gen_command_tree.py --spec .spec/purelymail-openapi.json --out internal/tree/schema.json
"""

import argparse
import json
import re
import sys

DOCS_URL = "https://news.purelymail.com/api/index.html"
SPEC_URL = "https://news.purelymail.com/api/swagger-spec.js"

# Resources, in the order `purelymail list` shows them.
RESOURCES = [
    ("user", "Users on your account"),
    ("password-reset", "A user's password reset methods (email or phone)"),
    ("app-password", "App passwords for IMAP/SMTP clients"),
    ("routing", "Mail routing rules: aliases, forwards, catch-alls"),
    ("domain", "Domains: add, list, settings, ownership proof"),
    ("account", "Account billing and credit"),
]

# API operation -> (resource, friendly op name). Destructive ops are the
# deletes; the CLI refuses them without --yes.
OPS = {
    "createUser": ("user", "create"),
    "getUser": ("user", "get"),
    "listUser": ("user", "list"),
    "modifyUser": ("user", "modify"),
    "deleteUser": ("user", "delete"),
    "upsertPasswordReset": ("password-reset", "upsert"),
    "listPasswordReset": ("password-reset", "list"),
    "deletePasswordReset": ("password-reset", "delete"),
    "createAppPassword": ("app-password", "create"),
    "deleteAppPassword": ("app-password", "delete"),
    "createRoutingRule": ("routing", "create"),
    "listRoutingRules": ("routing", "list"),
    "deleteRoutingRule": ("routing", "delete"),
    "addDomain": ("domain", "add"),
    "listDomains": ("domain", "list"),
    "getOwnershipCode": ("domain", "ownership-code"),
    "updateDomainSettings": ("domain", "update"),
    "deleteDomain": ("domain", "delete"),
    "checkAccountCredit": ("account", "credit"),
}

# Hand-written commands that live alongside the generated ones, so discovery
# shows them in the same place. The Go side supplies the implementation.
CUSTOM = {
    "domain": [
        {
            "name": "dns",
            "operation": "getOwnershipCode",
            "method": "POST",
            "path": "/api/v0/getOwnershipCode",
            "summary": "Print the DNS records a domain needs for Purelymail",
            "description": (
                "Lists MX, SPF, DKIM (3 CNAMEs), DMARC and the ownership TXT record "
                "for a domain, ready to feed to a DNS provider. Calls getOwnershipCode "
                "for the ownership value; --skip-ownership works offline."
            ),
            "destructive": False,
            "custom": True,
            "positional": "domain",
            "params": [
                {
                    "name": "domain",
                    "flag": "domain",
                    "type": "string",
                    "kind": "string",
                    "required": True,
                    "description": "Domain the records are for, e.g. example.com",
                }
            ],
            "returns": [
                {"path": "[].type", "type": "string", "required": True, "description": "MX, TXT or CNAME"},
                {"path": "[].name", "type": "string", "required": True, "description": "Fully qualified record name"},
                {"path": "[].content", "type": "string", "required": True, "description": "Record value"},
                {"path": "[].priority", "type": "integer", "required": False, "description": "MX priority"},
                {"path": "[].purpose", "type": "string", "required": True, "description": "What the record is for"},
            ],
        }
    ]
}


def kebab(name):
    return re.sub(r"(?<=[a-z0-9])([A-Z])", r"-\1", name).lower()


def resolve(spec, node):
    while "$ref" in node:
        parts = node["$ref"].lstrip("#/").split("/")
        target = spec
        for p in parts:
            target = target[p]
        node = target
    return node


def type_of(spec, prop):
    """Return (display type, kind, item kind) for a schema property."""
    prop = resolve(spec, prop)
    t = prop.get("type", "object")
    if t == "boolean":
        return "boolean", "bool", None
    if t == "integer" or (t == "number" and prop.get("format") in ("int32", "int64")):
        return "integer", "int", None
    if t == "number":
        return "number", "float", None
    if t == "string":
        return "string", "string", None
    if t == "array":
        item_type, item_kind, _ = type_of(spec, prop.get("items", {}))
        if item_kind == "json":
            return "array<object>", "json", None
        return "array<%s>" % item_type, "list", item_kind
    return "object", "json", None


def params_for(spec, schema):
    schema = resolve(spec, schema)
    required = set(schema.get("required", []))
    out = []
    for name, prop in schema.get("properties", {}).items():
        display, kind, item = type_of(spec, prop)
        p = {
            "name": name,
            "flag": kebab(name),
            "type": display,
            "kind": kind,
            "required": name in required,
        }
        if item:
            p["item"] = item
        if "default" in prop:
            p["default"] = prop["default"]
        desc = prop.get("description") or resolve(spec, prop).get("description")
        if desc:
            p["description"] = desc
        out.append(p)
    return out


def flatten(spec, schema, prefix, out, seen=()):
    """Describe a response schema as dotted paths, e.g. domains[].dnsSummary.passesMx."""
    schema = resolve(spec, schema)
    required = set(schema.get("required", []))
    for name, prop in schema.get("properties", {}).items():
        path = prefix + name
        raw = prop
        prop = resolve(spec, prop)
        display, kind, _ = type_of(spec, prop)
        entry = {"path": path, "type": display, "required": name in required}
        desc = raw.get("description") or prop.get("description")
        if desc:
            entry["description"] = desc
        out.append(entry)
        ref = raw.get("$ref") or prop.get("items", {}).get("$ref")
        if ref in seen:
            continue
        if prop.get("type") == "array" and prop.get("items"):
            items = resolve(spec, prop["items"])
            if items.get("properties"):
                flatten(spec, items, path + "[].", out, seen + (ref,))
        elif prop.get("properties"):
            flatten(spec, prop, path + ".", out, seen + (ref,))


def single_required_scalar(params):
    req = [p for p in params if p["required"]]
    if len(req) == 1 and req[0]["kind"] in ("string", "int"):
        return req[0]["name"]
    return None


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--spec", required=True)
    ap.add_argument("--out", required=True)
    args = ap.parse_args()

    with open(args.spec) as f:
        spec = json.load(f)

    servers = spec.get("servers") or [{"url": "https://purelymail.com"}]
    scheme = next(iter(spec["components"]["securitySchemes"].values()))

    by_resource = {name: [] for name, _ in RESOURCES}
    unmapped = []
    for path, methods in spec["paths"].items():
        for method, op in methods.items():
            api_name = path.rsplit("/", 1)[-1]
            if api_name not in OPS:
                unmapped.append(api_name)
                continue
            resource, friendly = OPS[api_name]
            body = op["requestBody"]
            body = resolve(spec, body)["content"]["application/json"]["schema"]
            params = params_for(spec, body)

            returns = []
            ok = op["responses"].get("200", {}).get("content", {}).get("application/json", {}).get("schema")
            if ok:
                result = resolve(spec, ok).get("properties", {}).get("result")
                if result:
                    flatten(spec, result, "", returns)

            entry = {
                "name": friendly,
                "operation": api_name,
                "method": method.upper(),
                "path": path,
                "summary": op.get("summary", "").strip(),
                "description": op.get("description", "").strip(),
                "destructive": api_name.startswith("delete"),
                "params": params,
                "returns": returns,
            }
            pos = single_required_scalar(params)
            if pos:
                entry["positional"] = pos
            by_resource[resource].append(entry)

    if unmapped:
        sys.exit("unmapped operations (add them to OPS): " + ", ".join(sorted(unmapped)))
    missing = set(OPS) - {o["operation"] for ops in by_resource.values() for o in ops}
    if missing:
        sys.exit("operations no longer in the spec: " + ", ".join(sorted(missing)))

    resources = []
    for name, desc in RESOURCES:
        ops = by_resource[name] + CUSTOM.get(name, [])
        resources.append({"name": name, "description": desc, "ops": ops})

    tree = {
        "api": {
            "title": spec["info"]["title"],
            "version": spec["info"]["version"],
            "base_url": servers[0]["url"],
            "auth_header": scheme["name"],
            "docs_url": DOCS_URL,
            "spec_url": SPEC_URL,
        },
        "resources": resources,
    }
    with open(args.out, "w") as f:
        json.dump(tree, f, indent=1, ensure_ascii=False)
        f.write("\n")
    n = sum(len(r["ops"]) for r in resources)
    print("wrote %s: %d resources, %d operations" % (args.out, len(resources), n))


if __name__ == "__main__":
    main()
