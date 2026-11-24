-- +goose Up
-- +goose StatementBegin
CREATE TABLE user_course_activity_totals ( 
	user_id INTEGER NOT NULL REFERENCES users(id), 
	course_id INTEGER NOT NULL REFERENCES courses(id),
	total_time BIGINT NOT NULL DEFAULT 0,
	last_ts timestamp with time zone,
	PRIMARY KEY (user_id, course_id)
);

DROP FUNCTION IF EXISTS public.insert_daily_activity(
       integer, integer, character varying, integer, character varying
);

CREATE OR REPLACE PROCEDURE insert_daily_activity_kolibri(
    _user_id INTEGER,
    _course_id INTEGER,
    _type VARCHAR,
    _total_time INTEGER,
    _external_id VARCHAR,
    _created_at TIMESTAMP
)
LANGUAGE plpgsql
AS $$
DECLARE 
    prev_total_time INTEGER := 0;
BEGIN
    SELECT total_time INTO prev_total_time
    FROM activities
    WHERE user_id = _user_id AND course_id = _course_id
    ORDER BY created_at DESC
    LIMIT 1;

    IF prev_total_time IS NULL THEN
        prev_total_time := 0;
    END IF;

    INSERT INTO activities (user_id, course_id, type, total_time, time_delta, external_id, created_at, updated_at)
    VALUES (
        _user_id, _course_id, _type, _total_time + prev_total_time, _total_time, _external_id, _created_at, NOW()
    );

    -- Upsert into user_course_activity_totals
    INSERT INTO user_course_activity_totals (user_id, course_id, total_time, last_ts)
    VALUES (_user_id, _course_id, _total_time + prev_total_time, _created_at)
    ON CONFLICT (user_id, course_id)
    DO UPDATE SET total_time = total_time + _total_time, last_ts = _created_at;

END;
$$;

CREATE OR REPLACE PROCEDURE insert_daily_activity_brightspace(
    _user_id INTEGER,
    _course_id INTEGER,
    _type VARCHAR,
    _total_time INTEGER,
    _external_id VARCHAR,
    _created_at TIMESTAMP
)
LANGUAGE plpgsql
AS $$
DECLARE 
    prev_total_time INTEGER := 0;
    prev_delta_time INTEGER := 0;
BEGIN
    SELECT total_time INTO prev_total_time
    FROM activities
    WHERE user_id = _user_id AND course_id = _course_id
    ORDER BY created_at DESC
    LIMIT 1;

    IF prev_total_time IS NULL THEN
        prev_total_time := 0;
    END IF;

    SELECT time_delta INTO prev_delta_time
    FROM activities
    WHERE user_id = _user_id AND course_id = _course_id
          AND SUBSTRING(external_id FROM '^[^-]+') = SUBSTRING(_external_id FROM '^[^-]+')
    ORDER BY created_at DESC
    LIMIT 1;

    IF prev_delta_time IS NULL THEN
        prev_delta_time := 0;
    END IF;

    INSERT INTO activities (user_id, course_id, type, total_time, time_delta, external_id, created_at, updated_at)
    VALUES (
        _user_id, _course_id, _type, (_total_time - prev_delta_time) + prev_total_time, 
        _total_time - prev_delta_time, _external_id, NOW(), NOW()
    );

    INSERT INTO user_course_activity_totals (user_id, course_id, total_time, last_ts)
    VALUES (_user_id, _course_id, _total_time, NOW())
    ON CONFLICT (user_id, course_id)
    DO UPDATE SET total_time = _total_time, last_ts = NOW();
END;
$$;

CREATE OR REPLACE PROCEDURE insert_daily_activity_canvas(
    _user_id INTEGER,
    _course_id INTEGER,
    _type VARCHAR,
    _total_time INTEGER,
    _external_id VARCHAR,
    _created_at TIMESTAMP
)
LANGUAGE plpgsql
AS $$
DECLARE 
    prev_total_time INTEGER := 0;
BEGIN
    SELECT total_time INTO prev_total_time
    FROM activities
    WHERE user_id = _user_id AND course_id = _course_id
    ORDER BY created_at DESC
    LIMIT 1;

    IF prev_total_time IS NULL THEN
        prev_total_time := 0;
    END IF;

    INSERT INTO activities (user_id, course_id, type, total_time, time_delta, external_id, created_at, updated_at)
    VALUES (
        _user_id, _course_id, _type, _total_time, _total_time - prev_total_time, _external_id, _created_at, NOW()
    );

    INSERT INTO user_course_activity_totals (user_id, course_id, total_time, last_ts)
    VALUES (_user_id, _course_id, _total_time, NOW())
    ON CONFLICT (user_id, course_id)
    DO UPDATE SET total_time = _total_time, last_ts = NOW();
END;
$$;

CREATE TABLE public.user_enrollments (
	user_id INTEGER NOT NULL REFERENCES users(id), 
	course_id INTEGER NOT NULL REFERENCES courses(id),
	external_id VARCHAR(64),
    created_at timestamp with time zone,
    updated_at timestamp with time zone,
    deleted_at timestamp with time zone,
	PRIMARY KEY (user_id, course_id)
);
CREATE UNIQUE INDEX idx_user_enrollments_external_id ON public.user_enrollments(external_id, course_id) WHERE external_id IS NOT NULL;

