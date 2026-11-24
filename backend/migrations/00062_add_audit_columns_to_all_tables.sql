-- +goose Up
-- +goose StatementBegin
ALTER TABLE public.users DROP CONSTRAINT users_facility_id_fkey;

ALTER TABLE public.users ADD CONSTRAINT users_facility_id_zero_or_positive CHECK (facility_id IS NULL OR facility_id >= 0);

CREATE UNIQUE INDEX facilities_id_nonzero_idx ON public.facilities(id) WHERE id > 0;

ALTER TABLE public.users ADD COLUMN create_user_id integer;
ALTER TABLE public.users ADD COLUMN update_user_id integer;

ALTER TABLE public.helpful_links ADD COLUMN create_user_id integer;
ALTER TABLE public.helpful_links ADD COLUMN update_user_id integer;

ALTER TABLE public.program_class_event_attendance ADD COLUMN create_user_id integer;
ALTER TABLE public.program_class_event_attendance ADD COLUMN update_user_id integer;

ALTER TABLE public.program_class_event_overrides ADD COLUMN create_user_id integer;
ALTER TABLE public.program_class_event_overrides ADD COLUMN update_user_id integer;

ALTER TABLE public.video_download_attempts ADD COLUMN create_user_id integer;
ALTER TABLE public.video_download_attempts ADD COLUMN update_user_id integer;

ALTER TABLE public.user_enrollments ADD COLUMN create_user_id integer;
ALTER TABLE public.user_enrollments ADD COLUMN update_user_id integer;

ALTER TABLE public.facilities ADD COLUMN create_user_id integer;
ALTER TABLE public.facilities ADD COLUMN update_user_id integer;

ALTER TABLE public.provider_platforms ADD COLUMN create_user_id integer;
ALTER TABLE public.provider_platforms ADD COLUMN update_user_id integer;

ALTER TABLE public.provider_user_mappings ADD COLUMN create_user_id integer;
ALTER TABLE public.provider_user_mappings ADD COLUMN update_user_id integer;

ALTER TABLE public.videos ADD COLUMN create_user_id integer;
ALTER TABLE public.videos ADD COLUMN update_user_id integer;

ALTER TABLE public.program_class_events ADD COLUMN create_user_id integer;
ALTER TABLE public.program_class_events ADD COLUMN update_user_id integer;

ALTER TABLE public.courses ADD COLUMN create_user_id integer;
ALTER TABLE public.courses ADD COLUMN update_user_id integer;

ALTER TABLE public.milestones ADD COLUMN create_user_id integer;
ALTER TABLE public.milestones ADD COLUMN update_user_id integer;

ALTER TABLE public.oidc_clients ADD COLUMN create_user_id integer;
ALTER TABLE public.oidc_clients ADD COLUMN update_user_id integer;

ALTER TABLE public.open_content_providers ADD COLUMN create_user_id integer;
ALTER TABLE public.open_content_providers ADD COLUMN update_user_id integer;

ALTER TABLE public.outcomes ADD COLUMN create_user_id integer;
ALTER TABLE public.outcomes ADD COLUMN update_user_id integer;

ALTER TABLE public.facilities_programs ADD COLUMN create_user_id integer;
ALTER TABLE public.facilities_programs ADD COLUMN update_user_id integer;

ALTER TABLE public.program_class_enrollments ADD COLUMN create_user_id integer;
ALTER TABLE public.program_class_enrollments ADD COLUMN update_user_id integer;

ALTER TABLE public.program_completions ADD COLUMN create_user_id integer;
ALTER TABLE public.program_completions ADD COLUMN update_user_id integer;

ALTER TABLE public.libraries ADD COLUMN create_user_id integer;
ALTER TABLE public.libraries ADD COLUMN update_user_id integer;

ALTER TABLE public.feature_flags ADD COLUMN create_user_id integer;
ALTER TABLE public.feature_flags ADD COLUMN update_user_id integer;

ALTER TABLE public.page_feature_flags ADD COLUMN create_user_id integer;
ALTER TABLE public.page_feature_flags ADD COLUMN update_user_id integer;

ALTER TABLE public.facility_visibility_statuses ADD COLUMN create_user_id integer;
ALTER TABLE public.facility_visibility_statuses ADD COLUMN update_user_id integer;

