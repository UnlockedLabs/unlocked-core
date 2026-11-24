-- +goose Up
-- +goose StatementBegin
ALTER TABLE cron_jobs ADD COLUMN category INTEGER DEFAULT 0;
UPDATE cron_jobs SET category = 1 WHERE name IN ('get_milestones', 'get_courses', 'get_activity');
UPDATE cron_jobs SET category = 2 WHERE name IN ('scrape_kiwix', 'retry_video_downloads', 'sync_video_metadata');
UPDATE cron_jobs SET category = 3 WHERE name IN ('daily_prog_history');

CREATE TABLE public.change_log_entries (
    id SERIAL NOT NULL PRIMARY KEY,
    table_name VARCHAR(255),
    parent_ref_id INT,
    field_name VARCHAR(150),
    old_value VARCHAR(255),
    new_value VARCHAR(255),
    created_at timestamp with time zone,
    user_id INTEGER NOT NULL,

    FOREIGN KEY (user_id) REFERENCES public.users(id) ON UPDATE CASCADE ON DELETE CASCADE
);
CREATE INDEX idx_change_log_table_name_parent_ref_id on public.change_log_entries (table_name, parent_ref_id);
CREATE INDEX idx_change_log_table_name_parent_ref_id_field_name on public.change_log_entries (table_name, parent_ref_id, field_name);

CREATE UNIQUE INDEX idx_unique_facility_program_active ON public.facilities_programs (program_id, facility_id) WHERE (deleted_at IS NULL);
ALTER TABLE public.program_class_enrollments ADD COLUMN change_reason VARCHAR(255);

ALTER TABLE public.program_class_event_overrides ADD COLUMN reason VARCHAR(255);
ALTER TABLE public.program_class_event_overrides RENAME COLUMN location TO room;
ALTER TABLE public.program_class_event_overrides ADD CONSTRAINT unique_event_id_rrule UNIQUE (event_id, override_rrule);
-- +goose StatementEnd

-- +goose Down                                   
-- +goose StatementBegin
ALTER TABLE cron_jobs DROP COLUMN IF EXISTS category;
DROP TABLE IF EXISTS public.change_log_entries CASCADE;
DROP INDEX IF EXISTS idx_unique_facility_program_active CASCADE;
ALTER TABLE public.program_class_enrollments DROP COLUMN IF EXISTS change_reason;

ALTER TABLE public.program_class_event_overrides DROP COLUMN reason;
ALTER TABLE public.program_class_event_overrides RENAME COLUMN room TO location;
ALTER TABLE public.program_class_event_overrides DROP CONSTRAINT IF EXISTS unique_event_id_rrule;
-- +goose StatementEnd
