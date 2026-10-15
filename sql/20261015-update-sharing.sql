CREATE TABLE IF NOT EXISTS public.update_sharing_settings (
    id varchar(32) PRIMARY KEY,
    policy jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
