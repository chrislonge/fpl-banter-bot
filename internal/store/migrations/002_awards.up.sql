CREATE TABLE gameweek_manager_stats (
    league_id           BIGINT NOT NULL,
    event_id            INT NOT NULL,
    manager_id          BIGINT NOT NULL,
    points_on_bench     INT NOT NULL,
    captain_element_id  INT,
    captain_points      INT,
    captain_multiplier  INT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (league_id, event_id, manager_id),
    FOREIGN KEY (league_id, manager_id) REFERENCES managers(league_id, id),
    CHECK (
        (
            captain_element_id IS NULL AND
            captain_points IS NULL AND
            captain_multiplier IS NULL
        ) OR (
            captain_element_id IS NOT NULL AND
            captain_points IS NOT NULL AND
            captain_multiplier IN (2, 3)
        )
    )
);

CREATE TABLE gw_awards (
    league_id            BIGINT NOT NULL,
    event_id             INT NOT NULL,
    award_key            TEXT NOT NULL,
    manager_id           BIGINT NOT NULL,
    opponent_manager_id  BIGINT,
    player_element_id    INT,
    metric_value         INT NOT NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (league_id, event_id, award_key),
    FOREIGN KEY (league_id, manager_id) REFERENCES managers(league_id, id),
    FOREIGN KEY (league_id, opponent_manager_id) REFERENCES managers(league_id, id)
);
