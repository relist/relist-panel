-- One rollup table per scope (spec, "two rollup tables, one per scope"): host
-- and Service samples carry different fields, so each gets only the columns it
-- uses. bucket_at is Unix seconds, the repo's timestamp idiom, and marks the
-- wall-clock minute the row's samples were seen in — a minute a restart cut
-- short still writes its row, because the screens' `partial` state is the
-- honest display for the gap.
--
-- Metric columns are nullable on purpose: a field no sample carried (an
-- unreadable source, a rateless first tick) has no honest average and stays
-- NULL — never a zero wearing a measurement's clothes.

-- +goose Up
CREATE TABLE host_metric_rollups (
    bucket_at         INTEGER NOT NULL PRIMARY KEY,
    cpu_percent       REAL,
    mem_used_bytes    REAL,
    mem_total_bytes   REAL,
    disk_free_bytes   REAL,
    disk_total_bytes  REAL,
    docker_disk_bytes REAL,
    disk_read_bps     REAL,
    disk_write_bps    REAL,
    net_rx_bps        REAL,
    net_tx_bps        REAL
);

CREATE TABLE service_metric_rollups (
    compose_project TEXT NOT NULL,
    service         TEXT NOT NULL,
    bucket_at       INTEGER NOT NULL,
    cpu_percent     REAL,
    mem_used_bytes  REAL,
    mem_limit_bytes REAL,
    net_rx_bps      REAL,
    net_tx_bps      REAL,
    block_read_bps  REAL,
    block_write_bps REAL,
    pids            REAL,
    PRIMARY KEY (compose_project, service, bucket_at)
);

-- The (project, service, bucket_at) primary key serves a Service's window
-- query; this index serves the retention delete, which drops by age alone
-- across every Service.
CREATE INDEX service_metric_rollups_bucket_at_idx ON service_metric_rollups(bucket_at);

-- +goose Down
DROP TABLE service_metric_rollups;
DROP TABLE host_metric_rollups;
