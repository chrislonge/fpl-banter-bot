CREATE TABLE leagues (
    id          BIGINT PRIMARY KEY,
    name        TEXT NOT NULL,
    league_type TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE managers (
    league_id   BIGINT NOT NULL REFERENCES leagues(id),
    id          BIGINT NOT NULL,
    name        TEXT NOT NULL,
    team_name   TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (league_id, id)
);

CREATE TABLE gameweek_standings (
    league_id   BIGINT NOT NULL,
    event_id    INT NOT NULL,
    manager_id  BIGINT NOT NULL,
    rank        INT NOT NULL,
    points      INT NOT NULL,
    total_score INT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (league_id, event_id, manager_id),
    FOREIGN KEY (league_id, manager_id) REFERENCES managers(league_id, id)
);

CREATE TABLE chip_usage (
    league_id   BIGINT NOT NULL,
    manager_id  BIGINT NOT NULL,
    event_id    INT NOT NULL,
    chip        TEXT NOT NULL,
    detected_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (league_id, manager_id, event_id, chip),
    FOREIGN KEY (league_id, manager_id) REFERENCES managers(league_id, id)
);

CREATE TABLE h2h_results (
    league_id       BIGINT NOT NULL,
    event_id        INT NOT NULL,
    manager_1_id    BIGINT NOT NULL,
    manager_1_score INT NOT NULL,
    manager_2_id    BIGINT NOT NULL,
    manager_2_score INT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (league_id, event_id, manager_1_id, manager_2_id),
    FOREIGN KEY (league_id, manager_1_id) REFERENCES managers(league_id, id),
    FOREIGN KEY (league_id, manager_2_id) REFERENCES managers(league_id, id),
    CHECK (manager_1_id < manager_2_id)
);

CREATE TABLE gameweek_snapshot_meta (
    league_id          BIGINT NOT NULL REFERENCES leagues(id),
    event_id           INT NOT NULL,
    source             TEXT NOT NULL,
    standings_fidelity TEXT NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (league_id, event_id)
);
