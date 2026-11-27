// Run: node --test web/  (plain Node, no dependencies)
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const ctx = { window: {} };
vm.runInNewContext(readFileSync(new URL('./floorplan.js', import.meta.url), 'utf8'), ctx);
const { buildModel } = ctx.window.Floorplan;

// the shape /api/floorplan/generate writes (inline: floorplan/ is not committed)
const layered = {
  components: [
    { id: 'core-domain', name: 'Core Domain', depends_on: [] },
    { id: 'persist-db', name: 'Database Persistence', depends_on: ['core-domain'] },
    { id: 'auth-service', name: 'Authentication', depends_on: ['core-domain'] },
    { id: 'api-http', name: 'HTTP API Layer', depends_on: ['core-domain'] },
    { id: 'ui-present', name: 'Presentation Layer', depends_on: ['api-http'] },
  ],
  screens: [
    { id: 'login', calls: ['/v1/auth/login'] },
    { id: 'user-list', calls: ['/v1/users'] },
    { id: 'dashboard', calls: ['/v1/health', '/v1/users'] },
  ],
  build_order: ['core-domain', 'persist-db', 'auth-service', 'api-http', 'ui-present'],
};

test('lays out a layered plan: screens/UI left, core domain right', () => {
  const m = buildModel(layered);
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
