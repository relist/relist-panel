-- Request metrics read from the Traefik Access log (ADR-0033). Series are keyed
-- by the router the panel wrote, never by the client-chosen Host, so the
-- number of series follows the number of Domains, not the traffic.
--
-- bucket_at is Unix seconds, the start of the minute (or hour). latency is a
-- sparse varint-encoded log-scale histogram (internal/requestmetrics).

-- +goose Up
CREATE TABLE request_metric_rollups (
    scope      TEXT    NOT NULL CHECK (scope IN ('total', 'panel', 'host')),
    compose_id INTEGER NOT NULL, -- 0 unless scope = 'host'
    host       TEXT    NOT NULL, -- '' unless scope = 'host'
    bucket_at  INTEGER NOT NULL,
    c1xx   INTEGER NOT NULL,
    c2xx   INTEGER NOT NULL,
    c3xx   INTEGER NOT NULL,
    c4xx   INTEGER NOT NULL,
    c5xx   INTEGER NOT NULL,
    cother INTEGER NOT NULL,
    latency BLOB NOT NULL,
    PRIMARY KEY (scope, compose_id, host, bucket_at)
) WITHOUT ROWID;

CREATE TABLE request_metric_hourly (
    scope      TEXT    NOT NULL CHECK (scope IN ('total', 'panel', 'host')),
    compose_id INTEGER NOT NULL,
    host       TEXT    NOT NULL,
    bucket_at  INTEGER NOT NULL,
    c1xx   INTEGER NOT NULL,
    c2xx   INTEGER NOT NULL,
    c3xx   INTEGER NOT NULL,
    c4xx   INTEGER NOT NULL,
    c5xx   INTEGER NOT NULL,
    cother INTEGER NOT NULL,
    latency BLOB NOT NULL,
    PRIMARY KEY (scope, compose_id, host, bucket_at)
) WITHOUT ROWID;

-- The primary keys serve a series' window query; these serve the retention
-- deletes, which drop by age alone across every series.
CREATE INDEX request_metric_rollups_bucket_at_idx ON request_metric_rollups(bucket_at);
CREATE INDEX request_metric_hourly_bucket_at_idx ON request_metric_hourly(bucket_at);

-- How many days the hourly rows are kept. One row.
CREATE TABLE request_metric_settings (
    id             INTEGER PRIMARY KEY CHECK (id = 1),
    retention_days INTEGER NOT NULL DEFAULT 1 CHECK (retention_days BETWEEN 1 AND 90)
);
INSERT INTO request_metric_settings (id, retention_days) VALUES (1, 1);

-- Where the Ingester resumes after a restart, written in the same
-- transaction as the minute rows it accounts for. One row.
CREATE TABLE request_metric_cursor (
    id              INTEGER PRIMARY KEY CHECK (id = 1),
    inode           INTEGER NOT NULL,
    resume_offset   INTEGER NOT NULL,
    flushed_through INTEGER NOT NULL -- unix seconds: every minute before this is written
);

-- +goose Down
DROP TABLE request_metric_cursor;
DROP TABLE request_metric_settings;
DROP TABLE request_metric_hourly;
DROP TABLE request_metric_rollups;