-- created system_batch user with a fixed high ID to avoid conflicts
-- Using ID 99999 to ensure it doesn't conflict with existing users
INSERT INTO public.users (id, username, name_first, name_last, email, role, facility_id, created_at, updated_at, create_user_id, update_user_id)
SELECT
    99999,
    'system_batch',
    'System',
    'Batch',
    'system_batch@unlocked.v2',
    'system_admin',
    0,
    NOW(),
    NOW(),
    NULL,  -- system_batch user created by system, so no create_user_id
    NULL   -- system_batch user created by system, so no update_user_id
WHERE NOT EXISTS (SELECT 1 FROM public.users WHERE username = 'system_batch');

UPDATE public.users SET create_user_id = (SELECT id FROM public.users WHERE username = 'system_batch') WHERE create_user_id IS NULL AND username != 'system_batch';
UPDATE public.facilities SET create_user_id = (SELECT id FROM public.users WHERE username = 'system_batch') WHERE create_user_id IS NULL;
UPDATE public.helpful_links SET create_user_id = (SELECT id FROM public.users WHERE username = 'system_batch') WHERE create_user_id IS NULL;
UPDATE public.program_class_event_attendance SET create_user_id = (SELECT id FROM public.users WHERE username = 'system_batch') WHERE create_user_id IS NULL;
UPDATE public.program_class_event_overrides SET create_user_id = (SELECT id FROM public.users WHERE username = 'system_batch') WHERE create_user_id IS NULL;
UPDATE public.video_download_attempts SET create_user_id = (SELECT id FROM public.users WHERE username = 'system_batch') WHERE create_user_id IS NULL;
UPDATE public.user_enrollments SET create_user_id = (SELECT id FROM public.users WHERE username = 'system_batch') WHERE create_user_id IS NULL;
UPDATE public.provider_platforms SET create_user_id = (SELECT id FROM public.users WHERE username = 'system_batch') WHERE create_user_id IS NULL;
UPDATE public.provider_user_mappings SET create_user_id = (SELECT id FROM public.users WHERE username = 'system_batch') WHERE create_user_id IS NULL;
UPDATE public.videos SET create_user_id = (SELECT id FROM public.users WHERE username = 'system_batch') WHERE create_user_id IS NULL;
UPDATE public.program_class_events SET create_user_id = (SELECT id FROM public.users WHERE username = 'system_batch') WHERE create_user_id IS NULL;
UPDATE public.courses SET create_user_id = (SELECT id FROM public.users WHERE username = 'system_batch') WHERE create_user_id IS NULL;
UPDATE public.milestones SET create_user_id = (SELECT id FROM public.users WHERE username = 'system_batch') WHERE create_user_id IS NULL;
UPDATE public.oidc_clients SET create_user_id = (SELECT id FROM public.users WHERE username = 'system_batch') WHERE create_user_id IS NULL;
UPDATE public.open_content_providers SET create_user_id = (SELECT id FROM public.users WHERE username = 'system_batch') WHERE create_user_id IS NULL;
UPDATE public.outcomes SET create_user_id = (SELECT id FROM users WHERE username = 'system_batch') WHERE create_user_id IS NULL;
UPDATE public.facilities_programs SET create_user_id = (SELECT id FROM public.users WHERE username = 'system_batch') WHERE create_user_id IS NULL;
UPDATE public.program_class_enrollments SET create_user_id = (SELECT id FROM public.users WHERE username = 'system_batch') WHERE create_user_id IS NULL;
UPDATE public.program_completions SET create_user_id = (SELECT id FROM public.users WHERE username = 'system_batch') WHERE create_user_id IS NULL;
UPDATE public.libraries SET create_user_id = (SELECT id FROM public.users WHERE username = 'system_batch') WHERE create_user_id IS NULL;
UPDATE public.feature_flags SET create_user_id = (SELECT id FROM public.users WHERE username = 'system_batch') WHERE create_user_id IS NULL;
UPDATE public.page_feature_flags SET create_user_id = (SELECT id FROM public.users WHERE username = 'system_batch') WHERE create_user_id IS NULL;
UPDATE public.facility_visibility_statuses SET create_user_id = (SELECT id FROM public.users WHERE username = 'system_batch') WHERE create_user_id IS NULL;

