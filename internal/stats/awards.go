package stats

import (
	"github.com/chrislonge/fpl-banter-bot/internal/store"
	"github.com/chrislonge/fpl-banter-bot/pkg/notify"
)

const (
	awardManagerOfTheWeek = "manager_of_the_week"
	awardWoodenSpoon      = "wooden_spoon"
	awardCaptainGenius    = "captain_genius"
	awardArmbandOfShame   = "armband_of_shame"
	awardBenchWarmer      = "bench_warmer"
	awardBiggestThrashing = "biggest_thrashing"
	awardLuckiestWin      = "luckiest_win"
	awardUnluckiestLoss   = "unluckiest_loss"
)

func buildGameweekAwards(leagueID int64, eventID int, managerByID map[int64]notify.ManagerRef, playerByID map[int]notify.PlayerRef, results []store.H2HResult, managerStats []store.GameweekManagerStat, plotTwist *notify.UpsetAlert) (*notify.GameweekAwardsAlert, []store.GameweekAward) {
	scoreByManager := scoresByManager(results)
	if len(scoreByManager) == 0 {
		return nil, nil
	}

	awards := &notify.GameweekAwardsAlert{
		PlotTwist: plotTwist,
	}
	var rows []store.GameweekAward

	managerOfWeek := highestScoreAward(scoreByManager, managerByID)
	woodenSpoon := lowestScoreAward(scoreByManager, managerByID)
	awards.ManagerOfTheWeek = &managerOfWeek
	awards.WoodenSpoon = &woodenSpoon
	rows = append(rows,
		store.GameweekAward{
			LeagueID:    leagueID,
			EventID:     eventID,
			AwardKey:    awardManagerOfTheWeek,
			ManagerID:   managerOfWeek.Manager.ID,
			MetricValue: managerOfWeek.Score,
		},
		store.GameweekAward{
			LeagueID:    leagueID,
			EventID:     eventID,
			AwardKey:    awardWoodenSpoon,
			ManagerID:   woodenSpoon.Manager.ID,
			MetricValue: woodenSpoon.Score,
		},
	)

	if benchAward, benchRow := benchWarmerAward(leagueID, eventID, managerByID, managerStats); benchAward != nil {
		awards.BenchWarmer = benchAward
		rows = append(rows, *benchRow)
	}
	if captainAward, captainRow := captainGeniusAward(leagueID, eventID, managerByID, playerByID, managerStats); captainAward != nil {
		awards.CaptainGenius = captainAward
		rows = append(rows, *captainRow)
	}
	if shameAward, shameRow := armbandOfShameAward(leagueID, eventID, managerByID, playerByID, managerStats); shameAward != nil {
		awards.ArmbandOfShame = shameAward
		rows = append(rows, *shameRow)
	}
	if thrashingAward, thrashingRow := biggestThrashingAward(leagueID, eventID, managerByID, results); thrashingAward != nil {
		awards.BiggestThrashing = thrashingAward
		rows = append(rows, *thrashingRow)
	}
	if luckyAward, luckyRow := luckiestWinAward(leagueID, eventID, managerByID, results); luckyAward != nil {
		awards.LuckiestWin = luckyAward
		rows = append(rows, *luckyRow)
	}
	if unluckyAward, unluckyRow := unluckiestLossAward(leagueID, eventID, managerByID, results); unluckyAward != nil {
		awards.UnluckiestLoss = unluckyAward
		rows = append(rows, *unluckyRow)
	}

	return awards, rows
}

func scoresByManager(results []store.H2HResult) map[int64]int {
	scoreByManager := make(map[int64]int, len(results)*2)
	for _, result := range results {
		scoreByManager[result.Manager1ID] = result.Manager1Score
		scoreByManager[result.Manager2ID] = result.Manager2Score
	}
	return scoreByManager
}

func highestScoreAward(scoreByManager map[int64]int, managerByID map[int64]notify.ManagerRef) notify.ManagerScore {
	var bestID int64
	bestScore := 0
	first := true
	for managerID, score := range scoreByManager {
		if first || score > bestScore || (score == bestScore && managerID < bestID) {
			bestID = managerID
			bestScore = score
			first = false
		}
	}
	return notify.ManagerScore{Manager: managerRef(managerByID, bestID), Score: bestScore}
}

func lowestScoreAward(scoreByManager map[int64]int, managerByID map[int64]notify.ManagerRef) notify.ManagerScore {
	var worstID int64
	worstScore := 0
	first := true
	for managerID, score := range scoreByManager {
		if first || score < worstScore || (score == worstScore && managerID < worstID) {
			worstID = managerID
			worstScore = score
			first = false
		}
	}
	return notify.ManagerScore{Manager: managerRef(managerByID, worstID), Score: worstScore}
}

