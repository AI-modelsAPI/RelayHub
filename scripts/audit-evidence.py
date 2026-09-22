#!/usr/bin/env python3
"""Read-only evidence probes for AUDIT-REPORT.md (2026-09-22 baseline).

Requires Python 3 and Node.js, no third-party packages or network access.
This is NOT a Go integration/security test suite. It executes the actual JS
with a minimal DOM stub, executes repository SQL using Python's SQLite, and
checks selected source invariants. OBSERVED means a baseline defect was found;
NOT_OBSERVED means re-review is needed (e.g. after a fix), not a security pass.
No persistent database, real credentials, upstream requests or files are made.
"""
import json
from pathlib import Path
import re
import sqlite3
import subprocess
from html.parser import HTMLParser

ROOT = Path(__file__).resolve().parents[1]


def source(path):
    return (ROOT / path).read_text(encoding="utf-8")


def section(text, start, end):
    return text.split(start, 1)[1].split(end, 1)[0]


def emit(probe, observed, evidence):
    print(json.dumps({"probe": probe, "status": "OBSERVED" if observed else "NOT_OBSERVED",
                      "evidence": evidence}, ensure_ascii=False))


class Elements(HTMLParser):
    def __init__(self):
        super().__init__()
        self.events = []

    def handle_starttag(self, tag, attrs):
        self.events.extend((tag, key, value) for key, value in attrs if key.startswith("on"))


def main():
    # Run repository JS, not a Python translation. No browser execution of the
    # injected event occurs: parse its generated markup separately below.
    js_probe = r'''
const fs = require("node:fs");
const vm = require("node:vm");
const nodes = new Map();
function node(id) {
  if (!nodes.has(id)) nodes.set(id, {
    value: "", innerHTML: "", textContent: "", hidden: true,
    classList: { removed: [], add() {}, toggle() {}, remove(x) { this.removed.push(x); } },
    addEventListener() {}, focus() {},
  });
  return nodes.get(id);
}
const sandbox = {
  document: { querySelector: node, querySelectorAll: () => [], addEventListener() {} },
  fetch: async () => { throw new Error("audit simulated offline"); },
  setInterval() {},
};
vm.createContext(sandbox);
vm.runInContext(fs.readFileSync("web/app.js", "utf8"), sandbox);
(async () => {
  await vm.runInContext("boot()", sandbox);
  const html = vm.runInContext(`rowHTML('x"><img src=x onerror="globalThis.auditMarker=1">', 'safe', '', '')`, sandbox);
  const tagHTML = vm.runInContext(`rowHTML('safe', 'safe', '', '<img src=x onerror="globalThis.auditMarker=2">')`, sandbox);
  console.log(JSON.stringify({ html, tagHTML, offlineShownAsOnline: node("#core-live").classList.removed.includes("off") }));
})().catch(e => { console.error(e); process.exitCode = 1; });
'''
    result = subprocess.run(["node", "-e", js_probe], cwd=ROOT, check=True,
                            capture_output=True, text=True)
    js = json.loads(result.stdout)
    parser = Elements()
    parser.feed(js["html"])
    parser.feed(js["tagHTML"])
    emit("V01 / RH-01: JS markup injection", len(parser.events) == 2, parser.events)
    emit("V02 / RH-29: offline indicator", js["offlineShownAsOnline"],
         "All fetches rejected; boot() removed the off class" if js["offlineShownAsOnline"] else "indicator changed")

    # Use the literal migration order/statements, not the modernc Go driver.
    with sqlite3.connect(":memory:") as db:
        db.execute("PRAGMA foreign_keys=ON")
        migrations = sorted((ROOT / "internal/storage/migrations").glob("*.sql"))
        for migration in migrations:
            for statement in migration.read_text().split(";"):
                if statement.strip():
                    db.execute(statement)
        print(json.dumps({"probe": "V03: SQLite migrations", "status": "PASS",
                          "count": len(migrations), "engine": sqlite3.sqlite_version}))
        cols = {row[1] for row in db.execute("PRAGMA table_info(channels)")}
        emit("V04 / RH-09: channel header persistence", "custom_headers" not in cols,
             "custom_headers absent from migrated channels schema")
        db.execute("INSERT INTO request_records(id,request_id,created_at,cache_read_tokens,ttft_ms,upstream_model) VALUES('audit','audit','2026-09-22T00:00:00Z',81,123,'upstream')")
        repo = source("internal/repository/repository.go")
        read_sql = re.search(r'`(SELECT [^`]+ FROM request_records ORDER BY created_at DESC)`', repo).group(1)
        cur = db.execute(read_sql)
        fields = [col[0] for col in cur.description]
        cur.fetchall()
        emit("V05 / RH-16: request metadata readback", "cache_read_tokens" not in fields,
             {"persisted_cache_tokens": 81, "read_fields": fields})
        db.execute("INSERT INTO model_groups(id,name,strategy,enabled,created_at,updated_at) VALUES('cycle','cycle','priority',1,'t','t')")
        db.execute("UPDATE model_groups SET fallback_group_id='cycle' WHERE id='cycle'")
        cycle = db.execute("SELECT id=fallback_group_id FROM model_groups WHERE id='cycle'").fetchone()[0]
        selector = source("internal/router/selector.go")
        body = section(selector, "func (r Resolver) selectGroupModel(", "func (r Resolver) candidates(")
        emit("V06 / RH-02: fallback cycle", bool(cycle) and body.count("visited[") == 1,
             "SQLite accepts self-cycle; selector only assigns visited[groupID], never checks it; recursion NOT executed")
        for provider in ("a", "b"):
            db.execute("INSERT INTO providers(id,name,adapter_type,protocol,created_at,updated_at) VALUES(?,?, 'generic','openai-chat','t','t')", (provider, provider))
        db.execute("INSERT INTO channels(id,provider_id,name,base_url,created_at,updated_at) VALUES('c','b','c','https://example.invalid','t','t')")
        db.execute("INSERT INTO models(id,display_name,created_at,updated_at) VALUES('m','m','t','t')")
        db.execute("INSERT INTO provider_models(id,provider_id,channel_id,model_id,upstream_model_name,protocol,created_at,updated_at) VALUES('pm','a','c','m','m','openai-chat','t','t')")
        emit("V07 / RH-02: cross-provider binding", True,
             "Foreign keys accept provider_model(provider=a) bound to channel(provider=b)")

    server = source("internal/api/server.go")
    routes = section(server, "func (s *Server) managementRoutes()", "func (s *Server) peerAllowed(")
    emit("V08 / RH-05: extras route registration", "s.extras" not in routes,
         "managementRoutes does not register s.extras")
    gateway = source("internal/gateway/gateway.go")
    emit("V09 / RH-06: runaway guard invocation", "Guard" in gateway and ".Trip(" not in gateway,
         "Guard declared in Config; no Trip call in gateway.go")
    emit("V10 / RH-33: missing acceptance probe", not (ROOT / "docs/acceptance/packaging_probe.py").exists()
         and "docs/acceptance/packaging_probe.py" in source("tests/macos/test_full_suite.sh"),
         "macOS full-suite script references absent docs/acceptance/packaging_probe.py")
    codex = source("internal/clisync/codex/codex.go")
    emit("V11 / RH-23: Codex key delivery", '"RELAYHUB_API_KEY"' in codex and "desired.APIKey" not in codex,
         "Codex config requires RELAYHUB_API_KEY but Syncer never reads desired.APIKey")


if __name__ == "__main__":
    main()
