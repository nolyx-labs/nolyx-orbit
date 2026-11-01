import assert from 'node:assert/strict'
import { test } from 'node:test'
import { inviteToken, safeNext } from './paths.ts'

test('safeNext keeps local paths only', () => {
  assert.equal(safeNext('/p/1/overview'), '/p/1/overview')
  assert.equal(safeNext(null), '/')
  assert.equal(safeNext('https://evil.example'), '/')
  assert.equal(safeNext('//evil.example'), '/')
  assert.equal(safeNext('/\\evil.example'), '/')
})

test('inviteToken reads the token from an invite path', () => {
  assert.equal(inviteToken('/invite/orb_inv_abc'), 'orb_inv_abc')
  assert.equal(inviteToken('/p/1/overview'), undefined)
})