CREATE TABLE public.open_content_favorites (
	id SERIAL NOT NULL,
    content_id INTEGER NOT NULL,
	open_content_provider_id INTEGER NOT NULL REFERENCES public.open_content_providers(id),
	user_id INTEGER NOT NULL REFERENCES public.users(id),
	facility_id INTEGER REFERENCES public.facilities(id),
    created_at timestamp with time zone,
    updated_at timestamp with time zone,
    deleted_at timestamp with time zone,
	PRIMARY KEY (content_id, open_content_provider_id, user_id)
);
CREATE UNIQUE INDEX idx_facility_library_user_favorites ON public.open_content_favorites(content_id, facility_id, open_content_provider_id) WHERE facility_id IS NOT NULL;

DROP TABLE IF EXISTS public.video_favorites CASCADE;
DROP TABLE IF EXISTS public.library_favorites CASCADE;
DROP TABLE IF EXISTS public.helpful_links_favorites CASCADE;


-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.user_course_activity_totals CASCADE;

CREATE OR REPLACE FUNCTION
    public.insert_daily_activity(
        _user_id integer,
        _course_id integer,
        _type character varying,
        _total_time integer,
        _external_id character varying
    )
RETURNS void
LANGUAGE plpgsql
as 
$$
    DECLARE prev_total_time INT;
    DECLARE prev_delta_time INT;
    DECLARE provider_type CHAR ( 100 );
    BEGIN
        SELECT p.type INTO provider_type FROM activities a 
        INNER JOIN courses c ON c.id = a.course_id 
                AND c.deleted_at IS NULL
        INNER JOIN provider_platforms p ON p.id = c.provider_platform_id
                AND p.deleted_at IS NULL
        WHERE a.user_id = _user_id AND a.course_id = _course_id
        LIMIT 1;
        SELECT total_time INTO prev_total_time FROM activities
        WHERE user_id = _user_id AND course_id = _course_id
        ORDER BY created_at DESC LIMIT 1;

        IF prev_total_time IS NULL THEN
            prev_total_time := 0;
        END IF;
        --brightspace previous time_delta is needed for recalulating actual
        --total time since brightspace is accumulating time spent on 
        --possibly the same content until student has completed content
        IF prev_total_time > 0 AND provider_type = 'brightspace' THEN
                SELECT time_delta INTO prev_delta_time FROM activities
                WHERE user_id = _user_id AND course_id = _course_id
                        AND SUBSTRING(external_id FROM '^[^-]+') = SUBSTRING(_external_id FROM '^[^-]+')
                ORDER BY created_at DESC LIMIT 1;
        END IF;
        
        IF prev_delta_time IS NULL THEN
            prev_delta_time := 0;
        END IF;
        --kolibri total_time is the delta and total_time 
        --is required to be added to previous_total_time
        IF provider_type = 'kolibri' THEN
                INSERT INTO activities (user_id, course_id, type, total_time, time_delta, external_id, created_at, updated_at)
                VALUES (_user_id, _course_id, _type, _total_time + prev_total_time, _total_time, _external_id, NOW(), NOW());
        ELSEIF provider_type = 'brightspace' THEN
                INSERT INTO activities (user_id, course_id, type, total_time, time_delta, external_id, created_at, updated_at)
                VALUES (_user_id, _course_id, _type, (_total_time-prev_delta_time)+prev_total_time, _total_time-prev_delta_time, _external_id, NOW(), NOW());
        ELSE
                INSERT INTO activities (user_id, course_id, type, total_time, time_delta, external_id, created_at, updated_at)
                VALUES (_user_id, _course_id, _type, _total_time, _total_time - prev_total_time, _external_id, NOW(), NOW());
        END IF;
    END;
$$
;
DROP PROCEDURE IF EXISTS insert_daily_activity_kolibri;
DROP PROCEDURE IF EXISTS insert_daily_activity_brightspace;
DROP PROCEDURE IF EXISTS insert_daily_activity_canvas;
DROP TABLE IF EXISTS public.user_enrollments CASCADE;
DROP TABLE IF EXISTS public.open_content_favorites CASCADE;

CREATE TABLE public.library_favorites (
        id SERIAL NOT NULL PRIMARY KEY,
        created_at timestamp with time zone,
        updated_at timestamp with time zone,
        deleted_at timestamp with time zone,
        user_id INTEGER NOT NULL,
        library_id INTEGER NOT NULL,
        facility_id INTEGER DEFAULT NULL REFERENCES public.facilities(id),
        FOREIGN KEY (user_id) REFERENCES public.users(id) ON UPDATE CASCADE ON DELETE CASCADE,
        CONSTRAINT uni_user_lib_id UNIQUE (user_id, library_id)
);
CREATE UNIQUE INDEX idx_facility_library_user ON public.library_favorites (library_id, facility_id) WHERE facility_id IS NOT NULL;

CREATE TABLE public.video_favorites (
	id SERIAL NOT NULL PRIMARY KEY,
	created_at timestamp with time zone,
	updated_at timestamp with time zone,
	deleted_at timestamp with time zone,
	video_id integer NOT NULL,
	user_id integer NOT NULL,
	FOREIGN KEY (video_id) REFERENCES videos(id) ON DELETE CASCADE,
	FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
	CONSTRAINT uni_user_vid_id UNIQUE (user_id, video_id)
);

-- +goose StatementEnd
