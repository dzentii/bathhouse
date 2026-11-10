import { Injectable } from '@angular/core';
import { Observable, defer, delay, of, throwError } from 'rxjs';
import { BookingApi } from './booking-api';
import {
  AdminOrderFilter, AdminSeatState, ApiError, Banya, Buyer, DateAvailability, Department, DepartmentDetails,
  CustomerProfile, CustomerSession, Guests, HoldResult, LoginCodeRequest, OrderView, ProfilePatch, Quote, QuoteLine, SaunaDay, SaunaSelection, ScheduleException, SeatScheme,
  SeatState, Selection, SessionAvailability, SessionSelection, SessionSlot, TariffRule, TestPaymentOutcome,
  WeekScheduleDay,
} from '../models';
import { TIME_ZONE, addDays, dayMonthShort, formatMoney, hh, plural, sortSeats, todayEkb } from '../format';

/*
 * ВРЕМЕННАЯ демо-реализация BookingApi — до подключения Go-бекенда.
 * Все данные синтетические (вымышленные имена, тестовый телефон +79000000000, условные цены).
 * Правила (удержание 10 минут, «всё или ничего», фиксация суммы, идемпотентность)
 * имитируются здесь только для демонстрации интерфейса; источником истины будет сервер.
 */

const HOLD_MS = 10 * 60 * 1000;
const SALE_DAYS = 7;
const STORAGE_KEY = 'sb-demo-state-v3';
// Возрастные границы демо-тарифа повторяют решения бекенда MVP; в рабочем режиме их применяет сервер.
const DEMO_FREE_AGE = 3;
const DEMO_SEAT_AGE = 8;
const DEMO_SAUNA_FREE_AGE = 12;
const CODE_TTL_MS = 5 * 60 * 1000;
const CODE_RESEND_SEC = 60;
const CODE_MAX_ATTEMPTS = 5;

const BANYAS: Banya[] = [
  {
    id: 'banya-1', name: 'Баня №1', address: 'Екатеринбург, ул. Первомайская, 71', shortAddress: 'ул. Первомайская, 71',
    description: 'Общие отделения и купели с местами на сеанс, пять саун для своей компании и мужское отделение первого этажа с покупкой билета на месте.',
  },
  {
    id: 'banya-2', name: 'Баня №2', address: 'Екатеринбург, ул. Лыжников, 38а', shortAddress: 'ул. Лыжников, 38а',
    description: 'Мужское и женское общие отделения, две сауны целиком и душевые, в которые билет покупается на месте.',
  },
];

const SESSION_RULES = [
  'Вы выбираете конкретный сеанс и места на схеме зала.',
  'Окончание сеанса фиксировано: при опоздании оно не сдвигается.',
  'Переодевание входит во время сеанса, дополнительного времени после окончания нет.',
];
const SAUNA_RULES = [
  'Сауна предоставляется целиком на выбранный интервал — только для вашей компании.',
  'Стоимость оплачивается полностью, без предоплаты и доплаты.',
  'Окончание аренды фиксировано.',
];

function dept(p: Partial<Department> & Pick<Department, 'id' | 'banyaId' | 'name' | 'mode'>): Department {
  return { description: '', tags: [], rules: [], ...p };
}

const DEPARTMENTS: Department[] = [
  dept({ id: 'b1-male', banyaId: 'banya-1', name: 'Мужское общее', mode: 'session', gender: 'male', tags: ['Мужское', 'Сеанс 2 ч'], description: 'Общее мужское отделение с парной и зоной отдыха. Места выбираются по схеме зала.', rules: SESSION_RULES }),
  dept({ id: 'b1-female', banyaId: 'banya-1', name: 'Женское общее', mode: 'session', gender: 'female', tags: ['Женское', 'Сеанс 2 ч'], description: 'Общее женское отделение с парной и зоной отдыха. Места выбираются по схеме зала.', rules: SESSION_RULES }),
  dept({ id: 'b1-male-kupel', banyaId: 'banya-1', name: 'Мужская Купель', mode: 'session', gender: 'male', tags: ['Мужское', 'Купель'], description: 'Мужское отделение с купелью. Места выбираются по схеме зала.', rules: SESSION_RULES }),
  dept({ id: 'b1-female-kupel', banyaId: 'banya-1', name: 'Женская Купель', mode: 'session', gender: 'female', tags: ['Женское', 'Купель'], description: 'Женское отделение с купелью. Места выбираются по схеме зала.', rules: SESSION_RULES }),
  dept({ id: 'b1-sauna-pool-1', banyaId: 'banya-1', name: 'Сауна с бассейном №1', mode: 'sauna', tags: ['Бассейн'], baseGuests: 6, maxGuests: 10, description: 'Сауна с бассейном, арендуется целиком.', rules: SAUNA_RULES }),
  dept({ id: 'b1-sauna-pool-2', banyaId: 'banya-1', name: 'Сауна с бассейном №2', mode: 'sauna', tags: ['Бассейн'], baseGuests: 6, maxGuests: 10, description: 'Сауна с бассейном, арендуется целиком.', rules: SAUNA_RULES }),
  dept({ id: 'b1-sauna-pool-3', banyaId: 'banya-1', name: 'Третья сауна с бассейном', mode: 'sauna', tags: ['Бассейн', 'Название уточняется'], baseGuests: 6, maxGuests: 10, description: 'Сауна с бассейном, арендуется целиком. Публичное название уточняется.', rules: SAUNA_RULES }),
  dept({ id: 'b1-sauna-2fl', banyaId: 'banya-1', name: 'Сауна без бассейна · 2 этаж', mode: 'sauna', tags: ['2 этаж'], baseGuests: 4, maxGuests: 6, description: 'Сауна без бассейна на втором этаже, арендуется целиком.', rules: SAUNA_RULES }),
  dept({ id: 'b1-sauna-3fl', banyaId: 'banya-1', name: 'Сауна без бассейна · 3 этаж', mode: 'sauna', tags: ['3 этаж'], baseGuests: 4, maxGuests: 6, description: 'Сауна без бассейна на третьем этаже, арендуется целиком.', rules: SAUNA_RULES }),
  dept({ id: 'b1-male-1fl', banyaId: 'banya-1', name: 'Мужское отделение 1 этажа', mode: 'info', gender: 'male', tags: ['Мужское'], description: 'Отделение работает без предварительной продажи: билет покупается в кассе в день посещения.', info: { schedule: 'Режим работы уточняется', priceOnSite: 'Стоимость — в кассе бани', howToBuy: 'Касса у входа в Баню №1' } }),

  dept({ id: 'b2-male', banyaId: 'banya-2', name: 'Мужское общее', mode: 'session', gender: 'male', tags: ['Мужское', 'Сеанс 2 ч'], description: 'Общее мужское отделение. Места выбираются по схеме зала.', rules: SESSION_RULES }),
  dept({ id: 'b2-female', banyaId: 'banya-2', name: 'Женское общее', mode: 'session', gender: 'female', tags: ['Женское', 'Сеанс 2 ч'], description: 'Общее женское отделение. Места выбираются по схеме зала.', rules: SESSION_RULES }),
  dept({ id: 'b2-sauna-pool', banyaId: 'banya-2', name: 'Сауна с бассейном', mode: 'sauna', tags: ['Бассейн'], baseGuests: null, maxGuests: null, description: 'Сауна с бассейном, арендуется целиком. Число гостей в базовой цене уточняется (5 или 6).', rules: SAUNA_RULES }),
  dept({ id: 'b2-sauna', banyaId: 'banya-2', name: 'Сауна без бассейна', mode: 'sauna', tags: [], baseGuests: 4, maxGuests: 6, description: 'Сауна без бассейна, арендуется целиком.', rules: SAUNA_RULES }),
  dept({ id: 'b2-shower-big', banyaId: 'banya-2', name: 'Большой душ', mode: 'info', tags: ['10 кабинок'], description: 'Душевые на 10 кабинок. Бронировать заранее не нужно.', info: { schedule: 'Режим работы уточняется', priceOnSite: 'Стоимость — в кассе бани', howToBuy: 'Касса у входа в Баню №2' } }),
  dept({ id: 'b2-shower-small', banyaId: 'banya-2', name: 'Малый душ', mode: 'info', tags: ['5 кабинок'], description: 'Душевые на 5 кабинок. Бронировать заранее не нужно.', info: { schedule: 'Режим работы уточняется', priceOnSite: 'Стоимость — в кассе бани', howToBuy: 'Касса у входа в Баню №2' } }),
];

