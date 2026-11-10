package booking

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGuestCategories(t *testing.T) {
	for _, tc := range []struct {
		input      string
		age, count int
		valid      bool
	}{
		{`{"category":"adult","count":2}`, 18, 2, true},
		{`{"category":"child","age":12,"count":1}`, 12, 1, true},
		{`{"age":30,"count":1}`, 30, 1, true},
		{`{"category":"adult","age":30,"count":1}`, 0, 0, false},
		{`{"category":"child","count":1}`, 0, 0, false},
		{`{"category":"child","age":18,"count":1}`, 0, 0, false},
		{`{"count":1}`, 0, 0, false},
		{`{"category":"other","count":1}`, 0, 0, false},
		{`{"category":"adult","count":1,"name":"extra"}`, 0, 0, false},
	} {
		var g Guest
		err := json.Unmarshal([]byte(tc.input), &g)
		if !tc.valid {
			require.Error(t, err)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, tc.age, g.Age)
		require.Equal(t, tc.count, g.Count)
		b, err := json.Marshal(g)
		require.NoError(t, err)
		if g.Age >= 18 {
			require.NotContains(t, string(b), "age")
		}
	}
}
