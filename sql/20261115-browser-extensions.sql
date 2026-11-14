CREATE TABLE IF NOT EXISTS public.browser_extensions (
    id uuid PRIMARY KEY,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    version text NOT NULL,
    icon text NOT NULL DEFAULT '',
    permissions jsonb NOT NULL DEFAULT '[]',
    sha256 varchar(64) NOT NULL,
    size bigint NOT NULL CHECK (size > 0 AND size <= 67108864),
    published boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS public.browser_extension_packages (
    id uuid PRIMARY KEY REFERENCES public.browser_extensions(id) ON DELETE CASCADE,
    data bytea NOT NULL
);
