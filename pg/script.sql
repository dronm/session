CREATE DATABASE test_proj;

-- Table: public.session_vals

-- DROP TABLE public.session_vals;

CREATE TABLE public.session_vals
(
    id character(36) COLLATE pg_catalog."default" NOT NULL,
    accessed_time timestamp with time zone DEFAULT now(),
    create_time timestamp with time zone DEFAULT now(),
    val bytea,
    CONSTRAINT session_vals_pkey PRIMARY KEY (id)
)
WITH (
    OIDS = FALSE
)
TABLESPACE pg_default;

create extension pgcrypto;
