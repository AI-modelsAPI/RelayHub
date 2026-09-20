const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

const root = path.join(__dirname, "..", "..", "web");
const html = fs.readFileSync(path.join(root, "index.html"), "utf8");
const css = fs.readFileSync(path.join(root, "styles.css"), "utf8");

test("desktop shell has rail views, not a website nav list", () => {
  assert.match(html, /class="rail"/);
  assert.doesNotMatch(html, /nav-list/);
  for (const v of ["pulse", "channels", "models", "identity", "checkin", "lab", "usage", "agents", "settings"]) {
    assert.match(html, new RegExp(`data-view="${v}"`));
  }
});

test("palette uses command-key, not site search", () => {
  assert.match(html, /id="cmd-open"/);
  assert.match(html, /id="palette"/);
});

test("forest-green tokens exist", () => {
  assert.match(css, /--deep:\s*#143528/i);
  assert.match(css, /--sage:\s*#52b788/i);
});
