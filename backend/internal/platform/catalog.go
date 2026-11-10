package platform

import "encoding/json"

type CatalogResource struct {
	ID    string `json:"id,omitempty"`
	Label string `json:"label"`
	X     int    `json:"x"`
	Y     int    `json:"y"`
}
type CatalogSetup struct {
	BaseGuests          int               `json:"base_guests"`
	MaxGuests           int               `json:"max_guests"`
	MinDurationMinutes  int               `json:"min_duration_minutes"`
	DurationStepMinutes int               `json:"duration_step_minutes"`
	StartStepMinutes    int               `json:"start_step_minutes"`
	BufferMinutes       int               `json:"buffer_minutes"`
	Schedule            json.RawMessage   `json:"schedule"`
	Tariffs             json.RawMessage   `json:"tariffs"`
	Resources           []CatalogResource `json:"resources"`
}

// CatalogDepartment is the versioned interservice metadata contract, not a shared repository.
type CatalogDepartment struct {
	ID          string        `json:"id"`
	BathID      int           `json:"bath_id"`
	BathName    string        `json:"bath_name"`
	BathAddress string        `json:"bath_address"`
	Name        string        `json:"name"`
	Mode        string        `json:"mode"`
	Active      bool          `json:"active"`
	Version     int64         `json:"version"`
	Setup       *CatalogSetup `json:"setup,omitempty"`
}
type CatalogApplied struct {
	DepartmentID string `json:"department_id"`
	Version      int64  `json:"version"`
	Error        string `json:"error,omitempty"`
}
