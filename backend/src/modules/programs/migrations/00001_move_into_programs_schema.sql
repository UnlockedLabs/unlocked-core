-- +goose Up
-- Programs' tables move into their own schema, and its queries run as the
-- programs role, which can write only that schema, read core's users and
-- facilities, and add to core's audit tables (EN-200). Goose runs this in one
-- transaction, so a failure leaves the database as it was.
--
-- SET SCHEMA keeps each table's rows, indexes, constraints, triggers, owned
-- sequences and column defaults, and foreign keys keep working across schemas.
-- The enum types (class_status, credit_type, funding_type, program_type) stay
-- in public, as Open Content's did.
--
-- Each move holds a brief exclusive lock on its table. If a long query holds
-- one first, this gives up instead of queueing every later query behind it;
-- the transaction rolls back and the migration can run again.
SET LOCAL lock_timeout = '5s';

CREATE SCHEMA programs;

ALTER TABLE public.programs SET SCHEMA programs;
ALTER TABLE public.program_types SET SCHEMA programs;
ALTER TABLE public.program_credit_types SET SCHEMA programs;
ALTER TABLE public.facilities_programs SET SCHEMA programs;
ALTER TABLE public.program_classes SET SCHEMA programs;
ALTER TABLE public.program_class_credit_types SET SCHEMA programs;
ALTER TABLE public.program_class_cohorts SET SCHEMA programs;
ALTER TABLE public.program_class_events SET SCHEMA programs;
ALTER TABLE public.program_class_event_overrides SET SCHEMA programs;
ALTER TABLE public.program_class_event_attendance SET SCHEMA programs;
ALTER TABLE public.program_class_enrollments SET SCHEMA programs;
ALTER TABLE public.class_completions SET SCHEMA programs;
ALTER TABLE public.program_classes_history SET SCHEMA programs;
ALTER TABLE public.rooms SET SCHEMA programs;

-- The history trigger on programs, program_classes and program_class_cohorts
-- names its table with the schema, so it moves too and writes the moved table.
-- The triggers point at the function itself, so they follow it.
ALTER FUNCTION public.log_program_classes_updates() SET SCHEMA programs;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION programs.log_program_classes_updates()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    INSERT INTO programs.program_classes_history (parent_ref_id, table_name, before_update, after_update)
    VALUES (
        OLD.id,
        TG_TABLE_NAME,
        row_to_json(OLD),
        row_to_json(NEW)
    );
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- The role can't log in: the app connects as its own user and switches to
-- programs with SET ROLE, so no new password is needed. Roles belong to the
-- whole Postgres server, so it may already exist. This assumes one Unlocked
-- database per server: a second one with a different app user fails at the
-- GRANT below, and its Down can't drop a role another database uses.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'programs') THEN
        CREATE ROLE programs NOLOGIN;
    END IF;
END
$$;
-- +goose StatementEnd
GRANT programs TO CURRENT_USER;

GRANT USAGE ON SCHEMA programs TO programs;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA programs TO programs;
GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA programs TO programs;
-- Tables and sequences later migrations add here get the same grants.
ALTER DEFAULT PRIVILEGES IN SCHEMA programs GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO programs;
ALTER DEFAULT PRIVILEGES IN SCHEMA programs GRANT USAGE, SELECT, UPDATE ON SEQUENCES TO programs;

GRANT USAGE ON SCHEMA public TO programs;
GRANT SELECT ON public.users, public.facilities TO programs;
-- Programs writes core's audit trail in its own transactions (attendance,
-- enrollment and class changes), so it may read and add rows, never change
-- or remove them.
GRANT SELECT, INSERT ON public.change_log_entries, public.user_account_history TO programs;
GRANT USAGE ON SEQUENCE public.change_log_entries_id_seq, public.user_account_history_id_seq TO programs;

-- +goose Down
-- Moving the tables back takes the same locks, so it gives up the same way.
SET LOCAL lock_timeout = '5s';

ALTER DEFAULT PRIVILEGES IN SCHEMA programs REVOKE SELECT, INSERT, UPDATE, DELETE ON TABLES FROM programs;
ALTER DEFAULT PRIVILEGES IN SCHEMA programs REVOKE USAGE, SELECT, UPDATE ON SEQUENCES FROM programs;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION programs.log_program_classes_updates()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    INSERT INTO public.program_classes_history (parent_ref_id, table_name, before_update, after_update)
    VALUES (
        OLD.id,
        TG_TABLE_NAME,
        row_to_json(OLD),
        row_to_json(NEW)
    );
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION programs.log_program_classes_updates() SET SCHEMA public;

ALTER TABLE programs.programs SET SCHEMA public;
ALTER TABLE programs.program_types SET SCHEMA public;
ALTER TABLE programs.program_credit_types SET SCHEMA public;
ALTER TABLE programs.facilities_programs SET SCHEMA public;
ALTER TABLE programs.program_classes SET SCHEMA public;
ALTER TABLE programs.program_class_credit_types SET SCHEMA public;
ALTER TABLE programs.program_class_cohorts SET SCHEMA public;
ALTER TABLE programs.program_class_events SET SCHEMA public;
ALTER TABLE programs.program_class_event_overrides SET SCHEMA public;
ALTER TABLE programs.program_class_event_attendance SET SCHEMA public;
ALTER TABLE programs.program_class_enrollments SET SCHEMA public;
ALTER TABLE programs.class_completions SET SCHEMA public;
ALTER TABLE programs.program_classes_history SET SCHEMA public;
ALTER TABLE programs.rooms SET SCHEMA public;

DROP SCHEMA programs;

-- DROP OWNED revokes every grant the role holds in this database.
DROP OWNED BY programs;
REVOKE programs FROM CURRENT_USER;
DROP ROLE programs;
