// Mock data. Everything here is what the backend will later count from the
// events the client sends:
//
//   POST /v1/events  { device, platform, version, events: [{ name, ts, props }] }
//
//   download_click   landing, the download button (no device yet: props.platform)
//   first_launch     the app started for the first time on this device = install
//   app_open         any start; active people = unique devices with one
//   connect          props.ok, props.ms
//   error            props.message, props.where, props.stack
//
// Support tickets, donations and presets are their own tables, not events.

export type Win = '24h' | '7d' | '30d';
export const WINDOWS: Win[] = ['24h', '7d', '30d'];

export type Platform = 'windows' | 'macos' | 'android';
export const PLATFORMS: Platform[] = ['windows', 'macos', 'android'];
export const PLATFORM_COLOR: Record<Platform, string> = {
	windows: 'var(--blue)',
	macos: 'var(--text)',
	android: 'var(--ok)',
};
export const PLATFORM_SHARE: Record<Platform, number> = { windows: 0.64, macos: 0.21, android: 0.15 };

const HOUR = 3600_000;
const DAY = 24 * HOUR;
const NOW = Date.now();

function rng(seed: number) {
	return () => {
		seed = (seed + 0x6d2b79f5) | 0;
		let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
		t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
		return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
	};
}

const SPEC: Record<Win, { n: number; step: number }> = {
	'24h': { n: 24, step: HOUR },
	'7d': { n: 28, step: 6 * HOUR },
	'30d': { n: 30, step: DAY },
};

/** Bucket start times, oldest first. */
export function buckets(w: Win): number[] {
	const { n, step } = SPEC[w];
	const end = Math.floor(NOW / step) * step;
	return Array.from({ length: n }, (_, i) => end - (n - 1 - i) * step);
}

/** A per-day rate spread over the window's buckets: daily rhythm, slow growth, noise. */
function series(w: Win, perDay: number, seed: number): number[] {
	const r = rng(seed);
	const { step } = SPEC[w];
	return buckets(w).map((t) => {
		const hour = new Date(t).getHours();
		const rhythm = step < DAY ? 1 + 0.55 * Math.sin(((hour - 9) / 24) * 2 * Math.PI) : 1;
		const growth = 1 - ((NOW - t) / (30 * DAY)) * 0.35;
		return Math.max(0, Math.round(perDay * (step / DAY) * rhythm * growth * (0.8 + r() * 0.4)));
	});
}

const sum = (a: number[]) => a.reduce((s, v) => s + v, 0);

// ---------- overview ----------

export function overview(w: Win) {
	const active = series(w, 3400, 1);
	const fresh = series(w, 190, 2);
	const clicks = series(w, 410, 3);
	const installs = fresh;
	const days = SPEC[w].n * (SPEC[w].step / DAY);
	const uniq = Math.round(3400 * (w === '24h' ? 1 : w === '7d' ? 1.9 : 2.6));
	const inst = sum(installs);
	return {
		buckets: buckets(w),
		people: {
			active: uniq,
			fresh: sum(fresh),
			returning: uniq - sum(fresh),
			total: 18_942,
			series: { active, fresh },
		},
		sessions: {
			count: Math.round(uniq * 2.3 * days),
			medianMin: 47,
			connectOkPct: 97.4,
			connectP50Ms: 820,
		},
		downloads: {
			clicks: sum(clicks),
			installs: inst,
			convPct: (inst / Math.max(1, sum(clicks))) * 100,
			series: { clicks, installs },
			platforms: PLATFORMS.map((p) => ({ p, installs: Math.round(inst * PLATFORM_SHARE[p]), pct: PLATFORM_SHARE[p] * 100 })),
		},
	};
}

// ---------- errors ----------

export interface ErrorGroup {
	id: string;
	message: string;
	where: string;
	count: number;
	devices: number;
	platforms: Platform[];
	versions: string[];
	last: number;
	stack: string;
}

