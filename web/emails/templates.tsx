// Every email Orbit sends. The key is the template name the Go server uses (sendTemplate).
import { Link, Text } from '@react-email/components'
import { Action, Details, H1, Layout, P, Subcopy } from './components'

// Status page emails go to the page's own visitors, so they show the page's name, not Orbit's logo.
const pageBrand = <Text style={{ color: '#1a202c', fontSize: 20, fontWeight: 700, margin: 0 }}>{'{{.PageName}}'}</Text>

export const templates = {
  'password-reset': (
    <Layout preview="Reset your Nolyx Orbit password">
      <H1>Reset your password</H1>
      <P>Someone asked to reset the password for your Nolyx Orbit account. Choose a new one with the button below.</P>
      <Action href="{{.Link}}">Reset password</Action>
      <P>
        This link expires in one hour. If you didn't ask for a reset, you can ignore this email. Your password won't
        change.
      </P>
      <Subcopy action="Reset password" href="{{.Link}}" />
    </Layout>
  ),

  invite: (
    <Layout preview="{{.Inviter}} invited you to {{.Org}} on Nolyx Orbit">
      <H1>Join {'{{.Org}}'} on Nolyx Orbit</H1>
      <P>
        {'{{.Inviter}}'} invited you to the <b>{'{{.Org}}'}</b> organization as <b>{'{{.Role}}'}</b>. Accept to see its
        servers, services and databases.
      </P>
      <Action href="{{.Link}}">Accept invitation</Action>
      <P muted>This invitation expires in 7 days and only works for {'{{.Email}}'}.</P>
      <Subcopy action="Accept invitation" href="{{.Link}}" />
    </Layout>
  ),

  alert: (
    <Layout preview="{{.Title}}">
      <P muted>
        <span style={{ color: '{{.Color}}', fontWeight: 700, letterSpacing: '0.04em', textTransform: 'uppercase' }}>
          {'{{.Status}}'}
        </span>
      </P>
      <H1>{'{{.Title}}'}</H1>
      <Details
        accent="{{.Color}}"
        rows={[
          ['Rule', '{{.Rule}}'],
          ['Target', '{{.Target}}'],
          ['Value', '{{.Value}}'],
          ['Time', '{{.Time}}'],
        ]}
      />
      <Action href="{{.Link}}">View alerts</Action>
      <Subcopy action="View alerts" href="{{.Link}}" />
    </Layout>
  ),

  'status-update': (
    <Layout
      preview="{{.Status}}: {{.Message}}"
      brand={pageBrand}
      footer={
        <>
          You subscribed to updates from {'{{.PageName}}'}.{' '}
          <Link href="{{.Unsubscribe}}" style={{ color: '#a0aec0' }}>
            Unsubscribe
          </Link>
        </>
      }
    >
      <P muted>
        <span style={{ color: '{{.Color}}', fontWeight: 700, letterSpacing: '0.04em', textTransform: 'uppercase' }}>
          {'{{.Status}}'}
        </span>
      </P>
      <H1>{'{{.Title}}'}</H1>
      <P>{'{{.Message}}'}</P>
      <Details accent="{{.Color}}" rows={[['Affected', '{{.Components}}']]} />
      <Action href="{{.Link}}">View incident</Action>
      <Subcopy action="View incident" href="{{.Link}}" />
    </Layout>
  ),

  'status-confirm': (
    <Layout
      preview="Confirm your subscription to {{.PageName}}"
      brand={pageBrand}
      footer="If you didn't ask for this, ignore this email and you won't be subscribed."
    >
      <H1>Confirm your subscription</H1>
      <P>
        Confirm that you'd like to get an email whenever {'{{.PageName}}'} posts an incident or scheduled maintenance.
      </P>
      <Action href="{{.Link}}">Confirm subscription</Action>
      <Subcopy action="Confirm subscription" href="{{.Link}}" />
    </Layout>
  ),

  'test-notification': (
    <Layout preview="Test notification from {{.Channel}}">
      <H1>Your email channel works</H1>
      <P>
        This is a test from the <b>{'{{.Channel}}'}</b> notification channel. Alerts that use it will arrive at this
        address.
      </P>
    </Layout>
  ),
}
