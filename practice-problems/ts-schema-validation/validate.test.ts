// The goal test: do not edit it, change parse.ts until it passes.
// Runs on Node 22.18+ with no dependencies: node --test validate.test.ts
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parseUser } from './parse.ts';

test('accepts a valid user', () => {
  assert.deepEqual(parseUser('{"id":"u1","email":"a@b.co","age":30}'), { id: 'u1', email: 'a@b.co', age: 30 });
});

test('rejects data that does not match the type', () => {
  for (const bad of ['null', '[]', '{}', '{"id":1,"email":"a@b.co","age":30}',
    '{"id":"u1","email":"a@b.co","age":"30"}', '{"id":"u1","age":30}']) {
    assert.throws(() => parseUser(bad), undefined, bad);
  }
});

test('no `any` crosses the boundary', () => {
  const src = readFileSync(new URL('./parse.ts', import.meta.url), 'utf8');
  assert.doesNotMatch(src, /(:|\bas)\s*any\b|<any>/);
});