const ERRORS: Omit<ErrorGroup, 'count' | 'devices' | 'last'>[] = [
	{
		id: 'e1',
		message: 'sing-box start: listen tcp 127.0.0.1:2080: bind: address already in use',
		where: 'engine/core/box',
		platforms: ['windows', 'macos'],
		versions: ['0.9.4', '0.9.3'],
		stack: 'engine/core/box.(*Box).Start\n\tbox.go:142\nengine/core.Start\n\tcore.go:88\nmain.nekkobox_start\n\texport.go:31',
	},
	{
		id: 'e2',
		message: 'wintun: LoadLibrary wintun.dll: The specified module could not be found',
		where: 'engine/core/tun',
		platforms: ['windows'],
		versions: ['0.9.4'],
		stack: 'engine/core/tun.open\n\ttun_windows.go:57\nengine/core/box.(*Box).Start\n\tbox.go:120',
	},
	{
		id: 'e3',
		message: 'dns: exchange timeout (fake-ip, 1.1.1.1:53)',
		where: 'engine/core/dns',
		platforms: ['windows', 'macos', 'android'],
		versions: ['0.9.4', '0.9.3', '0.9.2'],
		stack: 'engine/core/dns.(*Client).Exchange\n\tclient.go:211\nengine/core/dns.(*Router).Lookup\n\trouter.go:96',
	},
	{
		id: 'e4',
		message: 'profile import: invalid vless uri: missing pbk',
		where: 'app/import_profile_dialog',
		platforms: ['windows', 'android'],
		versions: ['0.9.4'],
		stack: 'ImportProfileDialog._submit (import_profile_dialog.dart:118)\nNekkoboxCore.importUri (nekkobox_core.dart:204)',
	},
	{
		id: 'e5',
		message: 'utun: create failed: operation not permitted',
		where: 'engine/core/tun',
		platforms: ['macos'],
		versions: ['0.9.4', '0.9.3'],
		stack: 'engine/core/tun.open\n\ttun_darwin.go:44\nengine/core/box.(*Box).Start\n\tbox.go:120',
	},
	{
		id: 'e6',
		message: 'RangeError (index): Invalid value: Not in inclusive range 0..3: 4',
		where: 'app/profiles_list',
		platforms: ['windows', 'macos', 'android'],
		versions: ['0.9.4'],
		stack: '#0 List.[] (dart:core-patch/growable_array.dart)\n#1 ProfilesList.build.<anonymous closure> (profiles_list.dart:77)\n#2 SliverChildBuilderDelegate.build',
	},
	{
		id: 'e7',
		message: 'routing: process lookup: OpenProcess: Access is denied',
		where: 'engine/core/routing',
		platforms: ['windows'],
		versions: ['0.9.4', '0.9.3'],
		stack: 'engine/core/routing.processName\n\tproc_windows.go:63\nengine/core/routing.(*Matcher).Match\n\tmatcher.go:140',
	},
];

export function errors(w: Win): ErrorGroup[] {
	const r = rng(7 + w.length);
	const k = w === '24h' ? 1 : w === '7d' ? 6 : 22;
	return ERRORS.map((e, i) => {
		const count = Math.round((900 / (i + 1.4)) * k * (0.6 + r() * 0.8));
		return {
			...e,
			count,
			devices: Math.max(1, Math.round(count / (2 + r() * 6))),
			last: NOW - Math.round(r() * (i + 1) * 40 * 60_000),
		};
	}).sort((a, b) => b.count - a.count);
}

// ---------- support ----------

export type TicketStatus = 'open' | 'answered' | 'closed';

export interface Ticket {
	id: number;
	from: string;
	platform: Platform;
	version: string;
	status: TicketStatus;
	messages: { staff: boolean; text: string; ts: number }[];
}

export const tickets: Ticket[] = [
	{
		id: 214,
		from: '@kirill_v',
		platform: 'windows',
		version: '0.9.4',
		status: 'open',
		messages: [
			{ staff: false, text: 'после обновления не подключается, пишет что порт занят', ts: NOW - 25 * 60_000 },
		],
	},
	{
		id: 213,
		from: 'anon · 7f3a…c1',
		platform: 'macos',
		version: '0.9.4',
		status: 'open',
		messages: [
			{ staff: false, text: 'ютуб грузит, а дискорд голос не работает', ts: NOW - 2 * HOUR },
			{ staff: false, text: 'режим обход, профиль reality', ts: NOW - 2 * HOUR + 60_000 },
		],
	},
	{
		id: 211,
		from: 'mail@ivanova.me',
		platform: 'android',
		version: '0.9.3',
		status: 'answered',
		messages: [
			{ staff: false, text: 'как добавить свой сервер?', ts: NOW - 9 * HOUR },
			{ staff: true, text: 'профили → импорт, вставьте ссылку vless://', ts: NOW - 8 * HOUR },
		],
	},
	{
		id: 207,
		from: '@den',
		platform: 'windows',
		version: '0.9.3',
		status: 'closed',
		messages: [
			{ staff: false, text: 'антивирус ругается на wintun.dll', ts: NOW - 3 * DAY },
			{ staff: true, text: 'это драйвер туннеля, добавьте папку в исключения', ts: NOW - 3 * DAY + HOUR },
			{ staff: false, text: 'помогло, спасибо', ts: NOW - 3 * DAY + 2 * HOUR },
		],
	},
];

// ---------- donations ----------

