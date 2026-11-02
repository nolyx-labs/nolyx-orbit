# Single sign-on

Orbit can sign people in with any OpenID Connect provider, such as Google, Microsoft Entra ID, Okta, Keycloak, Authentik or Auth0.

## Set it up

1. At your provider, create a web application (a confidential client):
   - Redirect URI: `APP_URL/api/v1/auth/oidc/callback`, for example `https://orbit.example.com/api/v1/auth/oidc/callback`
   - Scopes: `openid email profile`
2. Add these to `.env`, then run `docker compose up -d`:

   | Variable | Value |
   |---|---|
   | `OIDC_ISSUER` | The URL that serves `/.well-known/openid-configuration` (examples below) |
   | `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET` | From your provider |
   | `OIDC_NAME` | Label for the "Sign in with …" button, for example `Okta` |

Issuer examples:

- Google: `https://accounts.google.com`
- Microsoft Entra ID: `https://login.microsoftonline.com/<tenant-id>/v2.0`
- Okta: `https://<org>.okta.com`
- Keycloak: `https://keycloak.example.com/realms/<realm>`

## How it works

- **Verified emails only.** Orbit only accepts addresses the provider marks as verified (`email_verified`).
  - Microsoft Entra ID doesn't send that claim. In the app registration's **Token configuration**, add the optional claims `email` and `xms_edov`. Addresses on domains your tenant has verified are then accepted.
  - Multi-tenant (`/common`) issuers aren't supported.
- **Accounts are linked once.** The first sign-in links to the Orbit account with the same email. From then on the account only accepts that provider identity, so an email address reassigned at the provider can't take it over. To re-link, for example after changing provider, clear `oidc_subject` and `oidc_issuer` for that user in the database.
- **New people need an invite.** Someone without an account gets one only if sign-up is open (`ALLOW_SIGNUP`) or they have a pending invitation, as with passwords.
- **Two-factor still applies.** People with two-factor authentication enter their code after signing in with SSO.
- **Require SSO.** An owner can turn on **Require single sign-on** in **Settings → Organization**. Members of that organization can then only sign in with SSO. Owners can still use a password, so nobody is locked out if the provider is down. If SSO is later unconfigured, the requirement stops applying.
