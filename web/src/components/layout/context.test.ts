import assert from 'node:assert/strict'
import { test } from 'node:test'
import { pageArea } from './context.ts'

test('pageArea maps pages to custom-role areas', () => {
  assert.equal(pageArea('/p/1/servers'), 'servers')
  assert.equal(pageArea('/p/1/servers/s/updates'), 'updates')
  assert.equal(pageArea('/p/1/servers/s/overview'), 'servers')
  assert.equal(pageArea('/p/1/status-pages/x/incidents'), 'monitoring')
  assert.equal(pageArea('/p/1/settings/agents'), 'servers')
  assert.equal(pageArea('/p/1/settings/account'), '')
  assert.equal(pageArea('/p/1/overview'), '')
})
