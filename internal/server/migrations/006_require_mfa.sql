-- Organizations and teams can require two-factor authentication. A member it applies to who hasn't
-- turned 2FA on is blocked from the organization (projectAccess/orgAccess) until they do.
alter table organizations add column require_mfa boolean not null default false;
alter table teams add column require_mfa boolean not null default false;

create function mfa_required(org uuid, usr uuid) returns boolean language sql stable as $$
  select coalesce((select require_mfa from organizations where id = org), false) or exists(
    select 1 from team_members tm join teams t on t.id = tm.team_id
    where tm.user_id = usr and t.org_id = org and t.require_mfa)
$$;

create function mfa_blocked(org uuid, usr uuid) returns boolean language sql stable as $$
  select mfa_required(org, usr) and exists(select 1 from users where id = usr and totp_enabled_at is null)
$$;
