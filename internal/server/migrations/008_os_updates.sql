-- OS package updates and server maintenance windows.
create extension if not exists btree_gist; -- uuid equality in the maintenance overlap constraint

-- The latest update check per server.
create table os_update_checks (
  server_id       uuid primary key references servers on delete cascade,
  manager         text not null default '', -- apt | dnf
  packages        jsonb not null default '[]', -- [{name, installed, available, security}]
  held            jsonb not null default '[]', -- package names kept at their version
  reboot_required boolean not null default false,
  error           text not null default '', -- why the last check failed; packages keep the last good result
  checked_at      timestamptz,              -- last successful check
  attempted_at    timestamptz not null default now()
);

create table os_rollouts (
  id              uuid primary key default gen_random_uuid(),
  project_id      uuid not null references projects on delete cascade,
  scope           text not null check (scope in ('all','security','packages')),
  packages        jsonb not null default '[]', -- [{name, version}]; version empty = latest
  batch_size      int not null default 0 check (batch_size >= 0), -- 0: all servers at once
  stop_on_failure boolean not null default true,
  reboot          text not null check (reboot in ('never','if_required')),
  starts_at       timestamptz not null default now(),
  status          text not null default 'scheduled' check (status in ('scheduled','running','done','failed','cancelled')),
  heartbeat_at    timestamptz, -- the runner is alive; a stale running rollout is picked up again
  created_by      uuid references users on delete set null,
  created_at      timestamptz not null default now(),
  finished_at     timestamptz
);
create index on os_rollouts (project_id, created_at desc);
create index on os_rollouts (starts_at) where status in ('scheduled','running');

create table os_rollout_servers (
  rollout_id   uuid not null references os_rollouts on delete cascade,
  server_id    uuid not null references servers on delete cascade,
  position     int not null,
  status       text not null default 'waiting' check (status in ('waiting','running','done','failed','skipped')),
  result       text not null default '', -- plain-language outcome
  operation_id uuid,                     -- the install, with its log
  rebooting    boolean not null default false,
  started_at   timestamptz,
  finished_at  timestamptz,
  primary key (rollout_id, server_id)
);

-- Set while a server restarts for updates, to hold its alerts.
alter table servers add column alerts_held_until timestamptz;

create table server_maintenance (
  id           uuid primary key default gen_random_uuid(),
  project_id   uuid not null references projects on delete cascade,
  server_id    uuid not null references servers on delete cascade,
  starts_at    timestamptz not null,
  ends_at      timestamptz not null,
  reason       text not null default '',
  action       text not null default 'none' check (action in ('none','restart','update')),
  status       text not null default 'planned' check (status in ('planned','active','done','cancelled')),
  action_at    timestamptz, -- when the start action was sent
  rollout_id   uuid references os_rollouts on delete set null, -- action = update
  result       text not null default '',
  incident_ids uuid[] not null default '{}', -- status page maintenance entries created for it
  created_by   uuid references users on delete set null,
  created_at   timestamptz not null default now(),
  check (ends_at > starts_at),
  exclude using gist (server_id with =, tstzrange(starts_at, ends_at) with &&) where (status in ('planned','active'))
);
create index on server_maintenance (server_id, starts_at desc);

-- Servers whose alerts are held right now: inside a maintenance window, or restarting for updates.
create view servers_on_hold as
  select server_id id from server_maintenance where status in ('planned','active') and starts_at <= now() and ends_at > now()
  union select id from servers where alerts_held_until > now();
