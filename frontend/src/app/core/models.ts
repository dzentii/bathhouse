/**
 * Логическая модель данных по ФЗ (раздел II).
 * Поля и названия состояний — предварительные: окончательный контракт придёт вместе с бекендом.
 * Даты — 'YYYY-MM-DD', время — 'HH:mm' по Екатеринбургу; деньги — целые копейки.
 */

export type Kopecks = number;
export type VisitMode = 'session' | 'sauna' | 'info';
export type Gender = 'male' | 'female';

export interface Banya {
  id: string;
  name: string;
  address: string;
  shortAddress: string;
  description: string;
  phone?: string;
}

export interface Department {
  id: string;
  banyaId: string;
  name: string;
  mode: VisitMode;
  description: string;
  tags: string[];
  gender?: Gender;
  /** Сеансовый зал: цена места от. Сауна: цена часа от. */
  priceFrom?: Kopecks;
  /** Сауна: гостей включено в цену и безопасная вместимость (могут быть не согласованы). */
  baseGuests?: number | null;
  maxGuests?: number | null;
  rules: string[];
  /** Справочный режим. */
  info?: { schedule: string; priceOnSite: string; howToBuy: string };
  seatsTotal?: number;
}

export interface SeatZone { name: string; seats: string[] }
/** Места с координатами 0–1000 из готовой схемы зала (если сервер их отдаёт). */
export interface SeatPlan { width: number; height: number; seats: { label: string; x: number; y: number }[] }
export interface SeatScheme { version: string; zones: SeatZone[]; plan?: SeatPlan }

export interface SessionSlot {
  id: string;
  start: string;
  end: string;
  free: number;
}

export interface DateAvailability {
  date: string;
  hasSessions: boolean;
}

export type SeatState = 'free' | 'occupied' | 'blocked';
export interface SessionAvailability {
  scheme: SeatScheme;
  seats: Record<string, SeatState>;
}

export interface SaunaDay {
  open: number;   // час открытия
  close: number;  // час закрытия (24 = полночь)
  busy: number[]; // занятые часы, включая перерывы
  durations: number[];
}

export interface TariffLine { label: string; weekday: string; weekend: string }

export interface DepartmentDetails {
  department: Department;
  banya: Banya;
  scheduleRows: { days: string; value: string }[];
  tariffRows: TariffLine[];
}

/* ---------- Заказ ---------- */

export interface SessionSelection {
  kind: 'session';
  banyaId: string;
  departmentId: string;
  date: string;
  sessionId: string;
  start: string;
  end: string;
  seats: string[];
}

export interface SaunaSelection {
  kind: 'sauna';
  banyaId: string;
  departmentId: string;
  date: string;
  start: string;
  hours: number;
  end: string;
}

export type Selection = SessionSelection | SaunaSelection;

export interface Guests {
  adults: number;
  /** Возраст каждого ребёнка в полных годах на дату посещения; null — ещё не указан. От возраста зависят тариф и место. */
  children: (number | null)[];
}

export interface Buyer { name: string; phone: string }

export interface QuoteLine { label: string; amount: Kopecks }
export interface Quote { lines: QuoteLine[]; total: Kopecks; tariffName: string }

/** draft — черновик без удержания (бывает после конфликта при переходе к оплате). */
export type OrderStatus = 'draft' | 'held' | 'confirmed' | 'declined' | 'expired' | 'released' | 'pending';

export interface OrderView {
  id: string;
  status: OrderStatus;
  banya: Banya;
  department: Department;
  selection: Selection;
  guests: Guests;
  buyer: Buyer;
  quote: Quote;
  /** Сумма предварительного расчёта, если при удержании она изменилась. */
  previousTotal?: Kopecks;
  /** Покупатель явно подтвердил изменившуюся сумму. */
  newTotalConfirmed?: boolean;
  holdExpiresAt?: string; // ISO, задаёт сервер
  paidAt?: string;
  createdAt: string;
  ticketToken?: string;
  history: { at: string; text: string }[];
}

export interface HoldResult {
  order: OrderView;
  /** Секрет доступа покупателя к своему оформлению (номер заказа сам по себе доступа не даёт). */
  accessToken: string;
}

/** 'lost' — тест потери ответа: сервер сохранил успех, но клиент ответа не получил. */
export type TestPaymentOutcome = 'success' | 'decline' | 'lost';

/** Ошибка API в едином виде для интерфейса. */
export interface ApiError {
  code: 'conflict' | 'declined' | 'validation' | 'not_found' | 'expired' | 'forbidden' | 'unauthorized' | 'network' | 'unknown';
  message: string;
  field?: string;
  conflictSeats?: string[];
}

/* ---------- Личный кабинет покупателя ---------- */
/* Выходит за рамки ТЗ v0.2: кабинет необязательный, вход по телефону и коду из SMS. */

export interface CustomerProfile {
  id: string;
  phone: string;
  phoneVerified: boolean;
  firstName: string;
  lastName?: string;
  email?: string;
  notify: { smsReminder: boolean; emailTicket: boolean; news: boolean };
  consentAt: string; // ISO — согласие на обработку персональных данных
}

export type ProfilePatch = Partial<Pick<CustomerProfile, 'firstName' | 'lastName' | 'email' | 'notify'>>;

export interface LoginCodeRequest {
  /** Через сколько секунд можно запросить код повторно. */
  resendInSec: number;
  /** Только в демо-режиме: SMS не отправляются, код показывается на экране. */
  demoCode?: string;
}

export interface CustomerSession { token: string; profile: CustomerProfile }

/* ---------- Админка ---------- */

export interface AdminOrderFilter { banyaId?: string; departmentId?: string; from?: string; to?: string; status?: OrderStatus }

export interface WeekScheduleDay {
  date: string;
  dayType?: 'Мужской' | 'Женский';
  sessions: { id: string; start: string; end: string; cancelled?: boolean }[];
}

export interface ScheduleException { date: string; text: string; conflict?: boolean }

export interface TariffRule {
  id: string;
  departmentId: string;
  condition: string;
  days: string;
  time: string;
  from: string;
  to?: string;
  price: Kopecks;
  unit: 'место' | 'час' | 'гость';
}

export interface AdminSeatState { label: string; state: 'free' | 'sold' | 'blocked'; orderId?: string }
