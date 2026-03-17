CREATE TABLE leagues (
    id          BIGINT PRIMARY KEY,
    name        TEXT NOT NULL,
    league_type TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE managers (
    id          BIGINT PRIMARY KEY,
    league_id   BIGINT NOT NULL REFERENCES leagues(id),
    name        TEXT NOT NULL,
    team_name   TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_managers_league_id ON managers(league_id);

CREATE TABLE gameweek_standings (
    league_id   BIGINT NOT NULL REFERENCES leagues(id),
    event_id    INT NOT NULL,
    manager_id  BIGINT NOT NULL REFERENCES managers(id),
    rank        INT NOT NULL,
    points      INT NOT NULL,
    total_score INT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (league_id, event_id, manager_id)
);

CREATE TABLE chip_usage (
    league_id   BIGINT NOT NULL REFERENCES leagues(id),
    manager_id  BIGINT NOT NULL REFERENCES managers(id),
    event_id    INT NOT NULL,
    chip        TEXT NOT NULL,
    detected_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (league_id, manager_id, event_id, chip)
);

CREATE TABLE h2h_results (
    league_id       BIGINT NOT NULL REFERENCES leagues(id),
    event_id        INT NOT NULL,
    manager_1_id    BIGINT NOT NULL REFERENCES managers(id),
    manager_1_score INT NOT NULL,
    manager_2_id    BIGINT NOT NULL REFERENCES managers(id),
    manager_2_score INT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (league_id, event_id, manager_1_id, manager_2_id)
);
