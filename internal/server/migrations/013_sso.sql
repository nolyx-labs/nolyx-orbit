-- Single sign-on (OpenID Connect). Once linked, a user is pinned to their provider subject, so an email
-- address the provider later gives to someone else can't be used to take over the account.
alter table users
  add column oidc_issuer    text,
  add column oidc_subject   text,
  add column oidc_linked_at timestamptz;
create unique index users_oidc_identity on users (oidc_issuer, oidc_subject);

-- Members (not owners) of an organization that requires SSO can't sign in with a password.
alter table organizations add column require_sso boolean not null default false;
