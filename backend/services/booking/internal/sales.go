package booking

import (
	"encoding/json"
	"strings"
	"time"

	"banya.local/backend/internal/platform"
)

func salesEnabled(d Department, now time.Time) bool {
	return d.OnlineSalesEnabled || d.SalesPausedUntil != nil && !now.Before(*d.SalesPausedUntil)
}
func salesView(d Department, now time.Time) Department {
	if salesEnabled(d, now) {
		d.OnlineSalesEnabled = true
		d.SalesPausedUntil = nil
		d.SalesPauseReason = ""
	}
	return d
}
func requireSales(d Department, now time.Time) error {
	if d.Archived {
		return platform.Conflict("sales_closed", "Отделение архивировано; новое оформление закрыто")
	}
	if !salesEnabled(d, now) {
		return platform.Conflict("sales_paused", "Онлайн-оформление временно приостановлено")
	}
	return nil
}

type salesPatch struct {
	Enabled *bool           `json:"online_sales_enabled"`
	Until   json.RawMessage `json:"sales_paused_until"`
	Reason  *string         `json:"sales_pause_reason"`
}

func (p salesPatch) present() bool { return p.Enabled != nil || len(p.Until) > 0 || p.Reason != nil }
func applySalesPatch(d Department, p salesPatch, now time.Time) (Department, error) {
	d = salesView(d, now)
	if p.Enabled != nil {
		d.OnlineSalesEnabled = *p.Enabled
	}
	if len(p.Until) > 0 {
		if string(p.Until) == "null" {
			d.SalesPausedUntil = nil
		} else {
			var until time.Time
			if err := json.Unmarshal(p.Until, &until); err != nil || !until.After(now) {
				return d, platform.Bad("sales_paused_until должен быть будущим временем RFC3339 или null")
			}
			d.SalesPausedUntil = &until
		}
	}
	if p.Reason != nil {
		d.SalesPauseReason = strings.TrimSpace(*p.Reason)
	}
	if d.OnlineSalesEnabled {
		if len(p.Until) > 0 && d.SalesPausedUntil != nil || p.Reason != nil && d.SalesPauseReason != "" {
			return d, platform.Bad("Причина и срок приостановки задаются при online_sales_enabled=false")
		}
		d.SalesPausedUntil = nil
		d.SalesPauseReason = ""
	} else if !platform.SafeText(d.SalesPauseReason, 1, 500, false) {
		return d, platform.Bad("Для приостановки нужна публичная причина длиной 1–500 символов")
	}
	return d, nil
}
