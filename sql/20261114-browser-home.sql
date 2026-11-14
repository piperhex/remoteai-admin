CREATE TABLE IF NOT EXISTS public.browser_home_settings (
    id varchar(32) PRIMARY KEY,
    enabled boolean NOT NULL DEFAULT false,
    revision varchar(36) NOT NULL DEFAULT '',
    md5 varchar(32) NOT NULL DEFAULT '',
    file_name text NOT NULL DEFAULT '',
    file_count integer NOT NULL DEFAULT 0,
    total_bytes bigint NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS public.browser_home_packages (
    md5 varchar(32) PRIMARY KEY,
    data bytea NOT NULL
);

CREATE TABLE IF NOT EXISTS public.browser_home_assets (
    revision varchar(36) NOT NULL,
    path text NOT NULL,
    content_type text NOT NULL,
    data bytea NOT NULL,
    PRIMARY KEY (revision, path)
);
