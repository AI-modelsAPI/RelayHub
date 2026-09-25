const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

// Frontend <-> backend contract checks. These are static (no browser, no Go
// toolchain) so they run everywhere `node` does, and they exist because a whole
// family of endpoints once shipped implemented-but-unregistered while the UI
// swallowed every 404 (AUDIT P0-1 / RH-05) and the shell carried buttons with
// no handler at all (AUDIT 2.3 / RH-29).

const root = path.join(__dirname, "..", "..");
const appJs = fs.readFileSync(path.join(root, "web", "app.js"), "utf8");
const html = fs.readFileSync(path.join(root, "web", "index.html"), "utf8");
const serverGo = fs.readFileSync(path.join(root, "internal", "api", "server.go"), "utf8");

const registered = [...serverGo.matchAll(/mux\.HandleFunc\("([^"]+)"/g)].map((m) => m[1]);

function isRegistered(p) {
  const clean = p.split("?")[0];
  return registered.some((pat) => (pat.endsWith("/") ? clean.startsWith(pat) || clean + "/" === pat : clean === pat));
}

function frontendPaths() {
  const paths = new Set();
  for (const m of appJs.matchAll(/api\("([^"]+)"/g)) paths.add("/api/v1/" + m[1]);
  for (const m of appJs.matchAll(/fetch\("(\/api\/v1\/[^"]+)"/g)) paths.add(m[1]);
  return paths;
}

test("every API path the frontend calls is registered on the management mux", () => {
  const paths = frontendPaths();
  assert.ok(registered.length >= 30, `server.go route table looks truncated: ${registered.length} patterns`);
  assert.ok(paths.size >= 12, `expected the shell to call at least 12 endpoints, found ${paths.size}`);
  for (const p of paths) {
    assert.ok(isRegistered(p), `web/app.js calls ${p} but internal/api/server.go registers no handler for it`);
  }
});

test("the extras family the shell depends on is wired (regression for P0-1 / RH-05)", () => {
  for (const p of ["/api/v1/usage/summary", "/api/v1/verify/scores", "/api/v1/identity", "/api/v1/lab", "/api/v1/lab/capture", "/api/v1/routes/explain", "/api/v1/mcp"]) {
    assert.ok(isRegistered(p), `${p} is not registered`);
  }
});

test("every button in the shell has a handler in app.js (no dead controls)", () => {
  const ids = [...html.matchAll(/<button[^>]*\bid="([^"]+)"/g)].map((m) => m[1]);
  assert.ok(ids.length >= 3, `expected several id'd buttons, found ${ids.length}`);
  for (const id of ids) {
    assert.ok(appJs.includes(`#${id}`), `button #${id} in index.html is never referenced by app.js`);
  }
  // Rail buttons are addressed by data-view instead of id.
  assert.match(appJs, /\.rail-btn/);
});

test("the shell no longer advertises unimplemented lab modes or dead create buttons", () => {
  assert.doesNotMatch(html, /data-lab="(replay|probe|hedge)"/);
  assert.doesNotMatch(html, /id="(ch-new|md-new)"/);
});

test("offline state is derived from the overview heartbeat, not assumed", () => {
  // The old boot() removed the `off` class unconditionally inside a try that
  // could not throw; the fix must branch on the overview result.
  assert.match(appJs, /state\.online = ok\(ov\)/);
  assert.match(appJs, /live\.add\("off"\)/);
});

test("the console pairs through the URL fragment and an in-page gate (AUDIT 2026-09-24 F4)", () => {
  assert.match(appJs, /#pair=/);
  assert.match(appJs, /fetch\("\/api\/v1\/auth\/pair"/);
  // The one-time code must leave the address bar and history before use.
  assert.match(appJs, /history\.replaceState/);
  // WKWebView (the desktop shell) has no window.prompt; a prompt-based login
  // would lock the desktop app out.
  assert.doesNotMatch(appJs, /\bprompt\(/);
  assert.match(html, /<form[^>]*id="auth-gate"/);
  assert.ok(isRegistered("/api/v1/auth/pair") && isRegistered("/api/v1/auth/pair-codes"));
});
