-- Two-factor authentication (TOTP + recovery codes) and password resets.
alter table users
  add column totp_secret_encrypted bytea,
  add column totp_enabled_at       timestamptz,
  add column totp_last_step        bigint not null default 0; -- last accepted 30s step, so a code can't be replayed

create table recovery_codes (
  user_id   uuid not null references users on delete cascade,
  code_hash bytea not null,
  used_at   timestamptz,
  primary key (user_id, code_hash)
);

create table password_resets (
  token_hash bytea primary key,
  user_id    uuid not null references users on delete cascade,
  expires_at timestamptz not null,
  used_at    timestamptz,
  recovery   boolean not null default false -- issued by an operator (orbit reset-password): also turns off 2FA
);
