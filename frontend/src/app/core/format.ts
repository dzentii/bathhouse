import { ApiError, Guests, Kopecks, OrderStatus, Selection } from './models';

export const TIME_ZONE = 'Asia/Yekaterinburg';

/** Сумма в копейках → «4 200 ₽» / «4 200,50 ₽». */
export function formatMoney(value: Kopecks): string {
  const rub = Math.trunc(value / 100);
  const kop = Math.abs(value % 100);
  const rubStr = new Intl.NumberFormat('ru-RU').format(rub);
  return kop ? `${rubStr},${String(kop).padStart(2, '0')} ₽` : `${rubStr} ₽`;
}

/** Сегодняшняя дата в Екатеринбурге, 'YYYY-MM-DD'. */
export function todayEkb(now = new Date()): string {
  return new Intl.DateTimeFormat('en-CA', { timeZone: TIME_ZONE, year: 'numeric', month: '2-digit', day: '2-digit' }).format(now);
}

/** Текущее время в Екатеринбурге в минутах от полуночи. */
export function nowEkbMinutes(now = new Date()): number {
  const p = new Intl.DateTimeFormat('en-GB', { timeZone: TIME_ZONE, hour: '2-digit', minute: '2-digit', hour12: false }).formatToParts(now);
  const h = Number(p.find((x) => x.type === 'hour')?.value ?? 0) % 24;
  const m = Number(p.find((x) => x.type === 'minute')?.value ?? 0);
  return h * 60 + m;
}

export function toMinutes(t: string): number {
  const [h, m] = t.split(':').map(Number);
  return h * 60 + m;
}

/** Разница в днях между датами 'YYYY-MM-DD'. */
export function daysBetween(from: string, to: string): number {
  return Math.round((Date.parse(to + 'T00:00:00Z') - Date.parse(from + 'T00:00:00Z')) / 86400_000);
}

export function addDays(date: string, days: number): string {
  const d = new Date(date + 'T00:00:00Z');
  d.setUTCDate(d.getUTCDate() + days);
  return d.toISOString().slice(0, 10);
}

function utc(date: string): Date { return new Date(date + 'T12:00:00Z'); }

export function weekdayShort(date: string): string {
  const s = new Intl.DateTimeFormat('ru-RU', { weekday: 'short', timeZone: 'UTC' }).format(utc(date));
  return s.charAt(0).toUpperCase() + s.slice(1);
}

/** «10 ноя» */
export function dayMonthShort(date: string): string {
  return new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'short', timeZone: 'UTC' }).format(utc(date)).replace('.', '');
}

/** «Вт, 10 ноября 2026» */
export function dateLong(date: string): string {
  const d = new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'long', year: 'numeric', timeZone: 'UTC' }).format(utc(date)).replace(' г.', '');
  return `${weekdayShort(date)}, ${d}`;
}

/** «Вторник, 10 ноября 2026» */
export function dateFull(date: string): string {
  const wd = new Intl.DateTimeFormat('ru-RU', { weekday: 'long', timeZone: 'UTC' }).format(utc(date));
  const d = new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'long', year: 'numeric', timeZone: 'UTC' }).format(utc(date)).replace(' г.', '');
  return `${wd.charAt(0).toUpperCase() + wd.slice(1)}, ${d}`;
}

