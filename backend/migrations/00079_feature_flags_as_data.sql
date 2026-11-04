-- +goose Up
-- EN-257: feature_flags becomes the list of features, so a product can add its
-- own without a core change. The feature enum becomes text, and each row
-- carries its statewide default and the text Feature Control shows. The
-- backfill copies the defaults and card text that were hard-coded in Go and
-- FeatureControl.tsx before this release, so every facility resolves exactly as
-- before. Both tables are a handful of rows.
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';

ALTER TABLE public.feature_flags ALTER COLUMN name TYPE TEXT USING name::text;
ALTER TABLE public.page_feature_flags ALTER COLUMN page_feature TYPE TEXT USING page_feature::text;
DROP TYPE feature;

ALTER TABLE public.feature_flags
    ADD CONSTRAINT feature_flags_name_format CHECK (name ~ '^[a-z][a-z0-9_]{0,63}$');

ALTER TABLE public.feature_flags
    ADD COLUMN default_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN title TEXT,
    ADD COLUMN description TEXT;

UPDATE public.feature_flags AS ff
   SET default_enabled = v.default_enabled, title = v.title, description = v.description
  FROM (VALUES
    ('open_content', TRUE, 'Knowledge Center',
     'Provides educational content, videos, and resources for residents'),
    ('provider_platforms', TRUE, 'Learning Platforms',
     'Enables access to third-party provider platforms and courses'),
    ('program_management', TRUE, 'Program Hub & Tracking',
     'Enables program management, class scheduling, attendance tracking, and resident enrollment functionality'),
    ('learning_record', FALSE, 'Learning Record',
     'Allows residents to log and track their learning achievements and generate a personal learning record'),
    ('ai_tutor', FALSE, 'AI Tutor',
     'Provides an AI-powered HiSET tutor and writing assistant for residents'),
    ('request_content', TRUE, 'Request Content Button',
     'Allows residents to submit requests for new content to be added to the Knowledge Center'),
    ('helpful_links', TRUE, 'Helpful Links',
     'Enables the Helpful Links tab for residents and allows admins to add/manage helpful resources'),
    ('upload_video', TRUE, 'Videos',
     'Enables video content viewing for residents and allows admins to upload/manage videos'),
    ('resident_programs', TRUE, 'Programs Page for Residents',
     'Shows the Programs page and its nav item in the resident portal. Program tracking, data, and admin views are unaffected either way.')
  ) AS v(name, default_enabled, title, description)
 WHERE ff.name = v.name;

ALTER TABLE public.feature_flags ALTER COLUMN title SET NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- Fails on the enum cast if a product has registered a feature since; delete
-- those rows first (their facility overrides cascade).
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';

ALTER TABLE public.feature_flags
    DROP COLUMN description,
    DROP COLUMN title,
    DROP COLUMN default_enabled;
ALTER TABLE public.feature_flags DROP CONSTRAINT feature_flags_name_format;

CREATE TYPE feature AS ENUM (
    'open_content', 'provider_platforms', 'program_management',
    'request_content', 'helpful_links', 'upload_video',
    'learning_record', 'resident_programs', 'ai_tutor'
);
ALTER TABLE public.page_feature_flags ALTER COLUMN page_feature TYPE feature USING page_feature::feature;
ALTER TABLE public.feature_flags ALTER COLUMN name TYPE feature USING name::feature;
-- +goose StatementEnd