func benchWarmerAward(leagueID int64, eventID int, managerByID map[int64]notify.ManagerRef, managerStats []store.GameweekManagerStat) (*notify.BenchWarmerAwardAlert, *store.GameweekAward) {
	if len(managerStats) == 0 {
		return nil, nil
	}

	best := managerStats[0]
	for _, stat := range managerStats[1:] {
		if stat.PointsOnBench > best.PointsOnBench || (stat.PointsOnBench == best.PointsOnBench && stat.ManagerID < best.ManagerID) {
			best = stat
		}
	}

	return &notify.BenchWarmerAwardAlert{
			Manager:       managerRef(managerByID, best.ManagerID),
			PointsOnBench: best.PointsOnBench,
		}, &store.GameweekAward{
			LeagueID:    leagueID,
			EventID:     eventID,
			AwardKey:    awardBenchWarmer,
			ManagerID:   best.ManagerID,
			MetricValue: best.PointsOnBench,
		}
}

func captainGeniusAward(leagueID int64, eventID int, managerByID map[int64]notify.ManagerRef, playerByID map[int]notify.PlayerRef, managerStats []store.GameweekManagerStat) (*notify.CaptainAwardAlert, *store.GameweekAward) {
	if !captainDataComplete(managerStats) {
		return nil, nil
	}

	best := managerStats[0]
	bestTotal := captainTotal(best)
	for _, stat := range managerStats[1:] {
		total := captainTotal(stat)
		if total > bestTotal || (total == bestTotal && stat.ManagerID < best.ManagerID) {
			best = stat
			bestTotal = total
		}
	}

	return &notify.CaptainAwardAlert{
			Manager:           managerRef(managerByID, best.ManagerID),
			Captain:           playerRef(playerByID, *best.CaptainElementID),
			CaptainPoints:     *best.CaptainPoints,
			CaptainMultiplier: *best.CaptainMultiplier,
			TotalPoints:       bestTotal,
		}, &store.GameweekAward{
			LeagueID:       leagueID,
			EventID:        eventID,
			AwardKey:       awardCaptainGenius,
			ManagerID:      best.ManagerID,
			PlayerElementID: best.CaptainElementID,
			MetricValue:    bestTotal,
		}
}

func armbandOfShameAward(leagueID int64, eventID int, managerByID map[int64]notify.ManagerRef, playerByID map[int]notify.PlayerRef, managerStats []store.GameweekManagerStat) (*notify.ArmbandOfShameAlert, *store.GameweekAward) {
	if !captainDataComplete(managerStats) {
		return nil, nil
	}

	captainCounts := make(map[int]int, len(managerStats))
	captainPoints := make(map[int]int, len(managerStats))
	for _, stat := range managerStats {
		elementID := *stat.CaptainElementID
		captainCounts[elementID]++
		captainPoints[elementID] = *stat.CaptainPoints
	}

	consensusElementID := 0
	consensusCount := 0
	first := true
	for elementID, count := range captainCounts {
		if first || count > consensusCount || (count == consensusCount && elementID < consensusElementID) {
			consensusElementID = elementID
			consensusCount = count
			first = false
		}
	}
	if consensusCount < 2 || captainPoints[consensusElementID] < 10 {
		return nil, nil
	}

	var worst *store.GameweekManagerStat
	for i := range managerStats {
		stat := &managerStats[i]
		if *stat.CaptainElementID == consensusElementID || *stat.CaptainPoints > 2 {
			continue
		}
		if worst == nil ||
			*stat.CaptainPoints < *worst.CaptainPoints ||
			(*stat.CaptainPoints == *worst.CaptainPoints && stat.ManagerID < worst.ManagerID) {
			worst = stat
		}
	}
	if worst == nil {
		return nil, nil
	}

	return &notify.ArmbandOfShameAlert{
			Manager:          managerRef(managerByID, worst.ManagerID),
			Captain:          playerRef(playerByID, *worst.CaptainElementID),
			CaptainPoints:    *worst.CaptainPoints,
			ConsensusCaptain: playerRef(playerByID, consensusElementID),
			ConsensusPoints:  captainPoints[consensusElementID],
		}, &store.GameweekAward{
			LeagueID:        leagueID,
			EventID:         eventID,
			AwardKey:        awardArmbandOfShame,
			ManagerID:       worst.ManagerID,
			PlayerElementID: worst.CaptainElementID,
			MetricValue:     *worst.CaptainPoints,
		}
}

func biggestThrashingAward(leagueID int64, eventID int, managerByID map[int64]notify.ManagerRef, results []store.H2HResult) (*notify.MatchupAwardAlert, *store.GameweekAward) {
	var best *notify.MatchupAwardAlert
	for _, result := range results {
		winner := winnerID(result)
		if winner == nil {
			continue
		}
		alert := matchupAlert(managerByID, result)
		if best == nil ||
			alert.Margin > best.Margin ||
			(alert.Margin == best.Margin && alert.WinnerScore > best.WinnerScore) ||
			(alert.Margin == best.Margin && alert.WinnerScore == best.WinnerScore && alert.Winner.ID < best.Winner.ID) {
			best = alert
		}
	}
	if best == nil {
		return nil, nil
	}
	return best, &store.GameweekAward{
		LeagueID:          leagueID,
		EventID:           eventID,
		AwardKey:          awardBiggestThrashing,
		ManagerID:         best.Winner.ID,
		OpponentManagerID: int64Ptr(best.Loser.ID),
		MetricValue:       best.Margin,
	}
}

