export function Meter({ value }: { value: number | null | undefined }) {
  const v = value ?? 0
  const tone = v >= 90 ? 'err' : v >= 75 ? 'warn' : ''
  return (
    <div className="meter" title={value == null ? 'no data' : `${v.toFixed(1)}%`}>
      <div className="bar">
        <i className={tone} style={{ width: `${Math.min(100, v)}%` }} />
      </div>
      <span className="val">{value == null ? '—' : `${v.toFixed(0)}%`}</span>
    </div>
  )
}