const SESSION_TIMES: [string, string][] = [['08:00', '10:00'], ['10:30', '12:30'], ['13:00', '15:00'], ['15:30', '17:30'], ['18:00', '20:00'], ['20:30', '22:30']];

function range(a: number, b: number, skip: number[] = []): string[] {
  const r: string[] = [];
  for (let i = a; i <= b; i++) if (!skip.includes(i)) r.push(String(i));
  return r;
}

function schemeFor(d: Department): SeatScheme {
  if (d.id.includes('kupel')) {
    return { version: 'v1', zones: [{ name: 'У купели', seats: range(1, 6) }, { name: 'Зона отдыха', seats: range(7, 12) }] };
  }
  if (d.banyaId === 'banya-2') {
    return { version: 'v1', zones: [{ name: 'Вдоль стены', seats: range(1, 10) }, { name: 'Центральные лавки', seats: range(11, 20) }] };
  }
  return { version: 'v3', zones: [
    { name: 'Вдоль стены', seats: range(1, 8) },
    { name: 'Центральные лавки', seats: range(9, 18, [13]) },
    { name: 'У бассейна', seats: range(19, 26) },
  ] };
}

for (const d of DEPARTMENTS) {
  if (d.mode === 'session') d.seatsTotal = schemeFor(d).zones.reduce((n, z) => n + z.seats.length, 0);
}

