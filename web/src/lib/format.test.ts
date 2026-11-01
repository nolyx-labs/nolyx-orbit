import assert from 'node:assert/strict'
import { test } from 'node:test'
import { bitrate, bytes, pct } from './format.ts'

test('formatters', () => {
  assert.equal(bytes(null), '—')
  assert.equal(bytes(512), '512 B')
  assert.equal(bytes(1536), '1.5 KB')
  assert.equal(bitrate(125_000), '1.0 Mbps')
  assert.equal(pct(5.25), '5.3%')
  assert.equal(pct(42.4), '42%')
})
