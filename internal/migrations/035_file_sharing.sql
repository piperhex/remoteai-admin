CREATE TABLE IF NOT EXISTS public.shared_files (
    id uuid PRIMARY KEY,
    name text NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 200),
    size bigint NOT NULL CHECK (size BETWEEN 0 AND 10737418240),
    uploaded_bytes bigint NOT NULL CHECK (uploaded_bytes BETWEEN 0 AND size),
    fingerprint varchar(64) NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS public.shared_file_chunks (
    file_id uuid NOT NULL REFERENCES public.shared_files(id) ON DELETE CASCADE,
    chunk_index integer NOT NULL CHECK (chunk_index >= 0),
    digest bytea NOT NULL CHECK (octet_length(digest) = 32),
    data bytea NOT NULL CHECK (octet_length(data) BETWEEN 1 AND 1048576),
    PRIMARY KEY (file_id, chunk_index)
);