/* ---------- детерминированный «шум» занятости ---------- */
function hash(s: string): number {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) { h ^= s.charCodeAt(i); h = Math.imul(h, 16777619); }
  return h >>> 0;
}
function rnd(seed: string): () => number {
  let a = hash(seed);
  return () => {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function isWeekend(date: string): boolean {
  const wd = new Date(date + 'T12:00:00Z').getUTCDay();
  return wd === 0 || wd === 6;
}

function nowEkbMinutes(): number {
  const p = new Intl.DateTimeFormat('en-GB', { timeZone: TIME_ZONE, hour: '2-digit', minute: '2-digit', hour12: false }).formatToParts(new Date());
  const h = Number(p.find((x) => x.type === 'hour')?.value ?? 0) % 24;
  const m = Number(p.find((x) => x.type === 'minute')?.value ?? 0);
  return h * 60 + m;
}
function toMin(t: string): number { const [h, m] = t.split(':').map(Number); return h * 60 + m; }

/* ---------- состояние демо-«сервера» ---------- */

interface StoredOrder extends OrderView {
  accessToken: string;
  /** Аккаунт, из которого оформлен заказ. */
  customerId?: string;
  /** Телефон покупателя: заказы на подтверждённый номер видны в кабинете. */
  ownerPhone?: string;
}
interface StoredCustomer extends CustomerProfile { deleted?: boolean }
interface State {
  orders: StoredOrder[];
  blocks: { departmentId: string; date: string; sessionId: string; seat: string; reason: string }[];
  cancelled: { departmentId: string; date: string; sessionId: string }[];
  tariffs: TariffRule[];
  idem: Record<string, unknown>;
  seq: number;
  customers: StoredCustomer[];
  customerSessions: Record<string, string>; // token → customerId
  loginCodes: Record<string, { code: string; expiresAt: number; sentAt: number; attempts: number }>;
}

function defaultTariffs(): TariffRule[] {
  const rules: TariffRule[] = [];
  let n = 1;
  for (const d of DEPARTMENTS) {
    const add = (condition: string, days: string, price: number, unit: TariffRule['unit']) =>
      rules.push({ id: `t${n++}`, departmentId: d.id, condition, days, time: 'Весь день', from: '2026-09-01', price, unit });
    if (d.mode === 'session') {
      const base = d.id.includes('kupel') ? 1800_00 : 1500_00;
      add('Взрослый', 'Пн–Пт', base, 'место');
      add('Взрослый', 'Сб–Вс', base + 300_00, 'место');
      add('Ребёнок с местом', 'Все дни', Math.round(base * 0.6), 'место');
      add('Ребёнок без места', 'Все дни', 0, 'гость');
    } else if (d.mode === 'sauna') {
      const hour = d.tags.includes('Бассейн') ? 3000_00 : 2200_00;
      add('Аренда, час', 'Пн–Пт', hour, 'час');
      add('Аренда, час', 'Сб–Вс', hour + 500_00, 'час');
      add('Гость сверх базы', 'Все дни', 400_00, 'гость');
    }
  }
  return rules;
}

function newToken(bytes = 24): string {
  const b = crypto.getRandomValues(new Uint8Array(bytes));
  return [...b].map((x) => x.toString(16).padStart(2, '0')).join('');
}

function err(code: ApiError['code'], message: string, extra: Partial<ApiError> = {}): Observable<never> {
  return throwError(() => ({ code, message, ...extra } satisfies ApiError)).pipe(delay(250));
}

@Injectable()
export class DemoBookingApi extends BookingApi {
  readonly isDemo = true;
  readonly supportsCabinet = true;
  ticketPdfUrl(): string | null { return null; }
  private state: State = this.load();
  private adminTokens = new Set<string>();

  /* --- хранение --- */
  private load(): State {
    try {
      const raw = localStorage.getItem(STORAGE_KEY);
      if (raw) return JSON.parse(raw) as State;
    } catch { /* приватный режим и т.п. */ }
    const s: State = { orders: [], blocks: [], cancelled: [], tariffs: defaultTariffs(), idem: {}, seq: 120, customers: [], customerSessions: {}, loginCodes: {} };
    this.state = s;
    this.seedOrders(s);
    return s;
  }
  private save(): void {
    try { localStorage.setItem(STORAGE_KEY, JSON.stringify(this.state)); } catch { /* недоступно */ }
  }
  private ok<T>(value: T, ms = 300): Observable<T> {
    return defer(() => of(structuredClone(value))).pipe(delay(ms));
  }

  private seedOrders(s: State): void {
    const today = todayEkb();
    const push = (o: Omit<StoredOrder, 'id' | 'accessToken' | 'history' | 'holdExpiresAt' | 'paidAt' | 'ticketToken' | 'createdAt'>, createdOffsetDays: number) => {
      s.seq++;
      const created = new Date(Date.now() + createdOffsetDays * 86400_000 - 3 * 3600_000).toISOString();
      s.orders.push({
        ...o, id: `SB-${String(s.seq).padStart(6, '0')}`, createdAt: created,
        paidAt: o.status === 'confirmed' ? created : undefined,
        ticketToken: o.status === 'confirmed' ? newToken() : undefined,
        holdExpiresAt: new Date(Date.parse(created) + HOLD_MS).toISOString(),
        history: [{ at: created, text: 'Переход к оплате · удержание создано, сумма зафиксирована' }],
        accessToken: newToken(),
      });
    };
    // Заказы демо-покупателя «Иван Петров» (тестовый номер +79000000000): сегодня, предстоящие и история.
    const ivanBuyer = { name: 'Иван Петров', phone: '+79000000000' };
    const ivan = (deptId: string, dayOffset: number, sessionIdx: number, seats: string[], guests: Guests) => {
      const d = DEPARTMENTS.find((x) => x.id === deptId)!;
      const [start, end] = SESSION_TIMES[sessionIdx];
      const selection: SessionSelection = { kind: 'session', banyaId: d.banyaId, departmentId: d.id, date: addDays(today, dayOffset), sessionId: `s${sessionIdx}`, start, end, seats };
      push({ status: 'confirmed', banya: BANYAS.find((b) => b.id === d.banyaId)!, department: d, selection, guests,
        buyer: ivanBuyer, quote: this.calcQuote(selection, guests, s.tariffs), ownerPhone: ivanBuyer.phone }, Math.min(0, dayOffset) - 2);
    };
    const ivanSauna = (deptId: string, dayOffset: number, startH: number, hours: number, guests: Guests) => {
      const d = DEPARTMENTS.find((x) => x.id === deptId)!;
      const selection: SaunaSelection = { kind: 'sauna', banyaId: d.banyaId, departmentId: d.id, date: addDays(today, dayOffset), start: hh(startH), hours, end: hh(startH + hours) };
      push({ status: 'confirmed', banya: BANYAS.find((b) => b.id === d.banyaId)!, department: d, selection, guests,
        buyer: ivanBuyer, quote: this.calcQuote(selection, guests, s.tariffs), ownerPhone: ivanBuyer.phone }, Math.min(0, dayOffset) - 2);
    };
    ivan('b2-male', -43, 0, ['9', '10'], { adults: 2, children: [5] });
    ivanSauna('b1-sauna-2fl', -29, 19, 3, { adults: 4, children: [] });
    ivan('b1-male-kupel', -15, 1, ['3'], { adults: 1, children: [] });
    ivanSauna('b2-sauna-pool', -8, 14, 2, { adults: 6, children: [] });
    ivan('b1-male', 0, 5, ['7', '8'], { adults: 2, children: [5] });
    ivanSauna('b1-sauna-pool-1', 4, 14, 3, { adults: 5, children: [] });
    ivan('b1-male-kupel', 6, 2, ['4'], { adults: 1, children: [] });

    const mk = (deptId: string, dayOffset: number, sessionIdx: number, seats: string[], status: OrderView['status'], name: string) => {
      const d = DEPARTMENTS.find((x) => x.id === deptId)!;
      const date = addDays(today, dayOffset);
      const [start, end] = SESSION_TIMES[sessionIdx];
      const selection: SessionSelection = { kind: 'session', banyaId: d.banyaId, departmentId: d.id, date, sessionId: `s${sessionIdx}`, start, end, seats };
      const guests: Guests = { adults: seats.length, children: [] };
      const quote = this.calcQuote(selection, guests, s.tariffs);
      s.seq++;
      const created = new Date(Date.now() - (3 - dayOffset) * 3600_000).toISOString();
      s.orders.push({
        id: `SB-${String(s.seq).padStart(6, '0')}`, status, banya: BANYAS.find((b) => b.id === d.banyaId)!, department: d,
        selection, guests, buyer: { name, phone: '+79000000000' }, quote, createdAt: created,
        paidAt: status === 'confirmed' ? created : undefined,
        ticketToken: status === 'confirmed' ? newToken() : undefined,
        holdExpiresAt: new Date(Date.parse(created) + HOLD_MS).toISOString(),
        history: [{ at: created, text: 'Переход к оплате · места удержаны, сумма зафиксирована' }],
        accessToken: newToken(),
      });
    };
    mk('b1-male', 0, 4, ['14', '15'], 'confirmed', 'Глеб Орлов');
    mk('b1-male', 1, 1, ['6'], 'confirmed', 'Олег Смирнов');
    mk('b1-female', 1, 2, ['3', '4', '5'], 'confirmed', 'Анна Котова');
    mk('b2-male', 2, 0, ['9'], 'declined', 'Пётр Волков');
    mk('b1-male-kupel', 2, 3, ['2'], 'expired', 'Сергей Лосев');
    mk('b2-female', 3, 4, ['1', '2'], 'confirmed', 'Мария Белова');
  }

  /* --- вспомогательное --- */
  private dept(id: string): Department | undefined { return DEPARTMENTS.find((d) => d.id === id); }
  private banya(id: string): Banya | undefined { return BANYAS.find((b) => b.id === id); }

  private refreshExpiry(): void {
    const now = Date.now();
    let changed = false;
    for (const o of this.state.orders) {
      if (o.status === 'held' && o.holdExpiresAt && Date.parse(o.holdExpiresAt) <= now) {
        o.status = 'expired';
        o.history.push({ at: o.holdExpiresAt, text: 'Удержание истекло · ресурсы освобождены' });
        changed = true;
      }
    }
    if (changed) this.save();
  }

  /** Заказы, которые занимают ресурсы: подтверждённые и действующие удержания. */
  private activeOrders(): StoredOrder[] {
    this.refreshExpiry();
    return this.state.orders.filter((o) => o.status === 'confirmed' || o.status === 'held');
  }

  private saleDates(): string[] {
    const t = todayEkb();
    return Array.from({ length: SALE_DAYS }, (_, i) => addDays(t, i));
  }

  private sanitaryDay(departmentId: string): string {
    const idx = 2 + (hash(departmentId) % 5);
    return addDays(todayEkb(), idx);
  }

  private sessionsFor(departmentId: string, date: string): SessionSlot[] {
    if (date === this.sanitaryDay(departmentId) && hash(departmentId) % 2 === 0) return [];
    const isToday = date === todayEkb();
    const nowMin = nowEkbMinutes();
    const d = this.dept(departmentId)!;
    return SESSION_TIMES
      .map(([start, end], i) => ({ id: `s${i}`, start, end }))
      .filter((s) => !(isToday && toMin(s.start) <= nowMin))
      .filter((s) => !this.state.cancelled.some((c) => c.departmentId === departmentId && c.date === date && c.sessionId === s.id))
      .map((s) => {
        const seats = this.seatStates(d, date, s.id);
        return { ...s, free: Object.values(seats).filter((x) => x === 'free').length };
      });
  }

  private seatStates(d: Department, date: string, sessionId: string): Record<string, SeatState> {
    const scheme = schemeFor(d);
    const r = rnd(`${d.id}|${date}|${sessionId}`);
    const fullness = r() < 0.15 ? 1 : r() * 0.45;
    const res: Record<string, SeatState> = {};
    for (const z of scheme.zones) for (const s of z.seats) res[s] = r() < fullness ? 'occupied' : 'free';
    for (const o of this.activeOrders()) {
      const sel = o.selection;
      if (sel.kind === 'session' && sel.departmentId === d.id && sel.date === date && sel.sessionId === sessionId) {
        for (const s of sel.seats) res[s] = 'occupied';
      }
    }
    for (const b of this.state.blocks) {
      if (b.departmentId === d.id && b.date === date && b.sessionId === sessionId && res[b.seat] === 'free') res[b.seat] = 'blocked';
    }
    return res;
  }

  private saunaDay(departmentId: string, date: string): SaunaDay {
    const r = rnd(`${departmentId}|${date}`);
    const busy = new Set<number>();
    const blocks = Math.floor(r() * 3);
    for (let i = 0; i < blocks; i++) {
      const s = 10 + Math.floor(r() * 12);
      busy.add(s); busy.add(s + 1);
    }
    if (date === todayEkb()) {
      const nowH = Math.floor(nowEkbMinutes() / 60);
      for (let h = 10; h <= nowH; h++) busy.add(h);
    }
    for (const o of this.activeOrders()) {
      const sel = o.selection;
      if (sel.kind === 'sauna' && sel.departmentId === departmentId && sel.date === date) {
        const st = Number(sel.start.slice(0, 2));
        for (let h = st; h < st + sel.hours; h++) busy.add(h);
      }
    }
    return { open: 10, close: 24, busy: [...busy].filter((h) => h >= 10 && h < 24).sort((a, b) => a - b), durations: [2, 3, 4, 5] };
  }

  private price(tariffs: TariffRule[], departmentId: string, condition: string, date: string): number | null {
    const dayKey = isWeekend(date) ? 'Сб–Вс' : 'Пн–Пт';
    const rule = tariffs
      .filter((t) => t.departmentId === departmentId && t.condition === condition && (t.days === dayKey || t.days === 'Все дни'))
      .filter((t) => t.from <= date && (!t.to || t.to >= date))
      .sort((a, b) => b.from.localeCompare(a.from))[0];
    return rule ? rule.price : null;
  }

  private calcQuote(sel: Selection, g: Guests, tariffs = this.state.tariffs): Quote {
    const lines: QuoteLine[] = [];
    const missing = () => { throw { code: 'validation', message: 'Для выбранной даты нет действующего тарифа. Продажа не подтверждается.' } satisfies ApiError; };
    if (sel.kind === 'session') {
      const adult = this.price(tariffs, sel.departmentId, 'Взрослый', sel.date) ?? missing();
      const kid = this.price(tariffs, sel.departmentId, 'Ребёнок с местом', sel.date) ?? missing();
      const kidNo = this.price(tariffs, sel.departmentId, 'Ребёнок без места', sel.date) ?? 0;
      // Демо-правила как у бекенда: до 3 лет — бесплатно, 3–7 — детский без места, с 8 лет — с местом.
      const ages = g.children.map((a) => a ?? 0);
      const noSeatPaid = ages.filter((a) => a >= DEMO_FREE_AGE && a < DEMO_SEAT_AGE).length;
      const noSeatFree = ages.filter((a) => a < DEMO_FREE_AGE).length;
      sortSeats(sel.seats).forEach((s, i) => {
        const isAdult = i < g.adults;
        lines.push({ label: `Место ${s} · ${isAdult ? 'взрослый' : 'ребёнок от 8 лет'}`, amount: isAdult ? adult : kid });
      });
      if (noSeatPaid) lines.push({ label: `Ребёнок 3–7 лет без места × ${noSeatPaid}`, amount: kidNo * noSeatPaid });
      if (noSeatFree) lines.push({ label: `Ребёнок до 3 лет × ${noSeatFree}`, amount: 0 });
      return { lines, total: lines.reduce((s, l) => s + l.amount, 0), tariffName: isWeekend(sel.date) ? 'Выходной день' : 'Будний день' };
    }
    const d = this.dept(sel.departmentId)!;
    const hour = this.price(tariffs, sel.departmentId, 'Аренда, час', sel.date) ?? missing();
    lines.push({ label: `Аренда · ${sel.hours} ч`, amount: hour * sel.hours });
    // В тарифную базу сауны входят гости от 13 лет.
    const guests = g.adults + g.children.filter((a) => (a ?? 0) > DEMO_SAUNA_FREE_AGE).length;
    if (d.baseGuests != null && guests > d.baseGuests) {
      const extra = this.price(tariffs, sel.departmentId, 'Гость сверх базы', sel.date) ?? missing();
      const n = guests - d.baseGuests;
      lines.push({ label: `Гости сверх базы × ${n}`, amount: extra * n });
    }
    return { lines, total: lines.reduce((s, l) => s + l.amount, 0), tariffName: isWeekend(sel.date) ? 'Выходной день' : 'Будний день' };
  }

  private validateSelection(sel: Selection, g: Guests): ApiError | null {
    const d = this.dept(sel.departmentId);
    if (!d || d.banyaId !== sel.banyaId) return { code: 'not_found', message: 'Отделение не найдено.' };
    if (d.mode === 'info') return { code: 'forbidden', message: 'Для этого отделения предварительная продажа не предусмотрена.' };
    if (d.mode !== sel.kind) return { code: 'validation', message: 'Способ посещения не совпадает с отделением.' };
    if (!this.saleDates().includes(sel.date)) return { code: 'validation', message: 'Дата вне периода продажи.' };
    if (g.adults < 0) return { code: 'validation', message: 'Неверный состав гостей.' };
    if (g.children.some((a) => a == null || a < 0 || a > 17)) return { code: 'validation', field: 'guests', message: 'Укажите возраст каждого ребёнка (0–17 лет).' };
    if (sel.kind === 'session') {
      if (!sel.seats.length) return { code: 'validation', message: 'Выберите хотя бы одно место.' };
      const seated = g.adults + g.children.filter((a) => (a ?? 0) >= DEMO_SEAT_AGE).length;
      if (seated !== sel.seats.length) {
        return { code: 'validation', field: 'guests', message: `Каждому взрослому и ребёнку от 8 лет нужно отдельное место: выбрано мест — ${sel.seats.length}, гостей с местом — ${seated}.` };
      }
      if (g.adults < 1) return { code: 'validation', field: 'guests', message: 'Нужен хотя бы один взрослый.' };
      const session = this.sessionsFor(d.id, sel.date).find((s) => s.id === sel.sessionId);
      if (!session) return { code: 'conflict', message: 'Этот сеанс больше недоступен. Выберите другой.' };
      const states = this.seatStates(d, sel.date, sel.sessionId);
      const bad = sel.seats.filter((s) => states[s] !== 'free');
      if (bad.length) {
        return { code: 'conflict', conflictSeats: bad, message: `${bad.length > 1 ? 'Места' : 'Место'} ${bad.join(', ')} уже ${bad.length > 1 ? 'недоступны' : 'недоступно'}. Ничего не удержано — измените выбор и продолжите.` };
      }
    } else {
      const guests = g.adults + g.children.length;
      if (g.adults < 1) return { code: 'validation', field: 'guests', message: 'Нужен хотя бы один взрослый.' };
      if (d.maxGuests != null && guests > d.maxGuests) return { code: 'validation', field: 'guests', message: `Вместимость сауны — не более ${d.maxGuests} человек.` };
      const day = this.saunaDay(d.id, sel.date);
      const st = Number(sel.start.slice(0, 2));
      if (!day.durations.includes(sel.hours)) return { code: 'validation', message: 'Недопустимая длительность.' };
      if (st < day.open || st + sel.hours > day.close) return { code: 'validation', message: 'Интервал выходит за часы работы сауны.' };
      for (let h = st; h < st + sel.hours; h++) {
        if (day.busy.includes(h)) return { code: 'conflict', message: 'Это время уже занято. Ничего не удержано — выберите другой интервал.' };
      }
    }
    return null;
  }

  private findOrder(orderId: string, accessToken: string): StoredOrder | ApiError {
    this.refreshExpiry();
    const o = this.state.orders.find((x) => x.id === orderId && x.accessToken === accessToken);
    return o ?? { code: 'not_found', message: 'Оформление не найдено или ссылка недействительна.' };
  }

  private view(o: StoredOrder): OrderView {
    const { accessToken: _a, customerId: _c, ownerPhone: _p, ...rest } = o;
    return rest;
  }

  private idem<T>(key: string, scope: string, run: () => T): T {
    const k = `${scope}|${key}`;
    if (k in this.state.idem) return this.state.idem[k] as T;
    const r = run();
    this.state.idem[k] = r;
    this.save();
    return r;
  }

  /* ---------- Каталог ---------- */

  getBanyas(): Observable<Banya[]> { return this.ok(BANYAS); }

  getBanya(banyaId: string) {
    const banya = this.banya(banyaId);
    if (!banya) return err('not_found', 'Баня не найдена.');
    return this.ok({ banya, departments: DEPARTMENTS.filter((d) => d.banyaId === banyaId).map((d) => this.withPriceFrom(d)) });
  }

  private withPriceFrom(d: Department): Department {
    const cond = d.mode === 'session' ? 'Взрослый' : 'Аренда, час';
    const prices = this.saleDates().map((date) => this.price(this.state.tariffs, d.id, cond, date)).filter((x): x is number => x != null);
    return d.mode === 'info' ? d : { ...d, priceFrom: prices.length ? Math.min(...prices) : undefined };
  }

  getDepartment(banyaId: string, departmentId: string): Observable<DepartmentDetails> {
    const d = this.dept(departmentId);
    const banya = this.banya(banyaId);
    if (!d || !banya || d.banyaId !== banyaId) return err('not_found', 'Отделение не найдено.');
    const fmt = (cond: string, days: string) => {
      const r = this.state.tariffs.find((t) => t.departmentId === d.id && t.condition === cond && (t.days === days || t.days === 'Все дни') && t.from <= todayEkb() && (!t.to || t.to >= todayEkb()));
      return r ? (r.price === 0 ? 'бесплатно' : formatMoney(r.price)) : '—';
    };
    let scheduleRows: DepartmentDetails['scheduleRows'] = [];
    let tariffRows: DepartmentDetails['tariffRows'] = [];
    if (d.mode === 'session') {
      const times = SESSION_TIMES.map(([a, b]) => `${a}–${b}`).join(' · ');
      scheduleRows = [{ days: 'Пн–Пт', value: times }, { days: 'Сб–Вс', value: times }, { days: 'Исключения', value: 'санитарные и праздничные дни публикуются в расписании' }];
      tariffRows = ['Взрослый', 'Ребёнок с местом', 'Ребёнок без места'].map((c) => ({ label: c, weekday: fmt(c, 'Пн–Пт'), weekend: fmt(c, 'Сб–Вс') }));
    } else if (d.mode === 'sauna') {
      scheduleRows = [{ days: 'Ежедневно', value: '10:00–24:00, аренда от 2 до 5 часов' }];
      tariffRows = [
        { label: 'Аренда, час', weekday: fmt('Аренда, час', 'Пн–Пт'), weekend: fmt('Аренда, час', 'Сб–Вс') },
        { label: 'Гость сверх базы', weekday: fmt('Гость сверх базы', 'Пн–Пт'), weekend: fmt('Гость сверх базы', 'Сб–Вс') },
      ];
    }
    return this.ok({ department: this.withPriceFrom(d), banya, scheduleRows, tariffRows });
  }

  /* ---------- Доступность ---------- */

  getSaleDates(departmentId: string): Observable<DateAvailability[]> {
    const d = this.dept(departmentId);
    if (!d || d.mode === 'info') return err('not_found', 'Отделение не найдено.');
    return this.ok(this.saleDates().map((date) => ({
      date,
      hasSessions: d.mode === 'session' ? this.sessionsFor(departmentId, date).length > 0 : this.saunaDay(departmentId, date).busy.length < 14,
    })));
  }

  getSessions(departmentId: string, date: string): Observable<SessionSlot[]> {
    if (!this.dept(departmentId)) return err('not_found', 'Отделение не найдено.');
    return this.ok(this.sessionsFor(departmentId, date));
  }

  getSessionAvailability(departmentId: string, date: string, sessionId: string): Observable<SessionAvailability> {
    const d = this.dept(departmentId);
    if (!d || d.mode !== 'session') return err('not_found', 'Отделение не найдено.');
    return this.ok({ scheme: schemeFor(d), seats: this.seatStates(d, date, sessionId) }, 350);
  }

  getSaunaDay(departmentId: string, date: string): Observable<SaunaDay> {
    const d = this.dept(departmentId);
    if (!d || d.mode !== 'sauna') return err('not_found', 'Сауна не найдена.');
    return this.ok(this.saunaDay(departmentId, date));
  }

  /* ---------- Заказ ---------- */

  quote(selection: Selection, guests: Guests): Observable<Quote> {
    // Как у бекенда: состав гостей проверяется уже при расчёте, занятость — при удержании.
    const problem = this.validateSelection(selection, guests);
    if (problem?.field === 'guests') return err(problem.code, problem.message);
    try { return this.ok(this.calcQuote(selection, guests), 200); }
    catch (e) { return err((e as ApiError).code, (e as ApiError).message); }
  }

  createHold(selection: Selection, guests: Guests, buyer: Buyer, expectedTotal: number, idempotencyKey: string, customerToken?: string): Observable<HoldResult> {
    return defer(() => {
      const name = buyer.name.trim();
      const phone = buyer.phone.replace(/[^\d+]/g, '');
      if (!name) return err('validation', 'Укажите имя покупателя.', { field: 'name' });
      if (!/^\+7\d{10}$/.test(phone)) return err('validation', 'Введите номер полностью: +7 и 10 цифр.', { field: 'phone' });
      const prev = this.state.idem[`hold|${idempotencyKey}`] as string | undefined;
      if (prev) {
        const o = this.state.orders.find((x) => x.id === prev)!;
        return this.ok<HoldResult>({ order: this.view(o), accessToken: o.accessToken }, 400);
      }
      const problem = this.validateSelection(selection, guests);
      if (problem) return err(problem.code, problem.message, problem);
      let quote: Quote;
      try { quote = this.calcQuote(selection, guests); } catch (e) { return err('validation', (e as ApiError).message); }
      const now = new Date();
      this.state.seq++;
      const d = this.dept(selection.departmentId)!;
      const order: StoredOrder = {
        id: `SB-${String(this.state.seq).padStart(6, '0')}`,
        status: 'held', banya: this.banya(selection.banyaId)!, department: d, selection, guests,
        buyer: { name, phone }, quote, createdAt: now.toISOString(),
        holdExpiresAt: new Date(now.getTime() + HOLD_MS).toISOString(),
        previousTotal: quote.total !== expectedTotal ? expectedTotal : undefined,
        history: [{ at: now.toISOString(), text: `Переход к оплате · ${selection.kind === 'session' ? 'места удержаны' : 'сауна удержана'}, сумма зафиксирована` }],
        accessToken: newToken(),
        customerId: customerToken ? this.state.customerSessions[customerToken] : undefined,
        ownerPhone: phone,
      };
      if (order.previousTotal != null) order.history.push({ at: now.toISOString(), text: `Сумма изменилась: было ${formatMoney(expectedTotal)}, стало ${formatMoney(quote.total)}` });
      this.state.orders.push(order);
      this.state.idem[`hold|${idempotencyKey}`] = order.id;
      this.save();
      return this.ok<HoldResult>({ order: this.view(order), accessToken: order.accessToken }, 600);
    });
  }

  getOrder(orderId: string, accessToken: string): Observable<OrderView> {
    return defer(() => {
      const o = this.findOrder(orderId, accessToken);
      return 'code' in o ? err(o.code, o.message) : this.ok(this.view(o));
    });
  }

  releaseHold(orderId: string, accessToken: string, idempotencyKey: string): Observable<void> {
    return defer(() => {
      const o = this.findOrder(orderId, accessToken);
      if ('code' in o) return err(o.code, o.message);
      this.idem(idempotencyKey, 'release', () => {
        if (o.status === 'held') {
          o.status = 'released';
          o.history.push({ at: new Date().toISOString(), text: 'Покупатель вернулся к выбору · удержание снято целиком' });
        }
        return true;
      });
      if (o.status === 'confirmed') return err('conflict', 'Заказ уже оплачен — возврат к выбору недоступен.');
      this.save();
      return this.ok<void>(undefined, 300);
    });
  }

  confirmNewTotal(orderId: string, accessToken: string): Observable<OrderView> {
    return defer(() => {
      const o = this.findOrder(orderId, accessToken);
      if ('code' in o) return err(o.code, o.message);
      if (o.status !== 'held') return err('expired', 'Удержание уже не действует.');
      if (!o.newTotalConfirmed) {
        o.newTotalConfirmed = true;
        o.history.push({ at: new Date().toISOString(), text: 'Покупатель подтвердил новую сумму' });
        this.save();
      }
      return this.ok(this.view(o), 200);
    });
  }

  payTest(orderId: string, accessToken: string, outcome: TestPaymentOutcome, idempotencyKey: string): Observable<OrderView> {
    return defer(() => {
      const o = this.findOrder(orderId, accessToken);
      if ('code' in o) return err(o.code, o.message);
      const done = this.state.idem[`pay|${idempotencyKey}`];
      if (done || o.status === 'confirmed' || o.status === 'declined') return this.ok(this.view(o), 500);
      if (o.status === 'expired') return err('expired', 'Время удержания вышло — оплата не подтверждена.');
      if (o.status !== 'held') return err('conflict', 'Это оформление уже не ожидает оплаты.');
      if (o.previousTotal != null && !o.newTotalConfirmed) return err('validation', 'Подтвердите новую сумму перед оплатой.');
      const now = new Date().toISOString();
      if (outcome === 'decline') {
        o.status = 'declined';
        o.history.push({ at: now, text: 'Тестовая оплата: отказ · удержание снято' });
      } else {
        o.status = 'confirmed';
        o.paidAt = now;
        o.ticketToken = newToken();
        o.history.push({ at: now, text: `Тестовая оплата: успех на ${formatMoney(o.quote.total)} · заказ подтверждён, билет выдан` });
      }
      this.state.idem[`pay|${idempotencyKey}`] = true;
      this.save();
      if (outcome === 'lost') return err('network', 'Ответ не получен.').pipe(delay(1200));
      return this.ok(this.view(o), 1200);
    });
  }

  getTicket(ticketToken: string): Observable<OrderView> {
    return defer(() => {
      const o = ticketToken.length >= 32 ? this.state.orders.find((x) => x.ticketToken === ticketToken && x.status === 'confirmed') : undefined;
      return o ? this.ok(this.view(o)) : err('not_found', 'Билет не найден.');
    });
  }

  /* ---------- Личный кабинет ---------- */

  private me(token: string): StoredCustomer | null {
    const id = this.state.customerSessions[token];
    return this.state.customers.find((c) => c.id === id && !c.deleted) ?? null;
  }

  private profileView(c: StoredCustomer): CustomerProfile {
    const { deleted: _d, ...p } = c;
    return p;
  }

  requestLoginCode(phone: string): Observable<LoginCodeRequest> {
    return defer(() => {
      const p = phone.replace(/[^\d+]/g, '');
      if (!/^\+7\d{10}$/.test(p)) return err('validation', 'Введите номер полностью: +7 и 10 цифр.', { field: 'phone' });
      const prev = this.state.loginCodes[p];
      const now = Date.now();
      if (prev && now - prev.sentAt < CODE_RESEND_SEC * 1000) {
        // Повторный запрос раньше срока не отправляет новый код.
        return this.ok<LoginCodeRequest>({ resendInSec: Math.ceil((CODE_RESEND_SEC * 1000 - (now - prev.sentAt)) / 1000), demoCode: prev.code }, 300);
      }
      const code = String(crypto.getRandomValues(new Uint32Array(1))[0] % 10000).padStart(4, '0');
      this.state.loginCodes[p] = { code, sentAt: now, expiresAt: now + CODE_TTL_MS, attempts: 0 };
      this.save();
      return this.ok<LoginCodeRequest>({ resendInSec: CODE_RESEND_SEC, demoCode: code }, 600);
    });
  }

  verifyLoginCode(phone: string, code: string): Observable<CustomerSession> {
    return defer(() => {
      const p = phone.replace(/[^\d+]/g, '');
      const entry = this.state.loginCodes[p];
      if (!entry || entry.expiresAt < Date.now()) return err('expired', 'Код устарел. Запросите новый.');
      if (entry.attempts >= CODE_MAX_ATTEMPTS) return err('forbidden', 'Слишком много попыток. Запросите новый код.');
      if (entry.code !== code) {
        entry.attempts++;
        this.save();
        return err('validation', 'Неверный код. Проверьте SMS и попробуйте ещё раз.', { field: 'code' });
      }
      delete this.state.loginCodes[p];
      let c = this.state.customers.find((x) => x.phone === p && !x.deleted);
      if (!c) {
        const demo = p === '+79000000000';
        c = {
          id: `c${Date.now()}`, phone: p, phoneVerified: true,
          firstName: demo ? 'Иван' : '', lastName: demo ? 'Петров' : undefined,
          notify: { smsReminder: true, emailTicket: true, news: false }, consentAt: new Date().toISOString(),
        };
        this.state.customers.push(c);
      }
      const token = newToken(32);
      this.state.customerSessions[token] = c.id;
      this.save();
      return this.ok<CustomerSession>({ token, profile: this.profileView(c) }, 500);
    });
  }

  customerLogout(token: string): Observable<void> {
    delete this.state.customerSessions[token];
    this.save();
    return this.ok<void>(undefined, 100);
  }

  getProfile(token: string): Observable<CustomerProfile> {
    return defer(() => {
      const c = this.me(token);
      return c ? this.ok(this.profileView(c)) : err('unauthorized', 'Сессия завершена. Войдите снова.');
    });
  }

  updateProfile(token: string, patch: ProfilePatch, idempotencyKey: string): Observable<CustomerProfile> {
    return defer(() => {
      const c = this.me(token);
      if (!c) return err('unauthorized', 'Сессия завершена. Войдите снова.');
      if (patch.firstName !== undefined && !patch.firstName.trim()) return err('validation', 'Укажите имя.', { field: 'firstName' });
      if (patch.email && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(patch.email.trim())) return err('validation', 'Проверьте адрес почты.', { field: 'email' });
      this.idem(idempotencyKey, 'profile', () => {
        if (patch.firstName !== undefined) c.firstName = patch.firstName.trim();
        if (patch.lastName !== undefined) c.lastName = patch.lastName.trim() || undefined;
        if (patch.email !== undefined) c.email = patch.email.trim() || undefined;
        if (patch.notify) c.notify = { ...patch.notify };
        return true;
      });
      this.save();
      return this.ok(this.profileView(c), 400);
    });
  }

  deleteAccount(token: string): Observable<void> {
    return defer(() => {
      const c = this.me(token);
      if (!c) return err('unauthorized', 'Сессия завершена. Войдите снова.');
      // Заказы остаются (у них свой срок хранения), связь с аккаунтом снимается.
      for (const o of this.state.orders) if (o.customerId === c.id) o.customerId = undefined;
      c.deleted = true;
      for (const [t, id] of Object.entries(this.state.customerSessions)) if (id === c.id) delete this.state.customerSessions[t];
      this.save();
      return this.ok<void>(undefined, 400);
    });
  }

  myOrders(token: string): Observable<OrderView[]> {
    return defer(() => {
      const c = this.me(token);
      if (!c) return err('unauthorized', 'Сессия завершена. Войдите снова.');
      this.refreshExpiry();
      const list = this.state.orders
        .filter((o) => o.status === 'confirmed' && (o.customerId === c.id || (c.phoneVerified && o.ownerPhone === c.phone)))
        .map((o) => this.view(o));
      return this.ok(list);
    });
  }

  /* ---------- Администратор ---------- */

  private checkAdmin(token: string): ApiError | null {
    return this.adminTokens.has(token) || this.restoreAdmin(token) ? null : { code: 'unauthorized', message: 'Сессия администратора недействительна. Войдите заново.' };
  }
  private restoreAdmin(token: string): boolean {
    // демо: сессия переживает перезагрузку страницы в пределах вкладки
    try { if (sessionStorage.getItem('sb-demo-admin') === token) { this.adminTokens.add(token); return true; } } catch { /* */ }
    return false;
  }

  adminLogin(login: string, password: string) {
    // Демо: принимается любой логин при пароле не короче 4 символов. Настоящую проверку выполнит сервер.
    if (!login.trim() || password.length < 4) return err('unauthorized', 'Неверный логин или пароль');
    const token = newToken();
    this.adminTokens.add(token);
    try { sessionStorage.setItem('sb-demo-admin', token); } catch { /* */ }
    return this.ok({ token, name: 'Администратор' }, 500);
  }

  adminLogout(token: string): Observable<void> {
    this.adminTokens.delete(token);
    try { sessionStorage.removeItem('sb-demo-admin'); } catch { /* */ }
    return this.ok<void>(undefined, 100);
  }

  adminOrders(token: string, f: AdminOrderFilter): Observable<OrderView[]> {
    const a = this.checkAdmin(token);
    if (a) return err(a.code, a.message);
    this.refreshExpiry();
    const list = this.state.orders
      .filter((o) => !f.banyaId || o.banya.id === f.banyaId)
      .filter((o) => !f.departmentId || o.department.id === f.departmentId)
      .filter((o) => !f.from || o.selection.date >= f.from)
      .filter((o) => !f.to || o.selection.date <= f.to)
      .sort((x, y) => y.createdAt.localeCompare(x.createdAt))
      .map((o) => this.view(o));
    return this.ok(list);
  }

  adminOrder(token: string, orderId: string): Observable<OrderView> {
    const a = this.checkAdmin(token);
    if (a) return err(a.code, a.message);
    this.refreshExpiry();
    const o = this.state.orders.find((x) => x.id === orderId);
    return o ? this.ok(this.view(o)) : err('not_found', 'Заказ не найден.');
  }

  adminWeekSchedule(token: string, departmentId: string, weekStart: string) {
    const a = this.checkAdmin(token);
    if (a) return err(a.code, a.message);
    const d = this.dept(departmentId);
    if (!d || d.mode !== 'session') return err('not_found', 'Выберите сеансовое отделение.');
    const days: WeekScheduleDay[] = Array.from({ length: 7 }, (_, i) => {
      const date = addDays(weekStart, i);
      const sanitary = date === this.sanitaryDay(departmentId) && hash(departmentId) % 2 === 0;
      return {
        date,
        sessions: sanitary ? [] : SESSION_TIMES.map(([start, end], k) => ({
          id: `s${k}`, start, end,
          cancelled: this.state.cancelled.some((c) => c.departmentId === departmentId && c.date === date && c.sessionId === `s${k}`),
        })),
      };
    });
    const exceptions: ScheduleException[] = [
      ...days.filter((x) => !x.sessions.length).map((x) => ({ date: x.date, text: 'Санитарный день · закрыто' })),
      ...this.state.cancelled.filter((c) => c.departmentId === departmentId).map((c) => {
        const [s, e] = SESSION_TIMES[Number(c.sessionId.slice(1))];
        return { date: c.date, text: `Сеанс ${s}–${e} отменён` };
      }),
    ].sort((x, y) => x.date.localeCompare(y.date));
    return this.ok({ days, exceptions });
  }

  adminCancelSession(token: string, departmentId: string, date: string, sessionId: string, idempotencyKey: string): Observable<void> {
    const a = this.checkAdmin(token);
    if (a) return err(a.code, a.message);
    const affected = this.activeOrders().filter((o) => o.selection.kind === 'session' && o.selection.departmentId === departmentId && o.selection.date === date && o.selection.sessionId === sessionId);
    if (affected.length) {
      const ids = affected.map((o) => o.id).join(', ');
      return err('conflict', `Изменение не сохранено: на этот сеанс ${affected.length} ${plural(affected.length, 'действующий заказ', 'действующих заказа', 'действующих заказов')} (${ids}). Заказы не отменяются автоматически — способ разрешения конфликта на согласовании.`);
    }
    this.idem(idempotencyKey, 'cancel', () => { this.state.cancelled.push({ departmentId, date, sessionId }); return true; });
    this.save();
    return this.ok<void>(undefined);
  }

  adminTariffs(token: string, departmentId: string): Observable<TariffRule[]> {
    const a = this.checkAdmin(token);
    if (a) return err(a.code, a.message);
    return this.ok(this.state.tariffs.filter((t) => t.departmentId === departmentId).sort((x, y) => x.condition.localeCompare(y.condition) || x.from.localeCompare(y.from)));
  }

  adminSaveTariff(token: string, rule: Omit<TariffRule, 'id'>, idempotencyKey: string): Observable<TariffRule> {
    const a = this.checkAdmin(token);
    if (a) return err(a.code, a.message);
    if (!rule.condition || !rule.days || !rule.from) return err('validation', 'Заполните условие, дни и дату начала.');
    if (!Number.isInteger(rule.price) || rule.price < 0) return err('validation', 'Цена указана неверно.', { field: 'price' });
    if (rule.to && rule.to < rule.from) return err('validation', 'Дата окончания раньше даты начала.');
    const overlap = this.state.tariffs.find((t) => t.departmentId === rule.departmentId && t.condition === rule.condition && t.days === rule.days
      && t.from === rule.from);
    if (overlap) return err('conflict', `Пересечение с правилом «${overlap.condition} · ${overlap.days}» с ${dayMonthShort(overlap.from)} — укажите другую дату начала.`);
    const saved = this.idem(idempotencyKey, 'tariff', () => {
      const r: TariffRule = { ...rule, id: `t${Date.now()}` };
      // Действующее правило без даты окончания закрываем днём раньше нового (прочие пересечения — на согласовании).
      for (const t of this.state.tariffs) {
        if (t.departmentId === rule.departmentId && t.condition === rule.condition && t.days === rule.days && !t.to && t.from < rule.from) t.to = addDays(rule.from, -1);
      }
      this.state.tariffs.push(r);
      return r;
    });
    this.save();
    return this.ok(saved);
  }

  adminSeatStates(token: string, departmentId: string, date: string, sessionId: string) {
    const a = this.checkAdmin(token);
    if (a) return err(a.code, a.message);
    const d = this.dept(departmentId);
    if (!d || d.mode !== 'session') return err('not_found', 'Выберите сеансовое отделение.');
    const states = this.seatStates(d, date, sessionId);
    const scheme = schemeFor(d);
    const owner = (seat: string) => this.activeOrders().find((o) => o.selection.kind === 'session' && o.selection.departmentId === departmentId && o.selection.date === date && o.selection.sessionId === sessionId && o.selection.seats.includes(seat))?.id;
    return this.ok({
      scheme: {
        version: scheme.version,
        zones: scheme.zones.map((z) => ({
          name: z.name,
          seats: z.seats.map((l): AdminSeatState => ({ label: l, state: states[l] === 'occupied' ? 'sold' : states[l] === 'blocked' ? 'blocked' : 'free', orderId: owner(l) })),
        })),
      },
    });
  }

  adminSetBlock(token: string, departmentId: string, date: string, sessionId: string, seats: string[], block: boolean, reason: string, idempotencyKey: string): Observable<void> {
    const a = this.checkAdmin(token);
    if (a) return err(a.code, a.message);
    if (!seats.length) return err('validation', 'Выберите места на схеме.');
    const d = this.dept(departmentId)!;
    const states = this.seatStates(d, date, sessionId);
    if (block) {
      const sold = seats.filter((s) => states[s] === 'occupied');
      if (sold.length) {
        return err('conflict', `Блокировка не применена: ${plural(sold.length, 'место', 'места', 'места')} ${sold.join(', ')} уже ${sold.length > 1 ? 'проданы или удержаны' : 'продано или удержано'}. Изменение не применяется частично — уберите ${sold.length > 1 ? 'их' : 'его'} из выбора.`);
      }
    }
    this.idem(idempotencyKey, 'block', () => {
      if (block) {
        for (const s of seats) if (states[s] === 'free') this.state.blocks.push({ departmentId, date, sessionId, seat: s, reason });
      } else {
        this.state.blocks = this.state.blocks.filter((b) => !(b.departmentId === departmentId && b.date === date && b.sessionId === sessionId && seats.includes(b.seat)));
      }
      return true;
    });
    this.save();
    return this.ok<void>(undefined);
  }
}