export interface Donation {
	ts: number;
	who: string | null;
	rub: number;
	via: string;
	note: string;
}

export function donations(w: Win) {
	const sums = series(w, 4200, 11);
	const r = rng(12);
	const VIA = ['boosty', 'tribute', 'crypto'];
	const NOTES = ['', '', 'спасибо за обход', '', 'на сервера', '', 'лучший впн'];
	const list: Donation[] = Array.from({ length: 12 }, (_, i) => ({
		ts: NOW - Math.round((i + r()) * (w === '24h' ? 1.5 : w === '7d' ? 12 : 50) * HOUR),
		who: r() < 0.4 ? null : ['@mira', '@stas', 'oleg k.', '@nyan', '@vova', 'tanya'][i % 6],
		rub: [100, 200, 300, 500, 1000, 250][Math.floor(r() * 6)],
		via: VIA[Math.floor(r() * VIA.length)],
		note: NOTES[i % NOTES.length],
	}));
	const total = sum(sums);
	const count = Math.max(1, Math.round(total / 340));
	const days = SPEC[w].n * (SPEC[w].step / DAY);
	return {
		buckets: buckets(w),
		series: sums,
		total,
		count,
		avg: total / count,
		donors: Math.round(count * 0.8),
		monthForecast: (total / days) * 30,
		list,
	};
}

// ---------- presets ----------

export type RoutingMode = 'bypass' | 'proxy';

export interface Preset {
	id: string;
	name: string;
	version: number;
	mode: RoutingMode;
	domains: string[];
	apps: string[];
	percent: number;
	platforms: Platform[];
	users: string[];
	paused: boolean;
	received: number;
}

/** Devices one percent of the audience is: what the rollout is measured against. */
export const ACTIVE_DEVICES = 6800;

export const presets: Preset[] = [
	{
		id: 'ru-base',
		name: 'ru базовый',
		version: 3,
		mode: 'bypass',
		domains: ['gosuslugi.ru', 'sberbank.ru', 'vk.com', 'yandex.ru', 'ozon.ru', 'wildberries.ru', 'mos.ru'],
		apps: [],
		percent: 100,
		platforms: ['windows', 'macos', 'android'],
		users: [],
		paused: false,
		received: 6512,
	},
	{
		id: 'ru-base-4',
		name: 'ru базовый',
		version: 4,
		mode: 'bypass',
		domains: ['gosuslugi.ru', 'sberbank.ru', 'vk.com', 'yandex.ru', 'ozon.ru', 'wildberries.ru', 'mos.ru', 'tbank.ru', 'avito.ru'],
		apps: ['Steam.exe'],
		percent: 25,
		platforms: ['windows', 'macos'],
		users: [],
		paused: false,
		received: 1104,
	},
	{
		id: 'games',
		name: 'игры через прокси',
		version: 1,
		mode: 'proxy',
		domains: ['discord.com', 'discord.gg', 'discordapp.net'],
		apps: ['Discord.exe', 'Discord'],
		percent: 0,
		platforms: ['windows', 'macos'],
		users: ['7f3a…c1', 'b20e…9d', '@kirill_v'],
		paused: false,
		received: 2,
	},
];

// ---------- format ----------

/** Grouped with a thin space: the locale's full-width one gapes in a mono font. */
export const num = (v: number) => Math.round(v).toLocaleString('ru').replace(/\s/g, '\u2009');
export const rub = (v: number) => `${num(v)} ₽`;
export const pct = (v: number) => `${v.toFixed(1)}%`;

export function compact(v: number): string {
	if (v >= 1e6) return `${(v / 1e6).toFixed(1)}m`;
	if (v >= 1e4) return `${Math.round(v / 1e3)}k`;
	if (v >= 1e3) return `${(v / 1e3).toFixed(1)}k`;
	return String(Math.round(v));
}

/** The axis label of a bucket and its tooltip title. */
export function bucketText(t: number, w: Win): { axis: string; tip: string } {
	const d = new Date(t);
	const hh = `${String(d.getHours()).padStart(2, '0')}:00`;
	const dm = `${String(d.getDate()).padStart(2, '0')}.${String(d.getMonth() + 1).padStart(2, '0')}`;
	if (w === '24h') return { axis: hh, tip: `${dm} ${hh}` };
	if (w === '7d') return { axis: dm, tip: `${dm} ${hh}` };
	return { axis: dm, tip: dm };
}

export function ago(t: number): string {
	const m = Math.round((NOW - t) / 60_000);
	if (m < 1) return 'сейчас';
	if (m < 60) return `${m} мин`;
	if (m < 48 * 60) return `${Math.round(m / 60)} ч`;
	return `${Math.round(m / 1440)} дн`;
}
