-- +goose Up
-- Users deleted while Provider Platforms, now its own service, still has their
-- account links to remove (EN-206). DeleteUser queues a row in the same
-- transaction; a job removes it once Provider Platforms has unlinked the user.
CREATE TABLE public.provider_mapping_deletions (
    user_id    bigint PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE public.provider_mapping_deletions;
