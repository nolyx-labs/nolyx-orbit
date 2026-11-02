-- Custom roles cap a member's organization role per area of every project (see roleAreas in roles.go).
-- They only narrow access: a level above the member's role has no effect, and team grants still apply.
create table org_roles (
  id          uuid primary key default gen_random_uuid(),
  org_id      uuid not null references organizations on delete cascade,
  name        text not null,
  permissions jsonb not null default '{}', -- area → none | viewer | member | admin
  created_at  timestamptz not null default now(),
  unique (org_id, name)
);

alter table memberships add column custom_role_id uuid references org_roles on delete set null;
