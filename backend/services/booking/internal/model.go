package booking

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
	_ "time/tzdata"
)

var Location = func() *time.Location {
	l, e := time.LoadLocation("Asia/Yekaterinburg")
	if e != nil {
		panic(e)
	}
	return l
}()

type Guest struct {
	Age   int `json:"age" validate:"min=0,max=120"`
	Count int `json:"count" validate:"min=1,max=100"`
}
type Buyer struct {
	Name  string `json:"name,omitempty" validate:"required,min=1,max=100"`
	Phone string `json:"phone,omitempty" validate:"required,min=10,max=32"`
}
type Selection struct {
	DepartmentID string    `json:"department_id" validate:"required,max=80"`
	ResourceIDs  []string  `json:"resource_ids" validate:"required,min=1,max=100,dive,max=100"`
	StartsAt     time.Time `json:"starts_at" validate:"required"`
	EndsAt       time.Time `json:"ends_at" validate:"required"`
	Guests       []Guest   `json:"guests" validate:"required,min=1,max=100,dive"`
}
type Resource struct {
	Version int64  `json:"version,omitempty"`
	Active  *bool  `json:"active,omitempty"`
	ID      string `json:"id"`
	Label   string `json:"label"`
	X       int    `json:"x"`
	Y       int    `json:"y"`
	State   string `json:"state,omitempty"`
}
type Slot struct {
	Start  string `json:"start"`
	End    string `json:"end"`
	Gender string `json:"gender"`
}
type Schedule struct {
	Week       map[string][]Slot `json:"week"`
	Exceptions map[string][]Slot `json:"exceptions"`
	Calendar   map[string]string `json:"calendar"`
}
type Tariff struct {
	ID              string   `json:"id"`
	ValidFrom       string   `json:"valid_from"`
	ValidUntil      string   `json:"valid_until"`
	DayTypes        []string `json:"day_types"`
	StartMinute     int      `json:"start_minute"`
	EndMinute       int      `json:"end_minute"`
	DurationMinutes int      `json:"duration_minutes"`
	Adult           int64    `json:"adult_kopecks"`
	Child           int64    `json:"child_kopecks"`
	Hourly          int64    `json:"hourly_kopecks"`
	ExtraHourly     int64    `json:"extra_hourly_kopecks"`
}
type Tariffs struct {
	Rules []Tariff `json:"rules"`
}
type Department struct {
	OnlineSalesEnabled  bool       `json:"online_sales_enabled"`
	SalesPausedUntil    *time.Time `json:"sales_paused_until"`
	SalesPauseReason    string     `json:"sales_pause_reason"`
	ServerTime          time.Time  `json:"-"`
	Archived            bool       `json:"archived"`
	CatalogVersion      int64      `json:"catalog_version"`
	PolicyVersion       int64      `json:"policy_version"`
	SchemeVersion       int64      `json:"scheme_version"`
	ID                  string     `json:"id"`
	BathID              int        `json:"bath_id"`
	BathName            string     `json:"bath_name"`
	BathAddress         string     `json:"bath_address"`
	Name                string     `json:"name"`
	Mode                string     `json:"mode"`
	BaseGuests          int        `json:"base_guests"`
	MaxGuests           int        `json:"max_guests"`
	MinDurationMinutes  int        `json:"min_duration_minutes"`
	DurationStepMinutes int        `json:"duration_step_minutes"`
	StartStepMinutes    int        `json:"start_step_minutes"`
	BufferMinutes       int        `json:"buffer_minutes"`
	Schedule            Schedule   `json:"schedule"`
	Tariffs             Tariffs    `json:"tariffs"`
	ScheduleVersion     int64      `json:"schedule_version"`
	TariffVersion       int64      `json:"tariff_version"`
}
type PriceLine struct {
	Description string `json:"description"`
	TariffID    string `json:"tariff_id"`
	Minutes     int    `json:"minutes"`
	Count       int    `json:"count"`
	Rate        int64  `json:"rate_kopecks"`
	Numerator   int64  `json:"numerator"`
	Denominator int64  `json:"denominator"`
}
type Quote struct {
	DepartmentID    string      `json:"department_id"`
	BathID          int         `json:"bath_id"`
	BathName        string      `json:"bath_name"`
	BathAddress     string      `json:"bath_address"`
	DepartmentName  string      `json:"department_name"`
	Mode            string      `json:"mode"`
	StartsAt        time.Time   `json:"starts_at"`
	EndsAt          time.Time   `json:"ends_at"`
	Resources       []Resource  `json:"resources"`
	Guests          []Guest     `json:"guests"`
	Gender          string      `json:"gender"`
	Lines           []PriceLine `json:"lines"`
	Amount          int64       `json:"amount_kopecks"`
	Currency        string      `json:"currency"`
	PriceHash       string      `json:"price_hash"`
	ScheduleVersion int64       `json:"schedule_version"`
	TariffVersion   int64       `json:"tariff_version"`
	Preliminary     bool        `json:"preliminary"`
}
type Order struct {
	Version          int64           `json:"version"`
	Payment          *PaymentSummary `json:"payment,omitempty"`
	AccessExpiresAt  time.Time       `json:"access_expires_at"`
	ContactsPurgedAt *time.Time      `json:"contacts_purged_at,omitempty"`
	ID               string          `json:"id"`
	DepartmentID     string          `json:"department_id"`
	Status           string          `json:"status"`
	Selection        Selection       `json:"selection"`
	Buyer            Buyer           `json:"buyer"`
	Quote            Quote           `json:"quote"`
	Amount           int64           `json:"amount_kopecks"`
	CreatedAt        time.Time       `json:"created_at"`
	HeldAt           *time.Time      `json:"held_at,omitempty"`
	ExpiresAt        *time.Time      `json:"expires_at,omitempty"`
	ConfirmedAt      *time.Time      `json:"confirmed_at,omitempty"`
	TicketExpiresAt  *time.Time      `json:"ticket_expires_at,omitempty"`
	AccessHash       string          `json:"-"`
	RequestHash      string          `json:"-"`
	TicketURL        string          `json:"ticket_url,omitempty"`
	ServerTime       time.Time       `json:"server_time"`
}

func capability(id, key, purpose string) string {
	h := hmac.New(sha256.New, []byte(key))
	h.Write([]byte(purpose + ":" + id))
	return hex.EncodeToString(h.Sum(nil))
}
func bytesJSON(v any) []byte { b, _ := json.Marshal(v); return b }
