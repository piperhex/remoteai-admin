CREATE TABLE IF NOT EXISTS public.update_download_reports (
    id uuid NOT NULL,
    user_id uuid NOT NULL,
    artifact varchar(64) NOT NULL,
    platform varchar(16) NOT NULL,
    outcome varchar(16) NOT NULL CHECK (outcome IN ('success', 'failed')),
    peer_bytes bigint NOT NULL DEFAULT 0 CHECK (peer_bytes >= 0),
    server_bytes bigint NOT NULL DEFAULT 0 CHECK (server_bytes >= 0),
    origin_bytes bigint NOT NULL DEFAULT 0 CHECK (origin_bytes >= 0),
    failures jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_update_download_reports_created_at ON public.update_download_reports (created_at);
