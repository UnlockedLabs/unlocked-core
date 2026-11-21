-- +goose Up
-- +goose StatementBegin
ALTER TABLE public.library_favorites
	RENAME COLUMN content_id TO library_id;
ALTER TABLE public.library_favorites
    DROP COLUMN name,
	DROP COLUMN visibility_status,
	DROP COLUMN open_content_url_id,
	DROP COLUMN open_content_provider_id;
ALTER TABLE public.library_favorites ADD COLUMN facility_id INTEGER DEFAULT NULL REFERENCES public.facilities(id);
CREATE UNIQUE INDEX idx_facility_library_user ON library_favorites (library_id, facility_id) WHERE facility_id IS NOT NULL;

ALTER TABLE public.library_favorites ADD CONSTRAINT uni_user_lib_id UNIQUE(user_id, library_id);
ALTER TABLE public.video_favorites ADD CONSTRAINT uni_user_vid_id UNIQUE(user_id, video_id);
ALTER TABLE public.libraries RENAME COLUMN image_url TO thumbnail_url;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.libraries RENAME COLUMN thumbnail_url TO image_url;
ALTER TABLE public.library_favorites RENAME COLUMN library_id TO content_id;
ALTER TABLE public.library_favorites DROP COLUMN facility_id;
ALTER TABLE public.library_favorites DROP CONSTRAINT IF EXISTS uni_user_lib_id;
ALTER TABLE public.video_favorites DROP CONSTRAINT IF EXISTS uni_user_vid_id;
DROP INDEX IF EXISTS idx_facility_library_user;

ALTER TABLE public.library_favorites
	ADD COLUMN IF NOT EXISTS name CHARACTER VARYING(255),
	ADD COLUMN IF NOT EXISTS visibility_status BOOLEAN,
	ADD COLUMN IF NOT EXISTS open_content_url_id INTEGER,
	ADD COLUMN IF NOT EXISTS open_content_provider_id INTEGER;
-- +goose StatementEnd