ALTER TABLE public.users ADD CONSTRAINT fk_users_create_user_id FOREIGN KEY (create_user_id) REFERENCES public.users(id) ON DELETE SET NULL;
ALTER TABLE public.users ADD CONSTRAINT fk_users_update_user_id FOREIGN KEY (update_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE public.helpful_links ADD CONSTRAINT fk_helpful_links_create_user_id FOREIGN KEY (create_user_id) REFERENCES public.users(id) ON DELETE SET NULL;
ALTER TABLE public.helpful_links ADD CONSTRAINT fk_helpful_links_update_user_id FOREIGN KEY (update_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE public.program_class_event_attendance ADD CONSTRAINT fk_program_class_event_attendance_create_user_id FOREIGN KEY (create_user_id) REFERENCES public.users(id) ON DELETE SET NULL;
ALTER TABLE public.program_class_event_attendance ADD CONSTRAINT fk_program_class_event_attendance_update_user_id FOREIGN KEY (update_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE public.program_class_event_overrides ADD CONSTRAINT fk_program_class_event_overrides_create_user_id FOREIGN KEY (create_user_id) REFERENCES public.users(id) ON DELETE SET NULL;
ALTER TABLE public.program_class_event_overrides ADD CONSTRAINT fk_program_class_event_overrides_update_user_id FOREIGN KEY (update_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE public.video_download_attempts ADD CONSTRAINT fk_video_download_attempts_create_user_id FOREIGN KEY (create_user_id) REFERENCES public.users(id) ON DELETE SET NULL;
ALTER TABLE public.video_download_attempts ADD CONSTRAINT fk_video_download_attempts_update_user_id FOREIGN KEY (update_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE public.user_enrollments ADD CONSTRAINT fk_user_enrollments_create_user_id FOREIGN KEY (create_user_id) REFERENCES public.users(id) ON DELETE SET NULL;
ALTER TABLE public.user_enrollments ADD CONSTRAINT fk_user_enrollments_update_user_id FOREIGN KEY (update_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE public.facilities ADD CONSTRAINT fk_facilities_create_user_id FOREIGN KEY (create_user_id) REFERENCES public.users(id) ON DELETE SET NULL;
ALTER TABLE public.facilities ADD CONSTRAINT fk_facilities_update_user_id FOREIGN KEY (update_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE public.provider_platforms ADD CONSTRAINT fk_provider_platforms_create_user_id FOREIGN KEY (create_user_id) REFERENCES public.users(id) ON DELETE SET NULL;
ALTER TABLE public.provider_platforms ADD CONSTRAINT fk_provider_platforms_update_user_id FOREIGN KEY (update_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE public.provider_user_mappings ADD CONSTRAINT fk_provider_user_mapping_create_user_id FOREIGN KEY (create_user_id) REFERENCES public.users(id) ON DELETE SET NULL;
ALTER TABLE public.provider_user_mappings ADD CONSTRAINT fk_provider_user_mapping_update_user_id FOREIGN KEY (update_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE public.videos ADD CONSTRAINT fk_video_create_user_id FOREIGN KEY (create_user_id) REFERENCES public.users(id) ON DELETE SET NULL;
ALTER TABLE public.videos ADD CONSTRAINT fk_video_update_user_id FOREIGN KEY (update_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE public.program_class_events ADD CONSTRAINT fk_program_class_events_create_user_id FOREIGN KEY (create_user_id) REFERENCES public.users(id) ON DELETE SET NULL;
ALTER TABLE public.program_class_events ADD CONSTRAINT fk_program_class_events_update_user_id FOREIGN KEY (update_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE public.courses ADD CONSTRAINT fk_courses_create_user_id FOREIGN KEY (create_user_id) REFERENCES public.users(id) ON DELETE SET NULL;
ALTER TABLE public.courses ADD CONSTRAINT fk_courses_update_user_id FOREIGN KEY (update_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE public.milestones ADD CONSTRAINT fk_milestones_create_user_id FOREIGN KEY (create_user_id) REFERENCES public.users(id) ON DELETE SET NULL;
ALTER TABLE public.milestones ADD CONSTRAINT fk_milestones_update_user_id FOREIGN KEY (update_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE public.oidc_clients ADD CONSTRAINT fk_oidc_clients_create_user_id FOREIGN KEY (create_user_id) REFERENCES public.users(id) ON DELETE SET NULL;
ALTER TABLE public.oidc_clients ADD CONSTRAINT fk_oidc_clients_update_user_id FOREIGN KEY (update_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE public.open_content_providers ADD CONSTRAINT fk_open_content_providers_create_user_id FOREIGN KEY (create_user_id) REFERENCES public.users(id) ON DELETE SET NULL;
ALTER TABLE public.open_content_providers ADD CONSTRAINT fk_open_content_providers_update_user_id FOREIGN KEY (update_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE public.outcomes ADD CONSTRAINT fk_outcomes_create_user_id FOREIGN KEY (create_user_id) REFERENCES public.users(id) ON DELETE SET NULL;
ALTER TABLE public.outcomes ADD CONSTRAINT fk_outcomes_update_user_id FOREIGN KEY (update_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE public.facilities_programs ADD CONSTRAINT fk_facilities_programs_create_user_id FOREIGN KEY (create_user_id) REFERENCES public.users(id) ON DELETE SET NULL;
ALTER TABLE public.facilities_programs ADD CONSTRAINT fk_facilities_programs_update_user_id FOREIGN KEY (update_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE public.program_class_enrollments ADD CONSTRAINT fk_program_class_enrollments_create_user_id FOREIGN KEY (create_user_id) REFERENCES public.users(id) ON DELETE SET NULL;
ALTER TABLE public.program_class_enrollments ADD CONSTRAINT fk_program_class_enrollments_update_user_id FOREIGN KEY (update_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE public.program_completions ADD CONSTRAINT fk_program_completions_create_user_id FOREIGN KEY (create_user_id) REFERENCES public.users(id) ON DELETE SET NULL;
ALTER TABLE public.program_completions ADD CONSTRAINT fk_program_completions_update_user_id FOREIGN KEY (update_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE public.libraries ADD CONSTRAINT fk_libraries_create_user_id FOREIGN KEY (create_user_id) REFERENCES public.users(id) ON DELETE SET NULL;
ALTER TABLE public.libraries ADD CONSTRAINT fk_libraries_update_user_id FOREIGN KEY (update_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE public.feature_flags ADD CONSTRAINT fk_feature_flags_create_user_id FOREIGN KEY (create_user_id) REFERENCES public.users(id) ON DELETE SET NULL;
ALTER TABLE public.feature_flags ADD CONSTRAINT fk_feature_flags_update_user_id FOREIGN KEY (update_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE public.page_feature_flags ADD CONSTRAINT fk_page_feature_flags_create_user_id FOREIGN KEY (create_user_id) REFERENCES public.users(id) ON DELETE SET NULL;
ALTER TABLE public.page_feature_flags ADD CONSTRAINT fk_page_feature_flags_update_user_id FOREIGN KEY (update_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE public.facility_visibility_statuses ADD CONSTRAINT fk_facility_visibility_statuses_create_user_id FOREIGN KEY (create_user_id) REFERENCES public.users(id) ON DELETE SET NULL;
ALTER TABLE public.facility_visibility_statuses ADD CONSTRAINT fk_facility_visibility_statuses_update_user_id FOREIGN KEY (update_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

-- Add indexes for audit columns for better query performance
CREATE INDEX idx_users_create_user_id ON public.users(create_user_id);
CREATE INDEX idx_users_update_user_id ON public.users(update_user_id);

CREATE INDEX idx_helpful_links_create_user_id ON public.helpful_links(create_user_id);
CREATE INDEX idx_helpful_links_update_user_id ON public.helpful_links(update_user_id);

CREATE INDEX idx_program_class_event_attendance_create_user_id ON public.program_class_event_attendance(create_user_id);
CREATE INDEX idx_program_class_event_attendance_update_user_id ON public.program_class_event_attendance(update_user_id);

CREATE INDEX idx_program_class_event_overrides_create_user_id ON public.program_class_event_overrides(create_user_id);
CREATE INDEX idx_program_class_event_overrides_update_user_id ON public.program_class_event_overrides(update_user_id);

CREATE INDEX idx_video_download_attempts_create_user_id ON public.video_download_attempts(create_user_id);
CREATE INDEX idx_video_download_attempts_update_user_id ON public.video_download_attempts(update_user_id);

CREATE INDEX idx_user_enrollments_create_user_id ON public.user_enrollments(create_user_id);
CREATE INDEX idx_user_enrollments_update_user_id ON public.user_enrollments(update_user_id);

CREATE INDEX idx_facilities_create_user_id ON public.facilities(create_user_id);
CREATE INDEX idx_facilities_update_user_id ON public.facilities(update_user_id);

CREATE INDEX idx_provider_platforms_create_user_id ON public.provider_platforms(create_user_id);
CREATE INDEX idx_provider_platforms_update_user_id ON public.provider_platforms(update_user_id);

CREATE INDEX idx_provider_user_mapping_create_user_id ON public.provider_user_mappings(create_user_id);
CREATE INDEX idx_provider_user_mapping_update_user_id ON public.provider_user_mappings(update_user_id);

CREATE INDEX idx_video_create_user_id ON public.videos(create_user_id);
CREATE INDEX idx_video_update_user_id ON public.videos(update_user_id);

CREATE INDEX idx_program_class_events_create_user_id ON public.program_class_events(create_user_id);
CREATE INDEX idx_program_class_events_update_user_id ON public.program_class_events(update_user_id);

CREATE INDEX idx_courses_create_user_id ON public.courses(create_user_id);
CREATE INDEX idx_courses_update_user_id ON public.courses(update_user_id);

CREATE INDEX idx_milestones_create_user_id ON public.milestones(create_user_id);
CREATE INDEX idx_milestones_update_user_id ON public.milestones(update_user_id);

CREATE INDEX idx_oidc_clients_create_user_id ON public.oidc_clients(create_user_id);
CREATE INDEX idx_oidc_clients_update_user_id ON public.oidc_clients(update_user_id);

CREATE INDEX idx_open_content_providers_create_user_id ON public.open_content_providers(create_user_id);
CREATE INDEX idx_open_content_providers_update_user_id ON public.open_content_providers(update_user_id);

CREATE INDEX idx_outcomes_create_user_id ON public.outcomes(create_user_id);
CREATE INDEX idx_outcomes_update_user_id ON public.outcomes(update_user_id);

CREATE INDEX idx_facilities_programs_create_user_id ON public.facilities_programs(create_user_id);
CREATE INDEX idx_facilities_programs_update_user_id ON public.facilities_programs(update_user_id);

CREATE INDEX idx_program_class_enrollments_create_user_id ON public.program_class_enrollments(create_user_id);
CREATE INDEX idx_program_class_enrollments_update_user_id ON public.program_class_enrollments(update_user_id);

CREATE INDEX idx_program_completions_create_user_id ON public.program_completions(create_user_id);
CREATE INDEX idx_program_completions_update_user_id ON public.program_completions(update_user_id);

CREATE INDEX idx_libraries_create_user_id ON public.libraries(create_user_id);
CREATE INDEX idx_libraries_update_user_id ON public.libraries(update_user_id);

CREATE INDEX idx_feature_flags_create_user_id ON public.feature_flags(create_user_id);
CREATE INDEX idx_feature_flags_update_user_id ON public.feature_flags(update_user_id);

CREATE INDEX idx_page_feature_flags_create_user_id ON public.page_feature_flags(create_user_id);
CREATE INDEX idx_page_feature_flags_update_user_id ON public.page_feature_flags(update_user_id);

CREATE INDEX idx_facility_visibility_statuses_create_user_id ON public.facility_visibility_statuses(create_user_id);
CREATE INDEX idx_facility_visibility_statuses_update_user_id ON public.facility_visibility_statuses(update_user_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_users_create_user_id;
DROP INDEX IF EXISTS idx_users_update_user_id;
DROP INDEX IF EXISTS idx_helpful_links_create_user_id;
DROP INDEX IF EXISTS idx_helpful_links_update_user_id;
DROP INDEX IF EXISTS idx_program_class_event_attendance_create_user_id;
DROP INDEX IF EXISTS idx_program_class_event_attendance_update_user_id;
DROP INDEX IF EXISTS idx_program_class_event_overrides_create_user_id;
DROP INDEX IF EXISTS idx_program_class_event_overrides_update_user_id;
DROP INDEX IF EXISTS idx_video_download_attempts_create_user_id;
DROP INDEX IF EXISTS idx_video_download_attempts_update_user_id;
DROP INDEX IF EXISTS idx_user_enrollments_create_user_id;
DROP INDEX IF EXISTS idx_user_enrollments_update_user_id;
DROP INDEX IF EXISTS idx_facilities_create_user_id;
DROP INDEX IF EXISTS idx_facilities_update_user_id;
DROP INDEX IF EXISTS idx_provider_platforms_create_user_id;
DROP INDEX IF EXISTS idx_provider_platforms_update_user_id;
DROP INDEX IF EXISTS idx_provider_user_mapping_create_user_id;
DROP INDEX IF EXISTS idx_provider_user_mapping_update_user_id;
DROP INDEX IF EXISTS idx_video_create_user_id;
DROP INDEX IF EXISTS idx_video_update_user_id;
DROP INDEX IF EXISTS idx_program_class_events_create_user_id;
DROP INDEX IF EXISTS idx_program_class_events_update_user_id;
DROP INDEX IF EXISTS idx_courses_create_user_id;
DROP INDEX IF EXISTS idx_courses_update_user_id;
DROP INDEX IF EXISTS idx_milestones_create_user_id;
DROP INDEX IF EXISTS idx_milestones_update_user_id;
DROP INDEX IF EXISTS idx_oidc_clients_create_user_id;
DROP INDEX IF EXISTS idx_oidc_clients_update_user_id;
DROP INDEX IF EXISTS idx_open_content_providers_create_user_id;
DROP INDEX IF EXISTS idx_open_content_providers_update_user_id;
DROP INDEX IF EXISTS idx_outcomes_create_user_id;
DROP INDEX IF EXISTS idx_outcomes_update_user_id;
DROP INDEX IF EXISTS idx_facilities_programs_create_user_id;
DROP INDEX IF EXISTS idx_facilities_programs_update_user_id;
DROP INDEX IF EXISTS idx_program_class_enrollments_create_user_id;
DROP INDEX IF EXISTS idx_program_class_enrollments_update_user_id;
DROP INDEX IF EXISTS idx_program_completions_create_user_id;
DROP INDEX IF EXISTS idx_program_completions_update_user_id;
DROP INDEX IF EXISTS idx_libraries_create_user_id;
DROP INDEX IF EXISTS idx_libraries_update_user_id;
DROP INDEX IF EXISTS idx_feature_flags_create_user_id;
DROP INDEX IF EXISTS idx_feature_flags_update_user_id;
DROP INDEX IF EXISTS idx_page_feature_flags_create_user_id;
DROP INDEX IF EXISTS idx_page_feature_flags_update_user_id;
DROP INDEX IF EXISTS idx_facility_visibility_statuses_create_user_id;
DROP INDEX IF EXISTS idx_facility_visibility_statuses_update_user_id;


-- Remove foreign key constraints
ALTER TABLE public.users DROP CONSTRAINT IF EXISTS fk_users_create_user_id;
ALTER TABLE public.users DROP CONSTRAINT IF EXISTS fk_users_update_user_id;
ALTER TABLE public.helpful_links DROP CONSTRAINT IF EXISTS fk_helpful_links_create_user_id;
ALTER TABLE public.helpful_links DROP CONSTRAINT IF EXISTS fk_helpful_links_update_user_id;
ALTER TABLE public.program_class_event_attendance DROP CONSTRAINT IF EXISTS fk_program_class_event_attendance_create_user_id;
ALTER TABLE public.program_class_event_attendance DROP CONSTRAINT IF EXISTS fk_program_class_event_attendance_update_user_id;
ALTER TABLE public.program_class_event_overrides DROP CONSTRAINT IF EXISTS fk_program_class_event_overrides_create_user_id;
ALTER TABLE public.program_class_event_overrides DROP CONSTRAINT IF EXISTS fk_program_class_event_overrides_update_user_id;
ALTER TABLE public.video_download_attempts DROP CONSTRAINT IF EXISTS fk_video_download_attempts_create_user_id;
ALTER TABLE public.video_download_attempts DROP CONSTRAINT IF EXISTS fk_video_download_attempts_update_user_id;
ALTER TABLE public.user_enrollments DROP CONSTRAINT IF EXISTS fk_user_enrollments_create_user_id;
ALTER TABLE public.user_enrollments DROP CONSTRAINT IF EXISTS fk_user_enrollments_update_user_id;
ALTER TABLE public.facilities DROP CONSTRAINT IF EXISTS fk_facilities_create_user_id;
ALTER TABLE public.facilities DROP CONSTRAINT IF EXISTS fk_facilities_update_user_id;
ALTER TABLE public.provider_platforms DROP CONSTRAINT IF EXISTS fk_provider_platforms_create_user_id;
ALTER TABLE public.provider_platforms DROP CONSTRAINT IF EXISTS fk_provider_platforms_update_user_id;
ALTER TABLE public.provider_user_mappings DROP CONSTRAINT IF EXISTS fk_provider_user_mapping_create_user_id;
ALTER TABLE public.provider_user_mappings DROP CONSTRAINT IF EXISTS fk_provider_user_mapping_update_user_id;
ALTER TABLE public.videos DROP CONSTRAINT IF EXISTS fk_video_create_user_id;
ALTER TABLE public.videos DROP CONSTRAINT IF EXISTS fk_video_update_user_id;
ALTER TABLE public.program_class_events DROP CONSTRAINT IF EXISTS fk_program_class_events_create_user_id;
ALTER TABLE public.program_class_events DROP CONSTRAINT IF EXISTS fk_program_class_events_update_user_id;
ALTER TABLE public.courses DROP CONSTRAINT IF EXISTS fk_courses_create_user_id;
ALTER TABLE public.courses DROP CONSTRAINT IF EXISTS fk_courses_update_user_id;
ALTER TABLE public.milestones DROP CONSTRAINT IF EXISTS fk_milestones_create_user_id;
ALTER TABLE public.milestones DROP CONSTRAINT IF EXISTS fk_milestones_update_user_id;
ALTER TABLE public.oidc_clients DROP CONSTRAINT IF EXISTS fk_oidc_clients_create_user_id;
ALTER TABLE public.oidc_clients DROP CONSTRAINT IF EXISTS fk_oidc_clients_update_user_id;
ALTER TABLE public.open_content_providers DROP CONSTRAINT IF EXISTS fk_open_content_providers_create_user_id;
ALTER TABLE public.open_content_providers DROP CONSTRAINT IF EXISTS fk_open_content_providers_update_user_id;
ALTER TABLE public.outcomes DROP CONSTRAINT IF EXISTS fk_outcomes_create_user_id;
ALTER TABLE public.outcomes DROP CONSTRAINT IF EXISTS fk_outcomes_update_user_id;
ALTER TABLE public.facilities_programs DROP CONSTRAINT IF EXISTS fk_facilities_programs_create_user_id;
ALTER TABLE public.facilities_programs DROP CONSTRAINT IF EXISTS fk_facilities_programs_update_user_id;
ALTER TABLE public.program_class_enrollments DROP CONSTRAINT IF EXISTS fk_program_class_enrollments_create_user_id;
ALTER TABLE public.program_class_enrollments DROP CONSTRAINT IF EXISTS fk_program_class_enrollments_update_user_id;
ALTER TABLE public.program_completions DROP CONSTRAINT IF EXISTS fk_program_completions_create_user_id;
ALTER TABLE public.program_completions DROP CONSTRAINT IF EXISTS fk_program_completions_update_user_id;
ALTER TABLE public.libraries DROP CONSTRAINT IF EXISTS fk_libraries_create_user_id;
ALTER TABLE public.libraries DROP CONSTRAINT IF EXISTS fk_libraries_update_user_id;
ALTER TABLE public.feature_flags DROP CONSTRAINT IF EXISTS fk_feature_flags_create_user_id;
ALTER TABLE public.feature_flags DROP CONSTRAINT IF EXISTS fk_feature_flags_update_user_id;
ALTER TABLE public.page_feature_flags DROP CONSTRAINT IF EXISTS fk_page_feature_flags_create_user_id;
ALTER TABLE public.page_feature_flags DROP CONSTRAINT IF EXISTS fk_page_feature_flags_update_user_id;
ALTER TABLE public.facility_visibility_statuses DROP CONSTRAINT IF EXISTS fk_facility_visibility_statuses_create_user_id;
ALTER TABLE public.facility_visibility_statuses DROP CONSTRAINT IF EXISTS fk_facility_visibility_statuses_update_user_id;

-- Remove audit columns
ALTER TABLE public.users DROP COLUMN IF EXISTS create_user_id;
ALTER TABLE public.users DROP COLUMN IF EXISTS update_user_id;
ALTER TABLE public.helpful_links DROP COLUMN IF EXISTS create_user_id;
ALTER TABLE public.helpful_links DROP COLUMN IF EXISTS update_user_id;
ALTER TABLE public.program_class_event_attendance DROP COLUMN IF EXISTS create_user_id;
ALTER TABLE public.program_class_event_attendance DROP COLUMN IF EXISTS update_user_id;
ALTER TABLE public.program_class_event_overrides DROP COLUMN IF EXISTS create_user_id;
ALTER TABLE public.program_class_event_overrides DROP COLUMN IF EXISTS update_user_id;
ALTER TABLE public.video_download_attempts DROP COLUMN IF EXISTS create_user_id;
ALTER TABLE public.video_download_attempts DROP COLUMN IF EXISTS update_user_id;
ALTER TABLE public.user_enrollments DROP COLUMN IF EXISTS create_user_id;
ALTER TABLE public.user_enrollments DROP COLUMN IF EXISTS update_user_id;
ALTER TABLE public.facilities DROP COLUMN IF EXISTS create_user_id;
ALTER TABLE public.facilities DROP COLUMN IF EXISTS update_user_id;
ALTER TABLE public.provider_platforms DROP COLUMN IF EXISTS create_user_id;
ALTER TABLE public.provider_platforms DROP COLUMN IF EXISTS update_user_id;
ALTER TABLE public.provider_user_mappings DROP COLUMN IF EXISTS create_user_id;
ALTER TABLE public.provider_user_mappings DROP COLUMN IF EXISTS update_user_id;
ALTER TABLE public.videos DROP COLUMN IF EXISTS create_user_id;
ALTER TABLE public.videos DROP COLUMN IF EXISTS update_user_id;
ALTER TABLE public.program_class_events DROP COLUMN IF EXISTS create_user_id;
ALTER TABLE public.program_class_events DROP COLUMN IF EXISTS update_user_id;
ALTER TABLE public.courses DROP COLUMN IF EXISTS create_user_id;
ALTER TABLE public.courses DROP COLUMN IF EXISTS update_user_id;
ALTER TABLE public.milestones DROP COLUMN IF EXISTS create_user_id;
ALTER TABLE public.milestones DROP COLUMN IF EXISTS update_user_id;
ALTER TABLE public.oidc_clients DROP COLUMN IF EXISTS create_user_id;
ALTER TABLE public.oidc_clients DROP COLUMN IF EXISTS update_user_id;
ALTER TABLE public.open_content_providers DROP COLUMN IF EXISTS create_user_id;
ALTER TABLE public.open_content_providers DROP COLUMN IF EXISTS update_user_id;
ALTER TABLE public.outcomes DROP COLUMN IF EXISTS create_user_id;
ALTER TABLE public.outcomes DROP COLUMN IF EXISTS update_user_id;
ALTER TABLE public.facilities_programs DROP COLUMN IF EXISTS create_user_id;
ALTER TABLE public.facilities_programs DROP COLUMN IF EXISTS update_user_id;
ALTER TABLE public.program_class_enrollments DROP COLUMN IF EXISTS create_user_id;
ALTER TABLE public.program_class_enrollments DROP COLUMN IF EXISTS update_user_id;
ALTER TABLE public.program_completions DROP COLUMN IF EXISTS create_user_id;
ALTER TABLE public.program_completions DROP COLUMN IF EXISTS update_user_id;
ALTER TABLE public.libraries DROP COLUMN IF EXISTS create_user_id;
ALTER TABLE public.libraries DROP COLUMN IF EXISTS update_user_id;
ALTER TABLE public.feature_flags DROP COLUMN IF EXISTS create_user_id;
ALTER TABLE public.feature_flags DROP COLUMN IF EXISTS update_user_id;
ALTER TABLE public.page_feature_flags DROP COLUMN IF EXISTS create_user_id;
ALTER TABLE public.page_feature_flags DROP COLUMN IF EXISTS update_user_id;
ALTER TABLE public.facility_visibility_statuses DROP COLUMN IF EXISTS create_user_id;
ALTER TABLE public.facility_visibility_statuses DROP COLUMN IF EXISTS update_user_id;

-- Remove the system batch user (use the fixed ID)
DELETE FROM public.users WHERE id = 99999 OR username = 'system_batch';

DROP INDEX IF EXISTS facilities_id_nonzero_idx;

ALTER TABLE public.users DROP CONSTRAINT IF EXISTS users_facility_id_zero_or_positive;
ALTER TABLE public.users ADD CONSTRAINT users_facility_id_fkey FOREIGN KEY (facility_id) REFERENCES public.facilities(id) ON UPDATE CASCADE ON DELETE CASCADE;
-- +goose StatementEnd