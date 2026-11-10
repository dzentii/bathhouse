package identity

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuditStateContainsOnlyPermissions(t *testing.T) {
	state := auditState(Employee{ID: "private-id", Username: "private-login", DisplayName: "private-name", Role: "admin", Active: true, Version: 2})
	require.Len(t, state, 3)
	data, err := json.Marshal(state)
	require.NoError(t, err)
	require.NotContains(t, string(data), "private-")
	require.Contains(t, string(data), "admin")
	require.Nil(t, nullableUUID(""))
	require.Equal(t, "id", nullableUUID("id"))
}
