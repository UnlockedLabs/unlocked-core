-- +goose Up
-- Learning Record's table moves into its own schema, and its queries run as
-- the learning_record role, which can write only that schema, read core's
-- facilities, and add audit rows to core's user_account_history (EN-286).
-- Goose runs this in one transaction, so a failure leaves the database as it
-- was.
--
-- SET SCHEMA keeps the table's rows, indexes, constraints, owned sequence and
-- column defaults, and foreign keys keep working across schemas.
--
-- The move holds a brief exclusive lock on the table. If a long query holds
-- one first, this gives up instead of queueing every later query behind it;
-- the transaction rolls back and the migration can run again.
SET LOCAL lock_timeout = '5s';

CREATE SCHEMA learning_record;

ALTER TABLE public.learning_record_entries SET SCHEMA learning_record;

-- The role can't log in: the app connects as its own user and switches to
-- learning_record with SET ROLE, so no new password is needed. Roles belong to
-- the whole Postgres server, so it may already exist. This assumes one
-- Unlocked database per server: a second one with a different app user fails
-- at the GRANT below, and its Down can't drop a role another database uses.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'learning_record') THEN
        CREATE ROLE learning_record NOLOGIN;
    END IF;
END
$$;
-- +goose StatementEnd
GRANT learning_record TO CURRENT_USER;

GRANT USAGE ON SCHEMA learning_record TO learning_record;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA learning_record TO learning_record;
GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA learning_record TO learning_record;
-- Tables and sequences later migrations add here get the same grants.
ALTER DEFAULT PRIVILEGES IN SCHEMA learning_record GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO learning_record;
ALTER DEFAULT PRIVILEGES IN SCHEMA learning_record GRANT USAGE, SELECT, UPDATE ON SEQUENCES TO learning_record;

-- Core's tables: facilities for the achievement-location picker and the
-- facility_name join, and user_account_history for the audit row an entry
-- delete adds in the same transaction. Its id default calls nextval, which
-- needs USAGE on the sequence.
GRANT USAGE ON SCHEMA public TO learning_record;
GRANT SELECT ON public.facilities TO learning_record;
GRANT INSERT ON public.user_account_history TO learning_record;
GRANT USAGE ON SEQUENCE public.user_account_history_id_seq TO learning_record;

-- +goose Down
-- Down runs in its own transaction and takes the same lock moving back.
SET LOCAL lock_timeout = '5s';

ALTER DEFAULT PRIVILEGES IN SCHEMA learning_record REVOKE SELECT, INSERT, UPDATE, DELETE ON TABLES FROM learning_record;
ALTER DEFAULT PRIVILEGES IN SCHEMA learning_record REVOKE USAGE, SELECT, UPDATE ON SEQUENCES FROM learning_record;

ALTER TABLE learning_record.learning_record_entries SET SCHEMA public;

DROP SCHEMA learning_record;

-- DROP OWNED revokes every grant the role holds in this database.
DROP OWNED BY learning_record;
REVOKE learning_record FROM CURRENT_USER;
DROP ROLE learning_record;
