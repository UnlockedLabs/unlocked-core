-- +goose Up
-- Provider Platforms' tables and the LMS jobs' cron_jobs and runnable_tasks
-- move into their own schema, and its queries run as the provider role, which
-- can write only that schema and read only core's users (EN-199). Goose runs
-- this in one transaction, so a failure leaves the database as it was.
--
-- SET SCHEMA keeps each table's rows, indexes, constraints, owned sequences
-- and column defaults, and foreign keys keep working across schemas.
--
-- Each move holds a brief exclusive lock on its table. If a long query holds
-- one first, this gives up instead of queueing every later query behind it;
-- the transaction rolls back and the migration can run again.
SET LOCAL lock_timeout = '5s';

CREATE SCHEMA provider;

ALTER TABLE public.provider_platforms SET SCHEMA provider;
ALTER TABLE public.provider_user_mappings SET SCHEMA provider;
ALTER TABLE public.oidc_clients SET SCHEMA provider;
ALTER TABLE public.courses SET SCHEMA provider;
ALTER TABLE public.user_enrollments SET SCHEMA provider;
ALTER TABLE public.milestones SET SCHEMA provider;
ALTER TABLE public.outcomes SET SCHEMA provider;
ALTER TABLE public.activities SET SCHEMA provider;
ALTER TABLE public.user_course_activity_totals SET SCHEMA provider;
ALTER TABLE public.cron_jobs SET SCHEMA provider;
ALTER TABLE public.runnable_tasks SET SCHEMA provider;

-- The activity jobs CALL these unqualified, so they're found through the
-- provider role's search_path, and the tables in their bodies resolve there
-- too.
ALTER PROCEDURE public.insert_daily_activity_canvas SET SCHEMA provider;
ALTER PROCEDURE public.insert_daily_activity_brightspace SET SCHEMA provider;
ALTER PROCEDURE public.insert_daily_activity_kolibri SET SCHEMA provider;

-- The role can't log in: the app connects as its own user and switches to
-- provider with SET ROLE, so no new password is needed. Roles belong to the
-- whole Postgres server, so it may already exist. This assumes one Unlocked
-- database per server: a second one with a different app user fails at the
-- GRANT below, and its Down can't drop a role another database uses.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'provider') THEN
        CREATE ROLE provider NOLOGIN;
    END IF;
END
$$;
-- +goose StatementEnd
GRANT provider TO CURRENT_USER;

GRANT USAGE ON SCHEMA provider TO provider;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA provider TO provider;
GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA provider TO provider;
-- Tables and sequences later migrations add here get the same grants.
ALTER DEFAULT PRIVILEGES IN SCHEMA provider GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO provider;
ALTER DEFAULT PRIVILEGES IN SCHEMA provider GRANT USAGE, SELECT, UPDATE ON SEQUENCES TO provider;

GRANT USAGE ON SCHEMA public TO provider;
GRANT SELECT ON public.users TO provider;

-- +goose Down
-- Down runs in its own transaction and takes the same locks moving back.
SET LOCAL lock_timeout = '5s';

ALTER DEFAULT PRIVILEGES IN SCHEMA provider REVOKE SELECT, INSERT, UPDATE, DELETE ON TABLES FROM provider;
ALTER DEFAULT PRIVILEGES IN SCHEMA provider REVOKE USAGE, SELECT, UPDATE ON SEQUENCES FROM provider;

ALTER PROCEDURE provider.insert_daily_activity_canvas SET SCHEMA public;
ALTER PROCEDURE provider.insert_daily_activity_brightspace SET SCHEMA public;
ALTER PROCEDURE provider.insert_daily_activity_kolibri SET SCHEMA public;

ALTER TABLE provider.provider_platforms SET SCHEMA public;
ALTER TABLE provider.provider_user_mappings SET SCHEMA public;
ALTER TABLE provider.oidc_clients SET SCHEMA public;
ALTER TABLE provider.courses SET SCHEMA public;
ALTER TABLE provider.user_enrollments SET SCHEMA public;
ALTER TABLE provider.milestones SET SCHEMA public;
ALTER TABLE provider.outcomes SET SCHEMA public;
ALTER TABLE provider.activities SET SCHEMA public;
ALTER TABLE provider.user_course_activity_totals SET SCHEMA public;
ALTER TABLE provider.cron_jobs SET SCHEMA public;
ALTER TABLE provider.runnable_tasks SET SCHEMA public;

DROP SCHEMA provider;

-- DROP OWNED revokes every grant the role holds in this database.
DROP OWNED BY provider;
REVOKE provider FROM CURRENT_USER;
DROP ROLE provider;