/** ISO-момент → «08.11.2026, 18:05» по Екатеринбургу. */
export function dateTimeEkb(iso: string): string {
  return new Intl.DateTimeFormat('ru-RU', { timeZone: TIME_ZONE, day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit' }).format(new Date(iso));
}

export function timeEkb(iso: string): string {
  return new Intl.DateTimeFormat('ru-RU', { timeZone: TIME_ZONE, hour: '2-digit', minute: '2-digit' }).format(new Date(iso));
}

export function plural(n: number, one: string, few: string, many: string): string {
  const m10 = n % 10, m100 = n % 100;
  if (m10 === 1 && m100 !== 11) return one;
  if (m10 >= 2 && m10 <= 4 && (m100 < 10 || m100 >= 20)) return few;
  return many;
}

export function hh(h: number): string { return `${String(h % 24).padStart(2, '0')}:00`; }

export function sortSeats(seats: string[]): string[] {
  return [...seats].sort((a, b) => a.localeCompare(b, 'ru', { numeric: true }));
}

/** «5 лет», «1 год»; null — «возраст не указан». */
export function ageText(age: number | null): string {
  return age == null ? 'возраст не указан' : `${age} ${plural(age, 'год', 'года', 'лет')}`;
}

/** «2 детей (5 и 10 лет)». */
export function childrenText(ages: (number | null)[]): string {
  if (!ages.length) return '';
  const known = ages.filter((a): a is number => a != null).sort((a, b) => a - b);
  const word = `${ages.length} ${plural(ages.length, 'ребёнок', 'ребёнка', 'детей')}`;
  if (!known.length) return word;
  const list = known.length > 1 ? `${known.slice(0, -1).join(', ')} и ${known[known.length - 1]}` : String(known[0]);
  return `${word} (${list} ${plural(known[known.length - 1], 'год', 'года', 'лет')})`;
}

export function guestsCount(g: Guests): number { return g.adults + g.children.length; }

export function guestsText(g: Guests): string {
  const parts: string[] = [];
  if (g.adults) parts.push(`${g.adults} ${plural(g.adults, 'взрослый', 'взрослых', 'взрослых')}`);
  if (g.children.length) parts.push(childrenText(g.children));
  return parts.join(', ') || '—';
}

export function selectionTime(s: Selection): string {
  return `${s.start}–${s.end}`;
}

export function resourcesText(s: Selection): string {
  return s.kind === 'session' ? sortSeats(s.seats).join(', ') : 'Сауна целиком';
}

export const STATUS_LABEL: Record<OrderStatus, string> = {
  draft: 'Черновик',
  held: 'Удержание',
  confirmed: 'Подтверждён',
  declined: 'Отказ оплаты',
  expired: 'Удержание истекло',
  released: 'Выбор изменён',
  pending: 'Проверяется',
};

/** Нормализация телефона: «8 900…», «900…», «+7 (900)…» → «+79001234567». Окончательный формат — на согласовании. */
export function normalizePhone(raw: string): string {
  let d = raw.replace(/\D/g, '');
  if (d.length === 11 && (d.startsWith('8') || d.startsWith('7'))) d = d.slice(1);
  return d.length === 10 ? `+7${d}` : raw.trim();
}

export function isValidPhone(raw: string): boolean { return /^\+7\d{10}$/.test(normalizePhone(raw)); }

/** «+79000000000» → «+7 900 000-00-00». */
export function formatPhone(p: string): string {
  const m = /^\+7(\d{3})(\d{3})(\d{2})(\d{2})$/.exec(p);
  return m ? `+7 ${m[1]} ${m[2]}-${m[3]}-${m[4]}` : p;
}

/** Приводит любую ошибку (в т.ч. обёрнутую resource-API) к ApiError. */
export function asApiError(e: unknown): ApiError {
  const raw = (e && typeof e === 'object' && 'cause' in e && (e as { cause?: unknown }).cause) || e;
  if (raw && typeof raw === 'object' && 'code' in raw && 'message' in raw) return raw as ApiError;
  return { code: 'network', message: 'Не удалось связаться с сервером. Проверьте соединение и попробуйте ещё раз.' };
}

/** UUIDv4 для заголовка X-Idempotency-Key. */
export function uuidv4(): string {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID();
  // randomUUID недоступен вне защищённого контекста (http не на localhost)
  const b = crypto.getRandomValues(new Uint8Array(16));
  b[6] = (b[6] & 0x0f) | 0x40; b[8] = (b[8] & 0x3f) | 0x80;
  const h = [...b].map((x) => x.toString(16).padStart(2, '0')).join('');
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}
