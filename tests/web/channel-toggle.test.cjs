const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');

// The Channels section of web/app.js renders a per-row enable/disable switch
// whose inline handler calls toggleChannel(id, checked). This suite reproduces
// the gap-ledger defect "toggleChannel is referenced but never defined" and
// pins the required behavior: optimistic PATCH, rollback + toast on failure,
// and no writes for archived rows.

function fixture() {
  const source = fs.readFileSync(path.join(__dirname, '../../web/app.js'), 'utf8');
  const section = source.slice(source.indexOf('// ---------- Channels:'), source.indexOf('// ---- Slide-in editor ----'));
  const elements = new Map();
  const document = {getElementById(id) {
    if (!elements.has(id)) elements.set(id, {value:'', innerHTML:'', textContent:'', checked:false, indeterminate:false, disabled:false});
    return elements.get(id);
  }};
  const toasts = [];
  const context = vm.createContext({
    document, T:k=>k, esc:s=>String(s),
    showToast: m => { toasts.push(String(m)); },
  });
  vm.runInContext(`let channels = [
    {id:'a', name:'Alpha', base_url:'https://a.invalid', provider_id:'p', enabled:true, status:'enabled'},
    {id:'b', name:'Beta', base_url:'https://b.invalid', provider_id:'p', enabled:false, status:'disabled'},
    {id:'c', name:'Gamma', base_url:'https://g.invalid', provider_id:'p', enabled:false, status:'archived'}
  ]; let providerModels = [];\n${section}`, context);
  return {document, context, toasts, run: code => vm.runInContext(code, context)};
}

test('toggleChannel is defined and the rendered row switch handler resolves', () => {
  const f = fixture();
  f.run('renderChannelTable()');
  const html = f.document.getElementById('channels-table-rows').innerHTML;
  assert.match(html, /onchange="toggleChannel\('a',this\.checked\)"/);
  // RED today: toggleChannel is referenced by the row markup but never defined.
  assert.equal(f.run('typeof toggleChannel'), 'function');
});

test('toggleChannel optimistically updates state and PATCHes only that channel', async () => {
  const f = fixture();
  const calls = [];
  f.context.api = async (url, opts) => {
    calls.push({url, opts});
    return {channel: {id: 'b', enabled: true, status: 'enabled'}};
  };
  f.run('renderChannelTable()');
  await f.run('toggleChannel("b", true)');
  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, '/api/v1/channels/b');
  assert.equal(calls[0].opts.method, 'PATCH');
  const body = JSON.parse(calls[0].opts.body);
  assert.equal(body.enabled, true);
  assert.equal(body.status, 'enabled');
  assert.equal(body.id, undefined, 'must not re-derive or overwrite identity fields');
  assert.equal(f.run('channels.find(c=>c.id==="b").enabled'), true);
  assert.equal(f.run('channels.find(c=>c.id==="b").status'), 'enabled');
  assert.equal(f.toasts.length, 0);
});

test('toggleChannel rolls back and toasts on API failure without throwing', async () => {
  const f = fixture();
  f.context.api = async () => { throw new Error('upstream down'); };
  f.run('renderChannelTable()');
  await f.run('toggleChannel("a", false)');
  assert.equal(f.run('channels.find(c=>c.id==="a").enabled'), true, 'rollback to previous enabled state');
  assert.equal(f.run('channels.find(c=>c.id==="a").status'), 'enabled');
  assert.equal(f.toasts.length, 1);
  assert.match(f.toasts[0], /upstream down/);
});

test('toggleChannel never writes for archived channels', async () => {
  const f = fixture();
  const calls = [];
  f.context.api = async (url, opts) => { calls.push({url, opts}); return {}; };
  f.run('renderChannelTable()');
  // Archived rows render a badge instead of a switch; calling the handler
  // directly must still refuse to mutate or send any request.
  await f.run('toggleChannel("c", true)');
  assert.equal(calls.length, 0);
  assert.equal(f.run('channels.find(c=>c.id==="c").enabled'), false);
  assert.equal(f.run('channels.find(c=>c.id==="c").status'), 'archived');
});
