-- Uptime monitors, checked from the control plane.
create table monitors (
  id               uuid primary key default gen_random_uuid(),
  project_id       uuid not null references projects on delete cascade,
  name             text not null,
  type             text not null check (type in ('http','tcp')),
  target           text not null, -- http: URL; tcp: host:port
  interval_seconds int not null default 60 check (interval_seconds between 30 and 3600),
  timeout_ms       int not null default 10000 check (timeout_ms between 1000 and 30000),
  expected_status  text not null default '2xx', -- http: e.g. "2xx", "200", "200-299,301"
  keyword          text not null default '',    -- http: the body must contain it
  server_id        uuid references servers on delete set null, -- where it runs, for maintenance windows
  paused           boolean not null default false,
  status           text not null default 'pending' check (status in ('pending','up','down')),
  fail_streak      int not null default 0,
  last_checked_at  timestamptz,
  next_check_at    timestamptz not null default now(),
  last_latency_ms  int,
  last_error       text not null default '',
  cert_expires_at  timestamptz,
  created_at       timestamptz not null default now()
);
create index on monitors (next_check_at) where not paused;

create table monitor_checks (
  monitor_id  uuid not null references monitors on delete cascade,
  ts          timestamptz not null default now(),
  ok          boolean not null,
  latency_ms  int,
  status_code int,
  error       text not null default ''
);
create index on monitor_checks (monitor_id, ts);

-- One row per time a monitor was down; uptime is computed from these.
create table monitor_outages (
  id         bigserial primary key,
  monitor_id uuid not null references monitors on delete cascade,
  started_at timestamptz not null,
  ended_at   timestamptz,
  error      text not null default ''
);
create index on monitor_outages (monitor_id, started_at);
create unique index on monitor_outages (monitor_id) where ended_at is null;

-- Percent of time up since `since` (or since the monitor was created, if later). Null for an empty window.
create function monitor_uptime(mid uuid, since timestamptz) returns float8 language sql stable as $$
  select round((100 * (1 - coalesce(sum(extract(epoch from coalesce(o.ended_at, now()) - greatest(o.started_at, w.start)))
      filter (where o.id is not null), 0)
    / nullif(extract(epoch from now() - w.start), 0)))::numeric, 3)::float8
  from (select greatest(since, created_at) as start from monitors where id = mid) w
  left join monitor_outages o on o.monitor_id = mid and coalesce(o.ended_at, now()) > w.start
  group by w.start
$$;

alter table alert_rules drop constraint alert_rules_metric_check;
alter table alert_rules add constraint alert_rules_metric_check check (metric in
  ('cpu','memory','disk','offline','io','replication_lag','db_connections','db_down','error_rate','container_restarts',
   'deployment_failed','monitor_down'));

-- Public status pages, modelled on Atlassian Statuspage.
create table status_pages (
  id          uuid primary key default gen_random_uuid(),
  project_id  uuid not null references projects on delete cascade,
  name        text not null,
  slug        text not null unique check (slug ~ '^[a-z0-9][a-z0-9-]{1,47}$'),
  domain      text unique, -- custom domain such as status.example.com
  public      boolean not null default true, -- false: only signed-in organization members
  description text not null default '',
  support_url text not null default '',
  brand_color text not null default '#0b0b0c' check (brand_color ~ '^#[0-9a-fA-F]{6}$'),
  logo        bytea,
  logo_type   text,
  created_at  timestamptz not null default now()
);

create table status_components (
  id          uuid primary key default gen_random_uuid(),
  page_id     uuid not null references status_pages on delete cascade,
  group_name  text not null default '',
  name        text not null,
  description text not null default '',
  position    int not null default 0,
  source_type text not null default 'manual' check (source_type in ('manual','monitor','server','app')),
  source_id   uuid,
  -- manual components: set by people; others: derived from the source. Incidents and maintenance override both.
  manual_status text not null default 'operational',
  status      text not null default 'operational', -- effective status, kept current by the worker
  created_at  timestamptz not null default now(),
  check (manual_status in ('operational','degraded_performance','partial_outage','major_outage','under_maintenance')),
  check (status in ('operational','degraded_performance','partial_outage','major_outage','under_maintenance'))
);

-- Effective status over time, for the 90-day uptime bars.
create table status_component_history (
  component_id uuid not null references status_components on delete cascade,
  status       text not null,
  started_at   timestamptz not null,
  ended_at     timestamptz
);
create index on status_component_history (component_id, started_at);
create unique index on status_component_history (component_id) where ended_at is null;

create table status_incidents (
  id              uuid primary key default gen_random_uuid(),
  page_id         uuid not null references status_pages on delete cascade,
  kind            text not null check (kind in ('incident','maintenance')),
  name            text not null,
  impact          text not null default 'minor' check (impact in ('none','minor','major','critical','maintenance')),
  status          text not null check (status in
    ('investigating','identified','monitoring','resolved','scheduled','in_progress','verifying','completed')),
  scheduled_for   timestamptz,
  scheduled_until timestamptz,
  auto_transition boolean not null default true, -- maintenance starts and completes on schedule
  created_at      timestamptz not null default now(),
  resolved_at     timestamptz
);
create index on status_incidents (page_id, created_at);

create table status_incident_components (
  incident_id  uuid not null references status_incidents on delete cascade,
  component_id uuid not null references status_components on delete cascade,
  status       text not null, -- what the component shows while the incident is open
  primary key (incident_id, component_id)
);

create table status_incident_updates (
  id          bigserial primary key,
  incident_id uuid not null references status_incidents on delete cascade,
  status      text not null,
  message     text not null,
  created_by  uuid references users on delete set null,
  created_at  timestamptz not null default now()
);
create index on status_incident_updates (incident_id, created_at);

create table status_subscribers (
  id           uuid primary key default gen_random_uuid(),
  page_id      uuid not null references status_pages on delete cascade,
  email        text,
  webhook_url  text,
  token        text not null unique, -- in confirm and unsubscribe links
  confirmed_at timestamptz,
  created_at   timestamptz not null default now(),
  check ((email is null) <> (webhook_url is null)),
  unique (page_id, email),
  unique (page_id, webhook_url)
);
