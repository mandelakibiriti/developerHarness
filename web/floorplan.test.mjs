// Run: node --test web/  (plain Node, no dependencies)
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const ctx = { window: {} };
vm.runInNewContext(readFileSync(new URL('./floorplan.js', import.meta.url), 'utf8'), ctx);
const { buildModel } = ctx.window.Floorplan;

test('lays out the real plan: screens/UI left, core domain right', () => {
  const plan = JSON.parse(readFileSync(new URL('../floorplan/plan.json', import.meta.url), 'utf8'));
  const m = buildModel(plan);
  const col = (id) => m.byId.get(id).col;
  assert.equal(col('core-domain'), Math.max(...m.nodes.map((n) => n.col)), 'core domain is rightmost');
  assert.ok(col('ui-present') < col('api-http') && col('api-http') < col('core-domain'));
  assert.equal(m.api.id, 'api-http', 'screens attach to the API module');
  assert.equal(m.byId.get('screen:login').deps[0], 'api-http');
  assert.equal(m.byId.get('core-domain').step, 1);
  assert.equal(m.edges.length, 4 + 3, 'four depends_on edges + three screen edges');
});

test('prefers the public API over an auth gateway for screen calls', () => {
  const m = buildModel({ components: [{ id: 'auth-api-gateway' }, { id: 'public-http-api' }], screens: [{ id: 's' }] });
  assert.equal(m.api.id, 'public-http-api');
});

test('survives cycles and dangling dependencies', () => {
  const m = buildModel({ components: [
    { id: 'a', depends_on: ['b'] }, { id: 'b', depends_on: ['a', 'missing'] },
  ] });
  assert.equal(m.nodes.length, 2);
  assert.ok(m.nodes.every((n) => Number.isFinite(n.x) && Number.isFinite(n.y)));
  assert.ok(m.byId.get('b').deps.every((d) => d !== 'missing'));
});
