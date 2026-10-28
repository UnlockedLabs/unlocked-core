-- +goose Up
-- Open Content's tables move into their own schema, and its queries run as the
-- open_content role, which can write only that schema and read only core's
-- users and facilities (EN-198). Goose runs this in one transaction, so a
-- failure leaves the database as it was.
--
-- SET SCHEMA keeps each table's rows, indexes, constraints, owned sequences
-- and column defaults, and foreign keys keep working across schemas.
CREATE SCHEMA open_content;

ALTER TABLE public.open_content_providers SET SCHEMA open_content;
ALTER TABLE public.open_content_urls SET SCHEMA open_content;
ALTER TABLE public.open_content_activities SET SCHEMA open_content;
ALTER TABLE public.open_content_favorites SET SCHEMA open_content;
ALTER TABLE public.open_content_tags SET SCHEMA open_content;
ALTER TABLE public.tags SET SCHEMA open_content;
ALTER TABLE public.libraries SET SCHEMA open_content;
ALTER TABLE public.facility_visibility_statuses SET SCHEMA open_content;
ALTER TABLE public.helpful_links SET SCHEMA open_content;
ALTER TABLE public.videos SET SCHEMA open_content;
ALTER TABLE public.video_download_attempts SET SCHEMA open_content;

-- The role can't log in: the app connects as its own user and switches to
-- open_content with SET ROLE, so no new password is needed. Roles belong to
-- the whole Postgres server, so it may already exist. This assumes one
-- Unlocked database per server: a second one with a different app user fails
-- at the GRANT below, and its Down can't drop a role another database uses.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'open_content') THEN
        CREATE ROLE open_content NOLOGIN;
    END IF;
END
$$;
-- +goose StatementEnd
GRANT open_content TO CURRENT_USER;

GRANT USAGE ON SCHEMA open_content TO open_content;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA open_content TO open_content;
GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA open_content TO open_content;
-- Tables and sequences later migrations add here get the same grants.
ALTER DEFAULT PRIVILEGES IN SCHEMA open_content GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO open_content;
ALTER DEFAULT PRIVILEGES IN SCHEMA open_content GRANT USAGE, SELECT, UPDATE ON SEQUENCES TO open_content;

GRANT USAGE ON SCHEMA public TO open_content;
GRANT SELECT ON public.users, public.facilities TO open_content;

-- +goose Down
ALTER DEFAULT PRIVILEGES IN SCHEMA open_content REVOKE SELECT, INSERT, UPDATE, DELETE ON TABLES FROM open_content;
ALTER DEFAULT PRIVILEGES IN SCHEMA open_content REVOKE USAGE, SELECT, UPDATE ON SEQUENCES FROM open_content;

ALTER TABLE open_content.open_content_providers SET SCHEMA public;
ALTER TABLE open_content.open_content_urls SET SCHEMA public;
ALTER TABLE open_content.open_content_activities SET SCHEMA public;
ALTER TABLE open_content.open_content_favorites SET SCHEMA public;
ALTER TABLE open_content.open_content_tags SET SCHEMA public;
ALTER TABLE open_content.tags SET SCHEMA public;
ALTER TABLE open_content.libraries SET SCHEMA public;
ALTER TABLE open_content.facility_visibility_statuses SET SCHEMA public;
ALTER TABLE open_content.helpful_links SET SCHEMA public;
ALTER TABLE open_content.videos SET SCHEMA public;
ALTER TABLE open_content.video_download_attempts SET SCHEMA public;

DROP SCHEMA open_content;

-- DROP OWNED revokes every grant the role holds in this database.
DROP OWNED BY open_content;
REVOKE open_content FROM CURRENT_USER;
DROP ROLE open_content;
