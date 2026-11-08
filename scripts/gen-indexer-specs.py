#!/usr/bin/env python3
"""Converts Prowlarr/Indexers' v11 definitions into Solarr's spec file (internal/acquiring/indexer/spec/data/specs.json.gz).

Usage: scripts/gen-indexer-specs.py <checkout of github.com/Prowlarr/Indexers> <its commit>
Ordered maps become lists (field order and case order matter), every scalar becomes a string, header values lists.
"""
import glob, gzip, json, os, sys
import yaml


def text(value):
    if isinstance(value, bool):
        return "true" if value else "false"
    return "" if value is None else str(value)


def scalars(node):
    if isinstance(node, dict):
        return {key: scalars(value) for key, value in node.items()}
    if isinstance(node, list):
        return [scalars(value) for value in node]
    return text(node)


def filters(node):
    result = []
    for one in node or []:
        if isinstance(one, str):
            result.append({"name": one, "args": []})
            continue
        args = one.get("args", [])
        result.append({"name": one["name"], "args": [text(a) for a in (args if isinstance(args, list) else [args])]})
    return result


def date_headers(node):
    if not isinstance(node, dict):
        return None
    return {"selector": text(node.get("selector", "")), "filters": filters(node.get("filters"))}


def extractor(node):
    if not isinstance(node, dict):
        return {"text": text(node)}
    out = {}
    for key in ("selector", "attribute", "text", "default", "remove"):
        if key in node:
            out[key] = text(node[key])
    if node.get("optional"):
        out["optional"] = True
    if "filters" in node:
        out["filters"] = filters(node["filters"])
    if "case" in node:
        out["case"] = [{"selector": text(k), "value": text(v)} for k, v in node["case"].items()]
    return out


def errors(node):
    return [{"selector": text(e.get("selector")), "message": extractor(e["message"]) if e.get("message") else None} for e in node or []]


def setting(node):
    out = scalars({k: v for k, v in node.items() if k != "options"})
    if isinstance(node.get("options"), dict):
        out["options"] = [{"value": text(k), "label": text(v)} for k, v in node["options"].items()]
    return out


def infohash(node):
    if not node:
        return None
    return {"hash": extractor(node["hash"]) if "hash" in node else None, "title": extractor(node["title"]) if "title" in node else None,
            "usebeforeresponse": text(node.get("usebeforeresponse"))}


def headers(node):
    return {key: [text(v) for v in (value if isinstance(value, list) else [value])] for key, value in (node or {}).items()}


def convert(d):
    login = d.get("login")
    if login:
        login = scalars({k: v for k, v in login.items() if k not in ("headers", "test", "selectorinputs", "getselectorinputs", "error")})
        for key in ("selectorinputs", "getselectorinputs"):
            if d["login"].get(key):
                login[key] = {k: extractor(v) for k, v in d["login"][key].items()}
        login["error"] = errors(d["login"].get("error"))
        if d["login"].get("headers"):
            login["headers"] = headers(d["login"]["headers"])
        if d["login"].get("test"):
            login["test"] = scalars(d["login"]["test"])
    search = d.get("search") or {}
    out = {
        "id": d["id"], "name": d["name"], "description": text(d.get("description")), "language": text(d.get("language")),
        "privacy": text(d.get("type")), "encoding": text(d.get("encoding")) or "UTF-8", "links": d.get("links", []),
        "legacy_links": d.get("legacylinks", []), "request_delay": float(d.get("requestDelay", 0) or 0),
        "follow_redirect": bool(d.get("followredirect")),
        "settings": [setting(x) for x in d.get("settings", [])],
        "caps": {
            "categories": [scalars(c) for c in (d.get("caps", {}).get("categorymappings") or [])],
            "modes": {k: [text(x) for x in v] for k, v in (d.get("caps", {}).get("modes") or {}).items()},
        },
        "login": login,
        "search": {
            "paths": [
                {
                    "path": text(p.get("path")), "method": text(p.get("method")),
                    "inputs": scalars(p.get("inputs", {})), "categories": [text(c) for c in p.get("categories", [])],
                    "response": scalars(p.get("response", {})), "follow_redirect": bool(p.get("followredirect")),
                }
                for p in (search.get("paths") or ([{"path": search["path"]}] if search.get("path") else []))
            ],
            "inputs": scalars(search.get("inputs", {})), "headers": headers(search.get("headers")),
            "keywords_filters": filters(search.get("keywordsfilters")),
            "preprocessing_filters": filters(search.get("preprocessingfilters")),
            "allow_empty_inputs": bool(search.get("allowEmptyInputs")),
            "error": errors(search.get("error")),
            "rows": {
                "selector": text(search.get("rows", {}).get("selector")), "attribute": text(search.get("rows", {}).get("attribute")),
                "after": text(search.get("rows", {}).get("after")), "remove": text(search.get("rows", {}).get("remove")),
                "filters": filters(search.get("rows", {}).get("filters")),
                "count": scalars(search.get("rows", {}).get("count")) or None,
                "date_headers": date_headers(search.get("rows", {}).get("dateheaders")),
                "multiple": bool(search.get("rows", {}).get("multiple")),
                "missing_attribute_equals_no_results": bool(search.get("rows", {}).get("missingAttributeEqualsNoResults")),
            },
            "fields": [dict(extractor(v), name=k) for k, v in (search.get("fields") or {}).items()],
        },
    }
    dl = d.get("download")
    if dl:
        out["download"] = {
            "method": text(dl.get("method")), "before": scalars(dl.get("before")) or None,
            "infohash": infohash(dl.get("infohash")), "headers": headers(dl.get("headers")),
            "selectors": [dict(extractor(s), selector=text(s.get("selector"))) for s in dl.get("selectors", [])],
        }
    return out


def main():
    checkout, commit = sys.argv[1], sys.argv[2]
    specs = []
    for path in sorted(glob.glob(os.path.join(checkout, "definitions", "v11", "*.yml"))):
        with open(path, encoding="utf-8") as handle:
            specs.append(convert(yaml.safe_load(handle)))
    target = os.path.join(os.path.dirname(__file__), "..", "internal", "acquiring", "indexer", "spec", "data", "specs.json.gz")
    with gzip.GzipFile(target, "wb", mtime=0) as handle:
        handle.write(json.dumps({"source": "github.com/Prowlarr/Indexers definitions/v11", "commit": commit, "specs": specs},
                                ensure_ascii=False, separators=(",", ":")).encode("utf-8"))
    print(len(specs), "specs ->", os.path.relpath(target))


main()
