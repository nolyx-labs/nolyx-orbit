import assert from 'node:assert/strict'
import { test } from 'node:test'
import { TEMPLATES, getAt, insertAt, setAt, stepSummary, triggerSummary } from './workflowHelpers.ts'

test('summaries', () => {
  assert.equal(
    triggerSummary({ type: 'schedule', every: 'weekly', at: '03:00', weekday: 1 }),
    'Every Monday at 03:00 UTC',
  )
  assert.equal(triggerSummary({ type: 'monitor_down', monitor_id: 'm' }, { m: 'site' }), 'A monitor goes down (site)')
  assert.equal(stepSummary({ type: 'wait', seconds: 7200 }), 'Wait 2 hours')
  assert.equal(stepSummary({ type: 'restart', service_id: 'gone' }), 'Restart a deleted item')
})

test('tree edits copy and splice', () => {
  const def = TEMPLATES.find((t) => t.key === 'crash')!.build({ service: 's', to: { user_ids: ['u'] } })
  const added = insertAt(def, ['steps', 2, 'then'], 0, { type: 'wait', seconds: 5 })
  assert.equal(getAt(added, ['steps', 2, 'then', 0]).seconds, 5)
  assert.equal(def.steps[2].then!.length, 0, 'original untouched')
  const removed = setAt(added, ['steps', 0], undefined)
  assert.equal(removed.steps.length, 2)
  assert.equal(removed.steps[0].type, 'wait')
})