func luckiestWinAward(leagueID int64, eventID int, managerByID map[int64]notify.ManagerRef, results []store.H2HResult) (*notify.MatchupAwardAlert, *store.GameweekAward) {
	var best *notify.MatchupAwardAlert
	for _, result := range results {
		winner := winnerID(result)
		if winner == nil {
			continue
		}
		alert := matchupAlert(managerByID, result)
		if best == nil ||
			alert.WinnerScore < best.WinnerScore ||
			(alert.WinnerScore == best.WinnerScore && alert.Margin < best.Margin) ||
			(alert.WinnerScore == best.WinnerScore && alert.Margin == best.Margin && alert.Winner.ID < best.Winner.ID) {
			best = alert
		}
	}
	if best == nil {
		return nil, nil
	}
	return best, &store.GameweekAward{
		LeagueID:          leagueID,
		EventID:           eventID,
		AwardKey:          awardLuckiestWin,
		ManagerID:         best.Winner.ID,
		OpponentManagerID: int64Ptr(best.Loser.ID),
		MetricValue:       best.WinnerScore,
	}
}

func unluckiestLossAward(leagueID int64, eventID int, managerByID map[int64]notify.ManagerRef, results []store.H2HResult) (*notify.UnluckiestLossAlert, *store.GameweekAward) {
	scoreByManager := scoresByManager(results)

	var best *notify.UnluckiestLossAlert
	for _, result := range results {
		winner := winnerID(result)
		if winner == nil {
			continue
		}

		var candidate notify.UnluckiestLossAlert
		if *winner == result.Manager1ID {
			candidate = notify.UnluckiestLossAlert{
				Loser:         managerRef(managerByID, result.Manager2ID),
				LoserScore:    result.Manager2Score,
				Opponent:      managerRef(managerByID, result.Manager1ID),
				OpponentScore: result.Manager1Score,
				Margin:        result.Manager1Score - result.Manager2Score,
			}
		} else {
			candidate = notify.UnluckiestLossAlert{
				Loser:         managerRef(managerByID, result.Manager1ID),
				LoserScore:    result.Manager1Score,
				Opponent:      managerRef(managerByID, result.Manager2ID),
				OpponentScore: result.Manager2Score,
				Margin:        result.Manager2Score - result.Manager1Score,
			}
		}

		if !wouldBeatEveryoneElse(scoreByManager, candidate.Loser.ID, candidate.Opponent.ID, candidate.LoserScore) {
			continue
		}

		if best == nil ||
			candidate.LoserScore > best.LoserScore ||
			(candidate.LoserScore == best.LoserScore && candidate.Margin < best.Margin) ||
			(candidate.LoserScore == best.LoserScore && candidate.Margin == best.Margin && candidate.Loser.ID < best.Loser.ID) {
			best = &candidate
		}
	}
	if best == nil {
		return nil, nil
	}
	return best, &store.GameweekAward{
		LeagueID:          leagueID,
		EventID:           eventID,
		AwardKey:          awardUnluckiestLoss,
		ManagerID:         best.Loser.ID,
		OpponentManagerID: int64Ptr(best.Opponent.ID),
		MetricValue:       best.LoserScore,
	}
}

func matchupAlert(managerByID map[int64]notify.ManagerRef, result store.H2HResult) *notify.MatchupAwardAlert {
	if result.Manager1Score > result.Manager2Score {
		return &notify.MatchupAwardAlert{
			Winner:      managerRef(managerByID, result.Manager1ID),
			WinnerScore: result.Manager1Score,
			Loser:       managerRef(managerByID, result.Manager2ID),
			LoserScore:  result.Manager2Score,
			Margin:      result.Manager1Score - result.Manager2Score,
		}
	}
	return &notify.MatchupAwardAlert{
		Winner:      managerRef(managerByID, result.Manager2ID),
		WinnerScore: result.Manager2Score,
		Loser:       managerRef(managerByID, result.Manager1ID),
		LoserScore:  result.Manager1Score,
		Margin:      result.Manager2Score - result.Manager1Score,
	}
}

func wouldBeatEveryoneElse(scoreByManager map[int64]int, loserID int64, opponentID int64, loserScore int) bool {
	for managerID, score := range scoreByManager {
		if managerID == loserID || managerID == opponentID {
			continue
		}
		if loserScore <= score {
			return false
		}
	}
	return true
}

func captainDataComplete(managerStats []store.GameweekManagerStat) bool {
	if len(managerStats) == 0 {
		return false
	}
	for _, stat := range managerStats {
		if stat.CaptainElementID == nil || stat.CaptainPoints == nil || stat.CaptainMultiplier == nil {
			return false
		}
	}
	return true
}

func captainTotal(stat store.GameweekManagerStat) int {
	return *stat.CaptainPoints * *stat.CaptainMultiplier
}

func playerRef(playerByID map[int]notify.PlayerRef, elementID int) notify.PlayerRef {
	if player, ok := playerByID[elementID]; ok {
		return player
	}
	return notify.PlayerRef{ElementID: elementID}
}

func int64Ptr(v int64) *int64 {
	return &v
}
