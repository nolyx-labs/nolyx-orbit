// Renders emails/templates.tsx into Go templates the server embeds: internal/server/emails/<name>.{html,txt}.
// The logo is rasterised to PNG and sent inline (cid:logo): mail clients don't render SVG.
// Run after changing a template (npm run emails) and commit the output; CI checks it's up to date.
import { mkdir, readFile, rm, writeFile } from 'node:fs/promises'
import { Resvg } from '@resvg/resvg-js'
import { render } from '@react-email/components'
import { runnerImport } from 'vite'

const out = new URL('../../internal/server/emails/', import.meta.url)
const { module } = await runnerImport('./emails/templates.tsx')
await rm(out, { recursive: true, force: true })
await mkdir(out, { recursive: true })
for (const [name, email] of Object.entries(module.templates)) {
  await writeFile(new URL(`${name}.html`, out), await render(email))
  await writeFile(new URL(`${name}.txt`, out), await render(email, { plainText: true }))
}
const svg = await readFile(new URL('../public/brand/logo-horizontal-on-light.svg', import.meta.url))
await writeFile(new URL('logo.png', out), new Resvg(svg, { fitTo: { mode: 'width', value: 400 } }).render().asPng()) // 2x of 200px
console.log(`rendered ${Object.keys(module.templates).length} emails`)
