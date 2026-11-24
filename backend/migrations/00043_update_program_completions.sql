-- +goose Up
-- +goose StatementBegin
ALTER TABLE public.program_section_enrollments RENAME COLUMN section_id TO class_id;
ALTER TABLE public.program_section_events RENAME COLUMN section_id TO class_id;
ALTER TABLE public.program_section_enrollments RENAME TO program_class_enrollments;
ALTER TABLE public.program_section_event_attendance RENAME TO program_class_event_attendance;
ALTER TABLE public.program_section_event_overrides RENAME TO program_class_event_overrides;
ALTER TABLE public.program_section_events RENAME TO program_class_events;
ALTER TABLE public.program_sections RENAME TO program_classes;
ALTER TABLE public.programs_sections_history RENAME TO program_classes_history;
ALTER TABLE public.program_completions RENAME COLUMN program_section_start_dt TO program_class_start_dt;
ALTER TABLE public.program_completions RENAME COLUMN program_section_name TO program_class_name;
ALTER TABLE public.program_completions RENAME COLUMN program_section_id TO program_class_id;
ALTER TABLE public.program_class_event_attendance ADD COLUMN attendance_status VARCHAR(27);
DROP TRIGGER IF EXISTS sql_trigger_programs_update ON public.programs;
DROP TRIGGER IF EXISTS sql_trigger_program_sections_update ON public.program_sections;
DROP FUNCTION IF EXISTS public.log_programs_sections_updates() CASCADE;

CREATE OR REPLACE FUNCTION public.log_program_classes_updates()
RETURNS TRIGGER AS $$
BEGIN
    INSERT INTO public.program_classes_history (parent_ref_d, table_name, before_update, after_update)
    VALUES (
        OLD.id,
        TG_TABLE_NAME,
        row_to_json(OLD),
        row_to_json(NEW)
    );
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER sql_trigger_programs_update
    AFTER UPDATE ON public.programs
FOR EACH ROW
    EXECUTE FUNCTION public.log_program_classes_updates();

CREATE TRIGGER sql_trigger_program_classes_update
    AFTER UPDATE ON public.program_classes
FOR EACH ROW
    EXECUTE FUNCTION public.log_program_classes_updates();
ALTER TABLE public.program_completions ADD COLUMN user_id integer NOT NULL REFERENCES public.users(id) ON DELETE CASCADE ON UPDATE CASCADE;
ALTER TABLE public.program_completions ALTER COLUMN program_class_start_dt SET DATA TYPE timestamp with time zone USING to_timestamp(program_class_start_dt, 'YYYY-MM-DD HH24:MI:SS');

CREATE TABLE public.user_account_history (
        id SERIAL NOT NULL PRIMARY KEY,
        user_id INTEGER NOT NULL,
        admin_id INTEGER,
        action CHARACTER VARYING(255) NOT NULL,
        program_classes_history_id INTEGER,
        facility_id INTEGER,
        created_at timestamp with time zone,

        FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE ON UPDATE CASCADE,
		FOREIGN KEY (admin_id) REFERENCES public.users(id) ON DELETE CASCADE ON UPDATE CASCADE,
        FOREIGN KEY (program_classes_history_id) REFERENCES public.program_classes_history(id) ON DELETE CASCADE ON UPDATE CASCADE,
        FOREIGN KEY (facility_id) REFERENCES public.facilities(id) ON DELETE CASCADE ON UPDATE CASCADE
);
CREATE INDEX idx_user_account_history_user_id ON public.user_account_history USING btree(user_id);
CREATE INDEX idx_user_account_history_admin_id ON public.user_account_history USING btree(admin_id);
CREATE INDEX idx_user_account_history_program_classes_history_id ON public.user_account_history USING btree(program_classes_history_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS sql_trigger_programs_update ON public.programs;
DROP TRIGGER IF EXISTS sql_trigger_program_classes_update ON public.program_classes;
DROP FUNCTION IF EXISTS public.log_program_classes_updates();
DROP TABLE IF EXISTS public.user_account_history;

CREATE OR REPLACE FUNCTION public.log_programs_sections_updates()
RETURNS TRIGGER AS $$
BEGIN
    INSERT INTO public.programs_sections_history (parent_ref_d, table_name, before_update, after_update)
    VALUES (
        OLD.id,
        TG_TABLE_NAME,
        row_to_json(OLD),
        row_to_json(NEW)
    );
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER sql_trigger_programs_update
    AFTER UPDATE ON public.programs
FOR EACH ROW
    EXECUTE FUNCTION public.log_programs_sections_updates();

ALTER TABLE public.program_class_enrollments RENAME COLUMN class_id TO section_id;
ALTER TABLE public.program_class_events RENAME COLUMN class_id TO section_id;
ALTER TABLE public.program_class_event_attendance DROP COLUMN attendance_status;

ALTER TABLE public.program_class_enrollments RENAME TO program_section_enrollments;
ALTER TABLE public.program_class_event_attendance RENAME TO program_section_event_attendance;
ALTER TABLE public.program_class_event_overrides RENAME TO program_section_event_overrides;
ALTER TABLE public.program_class_events RENAME TO program_section_events;
ALTER TABLE public.program_classes RENAME TO program_sections;
ALTER TABLE public.program_classes_history RENAME TO programs_sections_history;
ALTER TABLE public.program_completions DROP COLUMN user_id;
ALTER TABLE public.program_completions ALTER COLUMN program_class_start_dt SET DATA TYPE character varying(255);
ALTER TABLE public.program_completions RENAME COLUMN program_class_start_dt TO program_section_start_dt;
ALTER TABLE public.program_completions RENAME COLUMN program_class_name TO program_section_name;
ALTER TABLE public.program_completions RENAME COLUMN program_class_id TO program_section_id;

CREATE TRIGGER sql_trigger_program_sections_update
    AFTER UPDATE ON public.program_sections
FOR EACH ROW
    EXECUTE FUNCTION public.log_programs_sections_updates();
-- +goose StatementEnd
