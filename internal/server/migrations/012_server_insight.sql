-- Server insight (services, ports, specs, security scan), environments and groups.
alter table servers add column environment text not null default 'production'
  check (environment in ('production','staging','development'));

create table server_groups (
  id         uuid primary key default gen_random_uuid(),
  project_id uuid not null references projects on delete cascade,
  name       text not null,
  created_at timestamptz not null default now(),
  unique (project_id, name)
);

create table server_group_members (
  group_id  uuid not null references server_groups on delete cascade,
  server_id uuid not null references servers on delete cascade,
  primary key (group_id, server_id)
);
create index on server_group_members (server_id);

-- Latest reports from the agent. scan holds the raw facts plus the findings derived from them.
create table server_insight (
  server_id    uuid primary key references servers on delete cascade,
  inventory    jsonb,
  inventory_at timestamptz,
  scan         jsonb,
  scan_at      timestamptz
);
