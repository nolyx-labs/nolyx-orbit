// Shared email layout. Templates render once at build time (npm run emails) into Go templates, so any
// dynamic value is a Go action string like "{{.Link}}" that the server fills in with html/template.
import { Body, Button, Container, Head, Hr, Html, Img, Link, Preview, Section, Text } from '@react-email/components'
import { ReactNode } from 'react'

const font = "-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif"
const c = { canvas: '#edf2f7', ink: '#1a202c', text: '#4a5568', muted: '#a0aec0', line: '#e2e8f0', button: '#111113' }

/** brand/footer: emails sent on behalf of a status page carry its name instead of Orbit's logo and footer. */
export function Layout({
  preview,
  children,
  brand,
  footer,
}: {
  preview: string
  children: ReactNode
  brand?: ReactNode
  footer?: ReactNode
}) {
  return (
    <Html lang="en">
      <Head />
      <Preview>{preview}</Preview>
      <Body style={{ backgroundColor: c.canvas, fontFamily: font, margin: 0, padding: '0 12px' }}>
        <Container style={{ maxWidth: 570, width: '100%', margin: '0 auto' }}>
          <Section style={{ padding: '28px 0', textAlign: 'center' }}>
            {brand ?? (
              <Link href="{{.AppURL}}">
                {/* Attached to each email by the server (emails.go), so it shows without loading remote images. */}
                <Img
                  src="cid:logo"
                  width={200}
                  height={36}
                  alt="Nolyx Labs"
                  style={{ display: 'inline-block', margin: '0 auto' }}
                />
              </Link>
            )}
          </Section>
          <Section
            style={{
              backgroundColor: '#ffffff',
              border: `1px solid ${c.line}`,
              borderRadius: 4,
              boxShadow: '0 2px 0 rgba(0, 0, 150, 0.025), 2px 4px 0 rgba(0, 0, 150, 0.015)',
              padding: 32,
            }}
          >
            {children}
          </Section>
          <Section style={{ padding: '28px 0', textAlign: 'center' }}>
            <Text style={{ color: c.muted, fontSize: 12, lineHeight: '18px', margin: 0 }}>
              {footer ?? (
                <>
                  Sent by Nolyx Orbit at{' '}
                  <Link href="{{.AppURL}}" style={{ color: c.muted }}>
                    {'{{.AppURL}}'}
                  </Link>
                </>
              )}
            </Text>
          </Section>
        </Container>
      </Body>
    </Html>
  )
}

export const H1 = ({ children }: { children: ReactNode }) => (
  <Text style={{ color: c.ink, fontSize: 20, fontWeight: 700, lineHeight: '28px', margin: '0 0 16px' }}>
    {children}
  </Text>
)

export const P = ({ children, muted }: { children: ReactNode; muted?: boolean }) => (
  <Text style={{ color: muted ? c.muted : c.text, fontSize: muted ? 14 : 16, lineHeight: '1.5em', margin: '0 0 16px' }}>
    {children}
  </Text>
)

export const Action = ({ href, children }: { href: string; children: ReactNode }) => (
  <Section style={{ padding: '8px 0 24px', textAlign: 'center' }}>
    <Button
      href={href}
      style={{
        backgroundColor: c.button,
        borderRadius: 6,
        color: '#ffffff',
        fontSize: 15,
        fontWeight: 600,
        padding: '11px 22px',
        textDecoration: 'none',
      }}
    >
      {children}
    </Button>
  </Section>
)

/** The fallback link under a button, as in Laravel's mail theme. */
export const Subcopy = ({ action, href }: { action: string; href: string }) => (
  <>
    <Hr style={{ borderColor: c.line, margin: '8px 0 20px' }} />
    <Text style={{ color: c.muted, fontSize: 13, lineHeight: '1.5em', margin: 0 }}>
      If the "{action}" button doesn't work, copy this link into your browser:{' '}
      <Link href={href} style={{ color: c.text, wordBreak: 'break-all' }}>
        {href}
      </Link>
    </Text>
  </>
)

/** Label/value rows in a tinted panel. */
export const Details = ({ rows, accent }: { rows: [string, string][]; accent: string }) => (
  <Section
    style={{
      backgroundColor: '#f7fafc',
      borderLeft: `4px solid ${accent}`,
      borderRadius: 4,
      margin: '0 0 24px',
      padding: '12px 16px',
    }}
  >
    {rows.map(([label, value]) => (
      <Text key={label} style={{ color: c.text, fontSize: 14, lineHeight: '22px', margin: '2px 0' }}>
        <span style={{ color: c.muted, display: 'inline-block', width: 110 }}>{label}</span>
        <span style={{ color: c.ink, fontWeight: 500 }}>{value}</span>
      </Text>
    ))}
  </Section>
)
