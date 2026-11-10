/**
 * Типы ответов бекенда MVP (backend/api/openapi.yaml, версия 1.3.x).
 * Только те поля, которые использует фронтенд. Имена — как в контракте (snake_case).
 */

export interface WList<T> { items: T[]; limit: number; offset: number }

export interface WBath { id: number; name: string; address: string; description: string; version: number }

export type WMode = 'session' | 'sauna' | 'info';
export interface WDepartment { id: string; bath_id: number; name: string; mode: WMode; description: string; version: number }
export interface WDepartments { items: WDepartment[]; limit: number; offset: number }

export interface WGuest { category?: 'adult' | 'child'; age?: number; count: number }

export interface WSelection {
  department_id: string;
  resource_ids: string[];
  starts_at: string;
  ends_at: string;
  guests: WGuest[];
}

export type WResourceState = 'free' | 'held' | 'booked' | 'blocked';
export interface WResource { id: string; label: string; x: number; y: number; state?: WResourceState; version?: number; active?: boolean }
export interface WLayout { version: number; width: number; height: number; resources: WResource[] }

export type WGender = 'male' | 'female' | 'mixed';
export interface WSlot { start: string; end: string; gender: WGender }
export type WDayType = 'weekday' | 'weekend' | 'holiday';
export interface WSchedule {
  /** Ключ — день недели: 0 — воскресенье … 6 — суббота. */
  week: Record<string, WSlot[]>;
  /** Ключ — дата YYYY-MM-DD; пустой массив закрывает день. */
  exceptions: Record<string, WSlot[]>;
  calendar: Record<string, WDayType>;
}

export interface WTariff {
  id: string;
  valid_from: string;
  /** Исключительная верхняя граница. */
  valid_until: string;
  day_types: WDayType[];
  start_minute: number;
  end_minute: number;
  duration_minutes: number;
  adult_kopecks: number;
  child_kopecks: number;
  hourly_kopecks: number;
  extra_hourly_kopecks: number;
}

export interface WConfig {
  id: string;
  bath_id: number;
  name: string;
  mode: 'session' | 'sauna';
  base_guests: number;
  max_guests: number;
  schedule: WSchedule;
  tariffs: { rules: WTariff[] };
  schedule_version: number;
  tariff_version: number;
  min_duration_minutes: number;
  duration_step_minutes: number;
  start_step_minutes: number;
  buffer_minutes: number;
  archived: boolean;
  online_sales_enabled: boolean;
  sales_paused_until: string | null;
  sales_pause_reason: string;
}

export interface WAvailabilityItem { starts_at: string; ends_at: string; gender: string; resources: WResource[] }
export interface WAvailability { department_id: string; date: string; server_time: string; items: WAvailabilityItem[] }

export type WCalendarStatus = 'available' | 'sold_out' | 'closed' | 'out_of_sale' | 'unavailable';
export interface WCalendar { items: { date: string; status: WCalendarStatus; available_slots: number; free_resources: number }[] }

export interface WPriceLine { description: string; tariff_id: string; minutes: number; count: number; rate_kopecks: number; numerator: number; denominator: number }
export interface WQuote {
  department_id: string;
  bath_id: number;
  department_name: string;
  mode: string;
  starts_at: string;
  ends_at: string;
  resources: WResource[];
  guests: WGuest[];
  lines: WPriceLine[];
  amount_kopecks: number;
  currency: 'RUB';
  price_hash: string;
}

export type WOrderStatus = 'draft' | 'held' | 'confirmed' | 'released' | 'expired';
export interface WPaymentSummary { status: 'none' | 'pending' | 'accepted' | 'declined' | 'rejected'; paid_kopecks: number }
export interface WOrder {
  id: string;
  department_id: string;
  status: WOrderStatus;
  selection: WSelection;
  buyer: { name?: string; phone?: string };
  quote: WQuote;
  amount_kopecks: number;
  created_at: string;
  held_at?: string;
  expires_at?: string;
  confirmed_at?: string;
  ticket_url?: string;
  server_time: string;
  payment?: WPaymentSummary;
  version: number;
}
export interface WCreatedOrder { order: WOrder; order_token: string }
export interface WHeldOrder { order: WOrder; price_changed: boolean }

export type WAttemptStatus = 'pending' | 'accepted' | 'declined' | 'rejected';
export interface WAttempt { id: string; order_id: string; status: WAttemptStatus; reason: string }

export interface WTicket { order_id: string; status: 'confirmed'; paid_at: string; test_mode: true; warning: string; visit: WQuote }

export interface WSession { access_token: string; csrf_token: string; expires_at: string }
export interface WProfile { employee: { username: string; display_name: string; role: 'owner' | 'admin' } }

export interface WHistoryEntry { id: string; source: string; action: string; created_at: string }

export interface WBlock { id: string; department_id: string; resource_ids: string[]; starts_at: string; ends_at: string; reason: string; version: number; active: boolean }

/** details.resource_ids — у 409 resource_unavailable при удержании: недоступные ресурсы выбора (внутренние id, не номера мест). */
export interface WError { error: { code: string; message: string; details?: { resource_ids: string[] } } }
