import { useEffect, useRef, useState } from 'react'
import uPlot from 'uplot'

export interface ChartSeries {
  label: string
  values: (number | null)[]
  color: 'series-1' | 'series-2' // CSS token; categorical slots in fixed order
}

interface Props {
  title: string
  t: number[]
  series: ChartSeries[]
  format: (v: number) => string
  percent?: boolean
  integer?: boolean // counts: keep axis ticks on whole numbers
  height?: number
}

const css = (name: string) => getComputedStyle(document.documentElement).getPropertyValue(`--${name}`).trim()

function tooltipPlugin(format: (v: number) => string): uPlot.Plugin {
  let tip: HTMLDivElement
  return {
    hooks: {
      init: (u) => {
        tip = document.createElement('div')
        tip.className = 'chart-tip'
        tip.style.display = 'none'
        u.over.appendChild(tip)
        u.over.addEventListener('mouseleave', () => (tip.style.display = 'none'))
      },
      setCursor: (u) => {
        const i = u.cursor.idx
        if (i == null || u.cursor.left == null || u.cursor.left < 0) {
          tip.style.display = 'none'
          return
        }
        const ts = new Date(u.data[0][i] * 1000).toLocaleString([], {
          month: 'short',
          day: '2-digit',
          hour: '2-digit',
          minute: '2-digit',
          second: '2-digit',
          hour12: false,
        })
        tip.innerHTML =
          `<div class="ts">${ts}</div>` +
          u.series
            .slice(1)
            .map((s, k) => {
              const v = u.data[k + 1][i]
              return `<div class="r"><span class="dot" style="background:${s.stroke instanceof Function ? '' : s.stroke}"></span>${s.label}<b>${v == null ? '—' : format(v)}</b></div>`
            })
            .join('')
        tip.style.display = 'block'
        const w = tip.offsetWidth
        const left = u.cursor.left + 12 + w > u.over.clientWidth ? u.cursor.left - w - 12 : u.cursor.left + 12
        tip.style.left = `${left}px`
        tip.style.top = `${Math.max(0, (u.cursor.top ?? 0) - 10)}px`
      },
    },
  }
}

export function Chart({ title, t, series, format, percent, integer, height = 160 }: Props) {
  const el = useRef<HTMLDivElement>(null)
  const plot = useRef<uPlot | null>(null)
  const [theme, setTheme] = useState(0)

  useEffect(() => {
    const bump = () => setTheme((n) => n + 1)
    window.addEventListener('orbit-theme', bump)
    return () => window.removeEventListener('orbit-theme', bump)
  }, [])

  const shape = series.map((s) => s.label).join('|')
  const data = [t, ...series.map((s) => s.values)] as uPlot.AlignedData

  useEffect(() => {
    if (!el.current) return
    const axis = {
      stroke: css('text-3'),
      grid: { stroke: css('grid'), width: 1 },
      ticks: { show: false },
      font: '11px ' + css('mono'),
      gap: 6,
    }
    const opts: uPlot.Options = {
      width: el.current.clientWidth,
      height,
      padding: [6, 4, 0, 0],
      cursor: { points: { size: 8, width: 2, stroke: css('bg-1') }, y: false, drag: { x: false, y: false } },
      legend: { show: false },
      scales: {
        x: { time: true },
        y: { range: (_u, _min, max) => [0, percent ? Math.max(100, max) : Math.max(max * 1.15, 1)] },
      },
      axes: [
        {
          ...axis,
          size: 22,
          values: (u, splits) => {
            const t = u.data[0]
            const long = t.length > 1 && t[t.length - 1] - t[0] > 2 * 86400
            const opts: Intl.DateTimeFormatOptions = long
              ? { month: 'short', day: 'numeric' }
              : { hour: '2-digit', minute: '2-digit', hour12: false }
            return splits.map((s) => new Date(s * 1000).toLocaleString([], opts))
          },
        },
        {
          ...axis,
          size: percent ? 44 : 72,
          values: (_u, vals) => vals.map((v) => (percent ? `${v}%` : format(v))),
          ...(integer ? { incrs: [1, 2, 5, 10, 20, 50, 100, 200, 500, 1000, 2000, 5000, 10000] } : {}),
        },
      ],
      series: [
        {},
        ...series.map((s) => {
          const c = css(s.color)
          return {
            label: s.label,
            stroke: c,
            width: 2,
            points: { show: false },
            fill: series.length === 1 ? c + '14' : undefined,
            spanGaps: false,
          }
        }),
      ],
      plugins: [tooltipPlugin(format)],
    }
    plot.current = new uPlot(opts, data, el.current)
    const ro = new ResizeObserver(() => el.current && plot.current?.setSize({ width: el.current.clientWidth, height }))
    ro.observe(el.current)
    return () => {
      ro.disconnect()
      plot.current?.destroy()
      plot.current = null
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [shape, height, percent, integer, theme])

  useEffect(() => {
    plot.current?.setData(data)
  })

  const last = series.map((s) => [...s.values].reverse().find((v) => v != null))
  return (
    <div className="chart-panel">
      <div className="chart-head">
        <h4>{title}</h4>
        {series.length > 1 && (
          <div className="chart-legend">
            {series.map((s, i) => (
              <span key={s.label}>
                <i style={{ background: `var(--${s.color})` }} />
                {s.label} <span className="mono">{last[i] == null ? '—' : format(last[i]!)}</span>
              </span>
            ))}
          </div>
        )}
        {series.length === 1 && <span className="now">{last[0] == null ? '—' : format(last[0]!)}</span>}
      </div>
      {t.length === 0 && <div className="chart-empty">No data in this range</div>}
      <div
        ref={el}
        style={{ display: t.length === 0 ? 'none' : 'block', position: 'relative' }}
        role="img"
        aria-label={`${title} chart`}
      />
    </div>
  )
}
