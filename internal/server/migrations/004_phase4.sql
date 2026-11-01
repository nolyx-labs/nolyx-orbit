-- Phase 4: teams & invites, per-project roles, regions, managed infrastructure.

alter table servers
  add column region text not null default '',
  add column provider text not null default '',
  add column provider_id text not null default '';

create table invites (
  id          uuid primary key default gen_random_uuid(),
  org_id      uuid not null references organizations on delete cascade,
  email       text not null,
  role        text not null check (role in ('admin','member','viewer')),
  token_hash  bytea not null unique,
  invited_by  uuid references users on delete set null,
  expires_at  timestamptz not null,
  accepted_at timestamptz,
  created_at  timestamptz not null default now()
);

create table teams (
  id         uuid primary key default gen_random_uuid(),
  org_id     uuid not null references organizations on delete cascade,
  name       text not null,
  created_at timestamptz not null default now(),
  unique (org_id, name)
);
create table team_members (
  team_id uuid not null references teams on delete cascade,
  user_id uuid not null references users on delete cascade,
  primary key (team_id, user_id)
);
-- A team grant raises a member's role on one project above their organization role.
create table team_project_roles (
  team_id    uuid not null references teams on delete cascade,
  project_id uuid not null references projects on delete cascade,
  role       text not null check (role in ('viewer','member','admin')),
  primary key (team_id, project_id)
);

create table cloud_accounts (
  id              uuid primary key default gen_random_uuid(),
  org_id          uuid not null references organizations on delete cascade,
  provider        text not null check (provider in ('hetzner')),
  name            text not null,
  token_encrypted bytea not null,
  created_at      timestamptz not null default now()
);

create table provisions (
  id               uuid primary key default gen_random_uuid(),
  project_id       uuid not null references projects on delete cascade,
  cloud_account_id uuid references cloud_accounts on delete set null,
  name             text not null,
  region           text not null,
  server_type      text not null,
  provider_id      text not null default '',
  ip               text not null default '',
  status           text not null default 'creating' check (status in ('creating','waiting_for_agent','ready','failed','deleted')),
  error            text not null default '',
  server_id        uuid references servers on delete set null,
  created_by       uuid references users on delete set null,
  created_at       timestamptz not null default now()
);

-- Effective project role: the higher of the organization role and a team grant (rank 0–2).
create function greatest_role(org_role text, team_rank int) returns text language sql immutable as $$
  select case
    when org_role = 'owner' then 'owner'
    when coalesce(team_rank, -1) > (case org_role when 'viewer' then 0 when 'member' then 1 when 'admin' then 2 end)
      then (array['viewer','member','admin'])[team_rank + 1]
    else org_role end
$$;
