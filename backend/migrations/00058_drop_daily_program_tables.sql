-- +goose Up
-- +goose StatementBegin
DELETE FROM public.runnable_tasks WHERE job_id IN (
    SELECT id FROM public.cron_jobs WHERE name = 'daily_prog_history'
);
DELETE FROM public.cron_jobs WHERE name = 'daily_prog_history';
DROP TABLE IF EXISTS public.daily_programs_facilities_history;
DROP TABLE IF EXISTS public.daily_program_facilities_history;
DROP TABLE IF EXISTS public.daily_program_facility_history;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS public.daily_programs_facilities_history (
    date DATE NOT NULL DEFAULT CURRENT_DATE,
    total_programs INTEGER NOT NULL,
    total_active_programs INTEGER NOT NULL,
    total_archived_programs INTEGER NOT NULL,
    total_enrollments INTEGER NOT NULL,
    total_completions INTEGER NOT NULL,
    total_program_offerings INTEGER NOT NULL,
    total_facilities INTEGER NOT NULL,
    total_attendances_marked INTEGER NOT NULL,
    total_students_present INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_daily_programs_facilities_history_date ON public.daily_programs_facilities_history USING btree (date DESC);

CREATE TABLE IF NOT EXISTS public.daily_program_facilities_history (
    date DATE NOT NULL DEFAULT CURRENT_DATE,
    program_id INTEGER NOT NULL,
    total_active_facilities INTEGER NOT NULL,
    total_enrollments INTEGER NOT NULL,
    total_completions INTEGER NOT NULL,
    total_active_enrollments INTEGER NOT NULL,
    total_classes INTEGER NOT NULL,
    total_archived_classes INTEGER NOT NULL,
    total_attendances_marked INTEGER NOT NULL,
    total_students_present INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_daily_program_facilities_history_date_program_id ON public.daily_program_facilities_history USING btree (date DESC, program_id);

CREATE TABLE IF NOT EXISTS public.daily_program_facility_history (
    date DATE NOT NULL DEFAULT CURRENT_DATE,
    program_id INTEGER NOT NULL,
    facility_id INTEGER NOT NULL,
    total_enrollments INTEGER NOT NULL,
    total_completions INTEGER NOT NULL,
    total_active_enrollments INTEGER NOT NULL,
    total_classes INTEGER NOT NULL,
    total_archived_classes INTEGER NOT NULL,
    total_attendances_marked INTEGER NOT NULL,
    total_students_present INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_daily_program_facility_history_history_date_program_id_facility_id ON public.daily_program_facility_history USING btree (date DESC, program_id, facility_id);

-- cron_jobs.id is text with no default; the app makes it a UUID.
INSERT INTO public.cron_jobs (id, name, schedule, category, created_at, updated_at) VALUES
(gen_random_uuid()::text, 'daily_prog_history', '0 22 * * *', 3, NOW(), NOW());
-- +goose StatementEnd
