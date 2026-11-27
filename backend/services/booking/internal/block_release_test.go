package booking

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRemainingBlocks(t *testing.T) {
	start := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	source := Block{ID: "source", DepartmentID: "department", ResourceIDs: []string{"58", "61"}, StartsAt: start, EndsAt: start.Add(4 * time.Hour), Reason: "Maintenance", Version: 7, Active: true}
	// Exhaustively check all non-empty resource subsets and hour-aligned cuts.
	for mask := 1; mask < 4; mask++ {
		for from := 0; from < 4; from++ {
			for until := from + 1; until <= 4; until++ {
				ids := []string{}
				for i, id := range source.ResourceIDs {
					if mask&(1<<i) != 0 {
						ids = append(ids, id)
					}
				}
				in := BlockRelease{Version: 7, ResourceIDs: ids, StartsAt: start.Add(time.Duration(from) * time.Hour), EndsAt: start.Add(time.Duration(until) * time.Hour)}
				pieces, err := remainingBlocks(source, in)
				require.NoError(t, err)
				require.LessOrEqual(t, len(pieces), 3)
				for i, id := range source.ResourceIDs {
					for hour := 0; hour < 4; hour++ {
						instant := start.Add(time.Duration(hour)*time.Hour + 30*time.Minute)
						count := 0
						for _, part := range pieces {
							for _, resource := range part.ResourceIDs {
								if resource == id && !instant.Before(part.StartsAt) && instant.Before(part.EndsAt) {
									count++
								}
							}
						}
						expected := 1
						if mask&(1<<i) != 0 && hour >= from && hour < until {
							expected = 0
						}
						require.Equal(t, expected, count, "resource=%s hour=%d mask=%d cut=%d..%d", id, hour, mask, from, until)
					}
				}
				seen := map[string]bool{}
				for _, piece := range pieces {
					require.NotEmpty(t, piece.ID)
					require.False(t, seen[piece.ID])
					seen[piece.ID] = true
					require.EqualValues(t, 1, piece.Version)
					require.True(t, piece.Active)
					require.Equal(t, source.Reason, piece.Reason)
					require.Equal(t, source.DepartmentID, piece.DepartmentID)
				}
			}
		}
	}
	require.Equal(t, []string{"58", "61"}, source.ResourceIDs)
	require.True(t, source.Active)
}

func TestRemainingBlocksRejectsExpansion(t *testing.T) {
	start := time.Date(2026, 11, 27, 12, 30, 0, 0, time.UTC)
	source := Block{ResourceIDs: []string{"58", "61"}, StartsAt: start, EndsAt: start.Add(time.Hour)}
	valid := BlockRelease{ResourceIDs: []string{"58"}, StartsAt: start, EndsAt: source.EndsAt}
	invalid := []BlockRelease{valid, valid, valid, valid, valid, valid, valid}
	invalid[0].ResourceIDs = nil
	invalid[1].ResourceIDs = []string{"58", "58"}
	invalid[2].ResourceIDs = []string{"unknown"}
	invalid[3].StartsAt = start.Add(-time.Minute)
	invalid[4].EndsAt = source.EndsAt.Add(time.Minute)
	invalid[5].EndsAt = start
	invalid[6].StartsAt = start.Add(time.Nanosecond)
	for _, in := range invalid {
		_, err := remainingBlocks(source, in)
		require.Error(t, err)
	}
}
