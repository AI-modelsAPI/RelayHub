const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');

function fixture() {
  const source = fs.readFileSync(path.join(__dirname, '../../web/app.js'), 'utf8');
  const section = source.slice(source.indexOf('// ---------- Channels:'), source.indexOf('// ---- Slide-in editor ----'));
  const elements = new Map();
  const document = {getElementById(id) {
    if (!elements.has(id)) elements.set(id, {value:'', innerHTML:'', textContent:'', checked:false, indeterminate:false, disabled:false});
    return elements.get(id);
  }};
  const context = vm.createContext({document, T:k=>k, esc:s=>String(s), showToast:()=>{}});
  vm.runInContext(`let channels = [
    {id:'a', name:'Alpha', base_url:'https://a.invalid', provider_id:'p', enabled:true},
    {id:'b', name:'Beta', base_url:'https://b.invalid', provider_id:'p', enabled:false}
  ]; let providerModels = [];\n${section}`, context);
  return {document, context, run:code=>vm.runInContext(code, context)};
}

test('row selection, half state, filtered select-all and deselect preserve hidden selection', () => {
  const f = fixture();
  f.run('renderChannelTable()');
  assert.match(f.document.getElementById('channels-table-rows').innerHTML, /data-channel-select=/);
  f.run('setChannelSelected("a", true)');
  assert.equal(f.document.getElementById('ch-select-all').indeterminate, true);
  f.document.getElementById('ch-search').value = 'Beta';
  f.run('renderChannelTable(); selectVisibleChannels(true)');
  assert.equal(f.run('selectedChannelIds.size'), 2);
  assert.equal(f.document.getElementById('ch-select-all').checked, true);
  f.run('selectVisibleChannels(false)');
  assert.equal(f.run('selectedChannelIds.has("a")'), true);
  assert.equal(f.run('selectedChannelIds.has("b")'), false);
});

test('change events delegated from tbody and header drive selection', async () => {
  const f = fixture();
  f.run('renderChannelTable()');
  f.run('renderChannelTable()'); // re-render must not break delegation
  const rowBox = {getAttribute: k => k === 'data-channel-select' ? encodeURIComponent('a') : null, checked: true};
  f.document.getElementById('channels-table-rows').onchange({target: rowBox});
  assert.equal(f.run('selectedChannelIds.has("a")'), true);
  rowBox.checked = false; rowBox.getAttribute = () => null; // foreign target: ignored
  f.document.getElementById('channels-table-rows').onchange({target: rowBox});
  assert.equal(f.run('selectedChannelIds.has("a")'), true);
  rowBox.getAttribute = k => k === 'data-channel-select' ? encodeURIComponent('a') : null;
  f.document.getElementById('channels-table-rows').onchange({target: rowBox});
  assert.equal(f.run('selectedChannelIds.has("a")'), false);
  f.document.getElementById('ch-select-all').onchange({target: {checked: true}});
  assert.equal(f.run('selectedChannelIds.size'), 2);
});

test('refresh prunes deleted channels, empty filter disables select-all, selection never calls API', async () => {
  const f = fixture();
  f.run('setChannelSelected("a", true); setChannelSelected("b", true)');
  const calls = [];
  f.context.api = async url => {calls.push(url); return url.endsWith('/channels') ? {channels:[{id:'b', name:'Beta', base_url:'', provider_id:'p', enabled:false}]} : {provider_models:[]};};
  await f.run('loadChannels()');
  assert.deepEqual(calls, ['/api/v1/channels', '/api/v1/provider-models']);
  assert.equal(f.run('selectedChannelIds.size'), 1);
  assert.equal(f.run('selectedChannelIds.has("a")'), false);
  f.document.getElementById('ch-search').value = 'no match';
  f.run('renderChannelTable(); selectVisibleChannels(true)');
  assert.equal(f.document.getElementById('ch-select-all').disabled, true);
  assert.equal(f.document.getElementById('ch-select-all').checked, false);
  assert.equal(f.document.getElementById('ch-select-all').indeterminate, false);
  assert.match(f.document.getElementById('channels-table-rows').innerHTML, /colspan="8"/);
  assert.equal(f.run('selectedChannelIds.size'), 1);
  assert.equal(calls.length, 2);
});
