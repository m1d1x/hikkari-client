<script lang="ts">
  import { onMount, onDestroy } from 'svelte'
  import { slide } from 'svelte/transition'
  import { cubicOut } from 'svelte/easing'
  import {
    StartVPN, StopVPN, RestartVPN, Health,
    GetWhitelist, AddRule, RemoveRule,
    GetProfiles, SwitchProfile, GetLogTail,
    GetUptimeHistory, RenameProfile, DeleteProfile,
  } from '../wailsjs/go/main/App.js'

  type HealthStatus = 'off' | 'connecting' | 'ok' | 'broken'
  type HealthInfo = { running: boolean; tunnelOK: boolean; ip: string; status: HealthStatus; message: string }

  let running = false
  let tunnelOK = false
  let status: HealthStatus = 'off'
  let statusMsg = ''
  let busy = false
  let extIP = '—'
  let up = 0
  let down = 0
  let dirty = false
  let showLog = false
  let logTail = ''
  let tab: 'domains' | 'ips' | 'processes' = 'domains'
  const tabs = ['domains', 'ips', 'processes'] as const

  let wl: { domains: string[]; ips: string[]; processes: string[] } = { domains: [], ips: [], processes: [] }
  let inputVal = ''

  type Sample = { ts: number; ok: boolean; rtt_ms: number }
  type Prof = { name: string; label: string; active: boolean; endpoint: string; protocol: string }
  let profiles: Prof[] = []
  let uptime: Record<string, Sample[]> = {}
  let expanded: string | null = null
  let editing: string | null = null
  let editVal = ''
  let switching = false
  let uptimeTimer: ReturnType<typeof setInterval>

  async function saveLabel(name: string) {
    const v = editVal.trim()
    editing = null
    if (!v) return
    try { await RenameProfile(name, v); await loadProfiles() } catch (e) { console.error(e) }
  }
  async function delProfile(name: string) {
    if (!confirm('Удалить этот профиль?')) return
    try { await DeleteProfile(name); expanded = null; await loadProfiles() } catch (e: any) { alert(e?.message || String(e)) }
  }

  let ws: WebSocket | null = null
  let statusTimer: ReturnType<typeof setInterval>

  let routingOpen = false
  const labels = { domains: 'домены', ips: 'IP', processes: 'приложения' }
  const phold = {
    domains: 'github.com',
    ips: '192.168.0.0/16',
    processes: 'discord.exe',
  }
  const hints = {
    domains: 'example.com = все поддомены тоже. api.example.com = только этот',
    ips: 'один IP или подсеть в CIDR-нотации',
    processes: 'имя .exe — весь трафик процесса пойдёт мимо VPN',
  }

  async function refreshStatus() {
    let h: HealthInfo
    try { h = (await Health()) as HealthInfo }
    catch { h = { running: false, tunnelOK: false, ip: '', status: 'off', message: '' } }
    running = h.running
    tunnelOK = h.tunnelOK
    status = h.status
    statusMsg = h.message || ''
    if (!running) {
      extIP = '—'; up = 0; down = 0; closeWS()
    } else {
      if (!ws) openWS()
      extIP = h.ip || '—'
    }
  }

  async function toggleLog() {
    showLog = !showLog
    if (showLog) {
      try { logTail = await GetLogTail() } catch { logTail = '' }
    }
  }

  async function toggle() {
    if (busy) return
    busy = true
    try {
      if (running) { await StopVPN() }
      else { await StartVPN(); await sleep(1600) }
      await refreshStatus()
    } finally { busy = false }
  }

  async function apply() {
    if (busy) return
    busy = true
    try {
      await RestartVPN()
      await sleep(2000)
      dirty = false
      extIP = '—'
      await refreshStatus()
    } finally { busy = false }
  }

  function openWS() {
    closeWS()
    try {
      ws = new WebSocket('ws://127.0.0.1:9090/traffic')
      ws.onmessage = (e) => {
        try { const d = JSON.parse(e.data); up = d.up ?? 0; down = d.down ?? 0 } catch {}
      }
    } catch {}
  }
  function closeWS() { if (ws) { try { ws.close() } catch {} ws = null } }

  const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))

  function fmt(b: number): string {
    if (!b || b < 0) return '0 B/s'
    if (b < 1024) return b.toFixed(0) + ' B/s'
    if (b < 1024 * 1024) return (b / 1024).toFixed(1) + ' KB/s'
    return (b / 1024 / 1024).toFixed(2) + ' MB/s'
  }

  async function loadWL() { wl = await GetWhitelist() }

  async function loadProfiles() {
    try { profiles = await GetProfiles() } catch { profiles = [] }
  }
  async function loadUptime() {
    try { uptime = (await GetUptimeHistory()) || {} } catch { uptime = {} }
  }

  function lastSample(name: string, dep: Record<string, Sample[]>): Sample | null {
    const h = dep[name]; if (!h || !h.length) return null
    return h[h.length - 1]
  }
  function uptimePct(name: string, dep: Record<string, Sample[]>): number {
    const h = dep[name]; if (!h || !h.length) return -1
    const ok = h.filter(s => s.ok).length
    return Math.round((ok / h.length) * 100)
  }
  function lastRttText(name: string, dep: Record<string, Sample[]>): string {
    const s = lastSample(name, dep)
    if (!s || !s.ok) return '—'
    return s.rtt_ms + ' ms'
  }
  function avgRtt(name: string, dep: Record<string, Sample[]>): number {
    const h = dep[name]; if (!h || !h.length) return -1
    const ok = h.filter(s => s.ok)
    if (!ok.length) return -1
    return Math.round(ok.reduce((a, s) => a + s.rtt_ms, 0) / ok.length)
  }
  // Build a smooth area chart in Apple-Health style. Returns SVG path strings
  // for the stroke (top line) and the filled area underneath.
  // `_dep` is passed explicitly so Svelte tracks the reactive dependency.
  function sparkPath(name: string, w: number, h: number, _dep: Record<string, Sample[]>): { line: string; area: string; dots: { cx: number; cy: number }[] } {
    const samples = _dep[name] || []
    const N = 60
    const slice = samples.slice(-N)
    if (slice.length === 0) return { line: '', area: '', dots: [] }
    const rtts = slice.filter(s => s.ok).map(s => s.rtt_ms)
    const max = Math.max(80, ...rtts)
    const stepW = w / (N - 1)
    const off = (N - slice.length) * stepW
    const pad = 3 // breathing room at top
    const pts: { x: number; y: number; ok: boolean }[] = slice.map((s, i) => ({
      x: off + i * stepW,
      y: s.ok ? (h - pad) - (s.rtt_ms / max) * (h - pad * 2) : h - 2,
      ok: s.ok,
    }))
    if (!pts.length) return { line: '', area: '', dots: [] }
    // smooth path via Catmull-Rom-ish midpoint curves
    let line = `M ${pts[0].x} ${pts[0].y}`
    for (let i = 1; i < pts.length; i++) {
      const p0 = pts[i - 1], p1 = pts[i]
      const mx = (p0.x + p1.x) / 2
      line += ` Q ${p0.x} ${p0.y} ${mx} ${(p0.y + p1.y) / 2} T ${p1.x} ${p1.y}`
    }
    const area = `${line} L ${pts[pts.length - 1].x} ${h} L ${pts[0].x} ${h} Z`
    const dots = pts.filter(p => !p.ok).map(p => ({ cx: p.x, cy: h - 4 }))
    return { line, area, dots }
  }

  async function switchTo(name: string) {
    if (switching) return
    const cur = profiles.find((p) => p.active)
    if (cur && cur.name === name) return
    switching = true
    try {
      await SwitchProfile(name)
      await loadProfiles()
      // server changed → drop the cached IP and re-read status after restart
      if (running) { extIP = '—'; await sleep(2200) }
      await refreshStatus()
    } finally { switching = false }
  }

  async function add() {
    const v = inputVal.trim()
    if (!v) return
    await AddRule(tab, v)
    inputVal = ''
    await loadWL()
    dirty = true
  }
  async function remove(v: string) {
    await RemoveRule(tab, v)
    await loadWL()
    dirty = true
  }

  onMount(async () => {
    await loadProfiles()
    await loadUptime()
    await loadWL()
    await refreshStatus()
    statusTimer = setInterval(refreshStatus, 3000)
    uptimeTimer = setInterval(loadUptime, 1000)
  })
  onDestroy(() => { clearInterval(statusTimer); clearInterval(uptimeTimer); closeWS() })

  $: list = wl[tab] ?? []
</script>

<main>
  <header>
    <div class="brand">
      <span class="dot"
        class:on={status === 'ok'}
        class:warn={status === 'connecting'}
        class:bad={status === 'broken'}></span>
      Nekko&nbsp;VPN
    </div>
    <div
      class="ip"
      class:bad={status === 'broken'}
      class:warn={status === 'connecting'}
      title={statusMsg}
      on:click={toggleLog}>
      {#if status === 'off'}отключён
      {:else if status === 'connecting'}подключаюсь…
      {:else if status === 'broken'}⚠ туннель не работает ({extIP})
      {:else}{extIP}{/if}
    </div>
  </header>

  {#if showLog}
    <section class="logbox">
      <div class="logbar">
        <span>журнал (последние ~16 КБ)</span>
        <button on:click={async () => (logTail = await GetLogTail())}>обновить</button>
        <button on:click={() => (showLog = false)}>×</button>
      </div>
      <pre>{logTail || '(пусто)'}</pre>
    </section>
  {/if}

  {#if profiles.length > 0}
    <section class="profiles">
      {#each profiles as p, i}
        <div class="prow" class:active={p.active} class:expanded={expanded === p.name}>
          <div class="row-main" class:dis={switching}
               role="button" tabindex="0"
               on:click={() => !switching && switchTo(p.name)}
               on:keydown={(e) => e.key === 'Enter' && !switching && switchTo(p.name)}>
            <button class="num" type="button"
                    on:click|stopPropagation={() => expanded = expanded === p.name ? null : p.name}>
              {i + 1}
            </button>
            <svg class="spark" viewBox="0 0 200 32" preserveAspectRatio="none">
              <defs>
                <linearGradient id={'g-' + p.name} x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%"   stop-color={p.active ? '#34c759' : '#8a8a8e'} stop-opacity="0.45"/>
                  <stop offset="100%" stop-color={p.active ? '#34c759' : '#8a8a8e'} stop-opacity="0"/>
                </linearGradient>
              </defs>
              {#each [sparkPath(p.name, 200, 32, uptime)] as spark}
                <path d={spark.area} fill={'url(#g-' + p.name + ')'} />
                <path d={spark.line} fill="none"
                      stroke={p.active ? '#34c759' : '#8a8a8e'}
                      stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" />
                {#each spark.dots as d}
                  <circle cx={d.cx} cy={d.cy} r="1.6" fill="#ff453a"/>
                {/each}
              {/each}
            </svg>
            <span class="rtt">
              {#if avgRtt(p.name, uptime) < 0}—
              {:else}{avgRtt(p.name, uptime)}<span class="u">ms</span>{/if}
            </span>
            <span class="dot-active" class:visible={p.active} />
          </div>
          {#if expanded === p.name}
            <div class="row-detail" transition:slide={{ duration: 180, easing: cubicOut }}>
              <div class="dl">
                <span class="k">аптайм</span>
                <span class="v">{uptimePct(p.name, uptime) < 0 ? '—' : uptimePct(p.name, uptime) + '%'} <span class="dim">· {avgRtt(p.name, uptime) < 0 ? '—' : avgRtt(p.name, uptime) + ' ms'}</span></span>
              </div>
              <div class="dl">
                <span class="k">сервер</span>
                <span class="v mono">{p.endpoint || '—'}</span>
              </div>
              <div class="dl">
                <span class="k">протокол</span>
                <span class="v">{p.protocol || '—'}</span>
              </div>
              <div class="dl">
                <span class="k">профиль</span>
                {#if editing === p.name}
                  <input
                    class="lbl-edit"
                    bind:value={editVal}
                    autofocus
                    on:keydown={(e) => { if (e.key === 'Enter') saveLabel(p.name); if (e.key === 'Escape') editing = null }}
                    on:blur={() => saveLabel(p.name)} />
                {:else}
                  <span class="v lbl"
                        on:dblclick={() => { editing = p.name; editVal = p.label }}
                        title="двойной клик — переименовать">{p.label}</span>
                {/if}
              </div>
              {#if !p.active}
                <button class="del-profile" on:click={() => delProfile(p.name)} title="удалить профиль">удалить профиль</button>
              {/if}
            </div>
          {/if}
        </div>
      {/each}
    </section>
  {/if}

  <section class="power">
    <!-- TODO: заменить на сплайн-анимацию кота-маскота.
         Сейчас — простой SVG-кот: ч/б когда выкл, цветной + лёгкое мерцание когда вкл. -->
    <button class="cat" class:on={running} disabled={busy} on:click={toggle} aria-label={running ? 'выключить' : 'включить'}>
      <div class="cat-art">
        <svg viewBox="0 0 120 120" xmlns="http://www.w3.org/2000/svg">
          <defs>
            <linearGradient id="catGrad" x1="0%" y1="0%" x2="100%" y2="100%">
              <stop offset="0%"   stop-color="#ffb86b"/>
              <stop offset="50%"  stop-color="#ff7eb9"/>
              <stop offset="100%" stop-color="#7afcff"/>
            </linearGradient>
          </defs>
          <!-- ears -->
          <path d="M28 38 L38 18 L52 36 Z" fill="url(#catGrad)"/>
          <path d="M92 38 L82 18 L68 36 Z" fill="url(#catGrad)"/>
          <path d="M34 32 L40 24 L46 32 Z" fill="#ff9aa8" opacity=".7"/>
          <path d="M86 32 L80 24 L74 32 Z" fill="#ff9aa8" opacity=".7"/>
          <!-- head -->
          <ellipse cx="60" cy="64" rx="34" ry="32" fill="url(#catGrad)"/>
          <!-- eyes -->
          <ellipse class="eye" cx="48" cy="62" rx="3.5" ry="6" fill="#1a1a1c"/>
          <ellipse class="eye" cx="72" cy="62" rx="3.5" ry="6" fill="#1a1a1c"/>
          <circle cx="49" cy="60" r="1" fill="#fff"/>
          <circle cx="73" cy="60" r="1" fill="#fff"/>
          <!-- nose + mouth -->
          <path d="M58 74 L60 76 L62 74 Z" fill="#ff6b8a"/>
          <path d="M60 76 Q56 80 53 78 M60 76 Q64 80 67 78" stroke="#1a1a1c" stroke-width="1.4" fill="none" stroke-linecap="round"/>
          <!-- whiskers -->
          <g stroke="#1a1a1c" stroke-width="1" stroke-linecap="round" opacity=".6">
            <line x1="32" y1="70" x2="44" y2="71"/>
            <line x1="32" y1="76" x2="44" y2="74"/>
            <line x1="88" y1="70" x2="76" y2="71"/>
            <line x1="88" y1="76" x2="76" y2="74"/>
          </g>
        </svg>
      </div>
      <span class="state">{busy ? 'подождите' : running ? 'вкл' : 'выкл'}</span>
    </button>
  </section>

  <section class="route">
    <div class="route-head" on:click={() => routingOpen = !routingOpen}>
      <span class="route-title">маршрутизация</span>
      <button class="switch" class:on={routingOpen} type="button"
              on:click|stopPropagation={() => routingOpen = !routingOpen}
              aria-label="включить раздел маршрутизации">
        <span class="knob" />
      </button>
    </div>

    <div class="route-body-wrap" class:open={routingOpen}>
      <div class="route-body-inner">
        <div class="route-body">
          <div class="tabs">
            {#each tabs as t}
              <button class:active={tab === t} on:click={() => { tab = t; inputVal = '' }}>{labels[t]}</button>
            {/each}
          </div>

          <div class="adder">
            <input
              placeholder={phold[tab]}
              bind:value={inputVal}
              on:keydown={(e) => e.key === 'Enter' && add()} />
            <button class="add" on:click={add} aria-label="добавить"></button>
          </div>
          <div class="hint-mini">{hints[tab]}</div>
        </div>
      </div>
    </div>
  </section>

  {#if routingOpen && list.length > 0}
    <section class="items-block" transition:slide={{ duration: 200, easing: cubicOut }}>
      {#each list as item}
        <div class="item">
          <span class="val" title={item}>{item}</span>
          <button class="del" on:click={() => remove(item)}>✕</button>
        </div>
      {/each}
    </section>
  {/if}

  {#if dirty}
    <div class="apply-bar">
      <span>изменения не применены</span>
      <button class="apply" disabled={busy} on:click={apply}>применить</button>
    </div>
  {/if}
</main>

<style>
  :global(html, body) { margin: 0; padding: 0; height: 100%; }
  :global(body) {
    font-family: 'IBM Plex Mono', ui-monospace, SFMono-Regular, Menlo, monospace;
    background: #0e0e10;
    color: #ededee;
    user-select: none;
    -webkit-font-smoothing: antialiased;
    -moz-osx-font-smoothing: grayscale;
    font-weight: 500;
    letter-spacing: 0.02em;
    line-height: 1.45;
    font-feature-settings: "calt" 0;
    text-transform: lowercase;
  }
  :global(input) {
    font-family: inherit;
    font-weight: 500;
  }
  :global(:root) {
    --bg: #0e0e10;
    --surface: #18181b;
    --surface-2: #1f1f23;
    --border: rgba(255,255,255,0.06);
    --border-strong: rgba(255,255,255,0.12);
    --text: #ededee;
    --text-muted: #8a8a8e;
    --text-faint: #5b5b60;
    --ok: #34c759;
    --warn: #ff9f0a;
    --bad: #ff453a;
    --radius: 14px;
    --radius-lg: 18px;
    --mono: 'IBM Plex Mono', ui-monospace, SFMono-Regular, Menlo, monospace;
  }

  main {
    display: flex; flex-direction: column;
    height: 100vh; padding: 20px; box-sizing: border-box; gap: 14px;
  }

  /* ─── header ─────────────────────────────────────────── */
  header { display: flex; align-items: center; justify-content: space-between; }
  .brand {
    display: flex; align-items: center; gap: 10px;
    font-weight: 600; font-size: 16px; letter-spacing: -0.02em;
  }
  .dot {
    width: 7px; height: 7px; border-radius: 50%;
    background: var(--text-faint); transition: background .25s;
  }
  .dot.on   { background: var(--ok); }
  .dot.warn { background: var(--warn); }
  .dot.bad  { background: var(--bad); animation: pulse 1.4s infinite; }
  @keyframes pulse { 0%,100% { opacity: 1 } 50% { opacity: .45 } }

  .ip {
    font-family: var(--mono); font-size: 11px;
    color: var(--text-muted); cursor: pointer;
    padding: 6px 10px; border-radius: var(--radius);
    background: var(--surface); border: 1px solid var(--border);
    transition: .15s; letter-spacing: 0;
    text-transform: none;
  }
  .ip:hover { color: var(--text); border-color: var(--border-strong); }
  .ip.warn { color: var(--warn); }
  .ip.bad  { color: var(--bad); }

  /* ─── log box ────────────────────────────────────────── */
  .logbox {
    background: var(--surface); border: 1px solid var(--border);
    border-radius: var(--radius);
    display: flex; flex-direction: column; max-height: 220px; overflow: hidden;
  }
  .logbar {
    display: flex; align-items: center; gap: 8px; padding: 8px 12px;
    font-size: 11px; color: var(--text-muted); border-bottom: 1px solid var(--border);
  }
  .logbar span { flex: 1; }
  .logbar button {
    background: var(--surface-2); color: var(--text-muted);
    border: 1px solid var(--border); border-radius: var(--radius);
    padding: 3px 10px; cursor: pointer; font-size: 11px;
    font-family: inherit; transition: .15s;
  }
  .logbar button:hover { color: var(--text); border-color: var(--border-strong); }
  .logbox pre {
    margin: 0; padding: 10px 12px; font-family: var(--mono);
    font-size: 11px; line-height: 1.5;
    color: var(--text-muted); overflow: auto;
    white-space: pre-wrap; word-break: break-all;
    text-transform: none;
  }

  /* ─── profiles list (vertical with sparkline) ────────── */
  .profiles { display: flex; flex-direction: column; gap: 4px; }

  .prow {
    background: var(--surface); border: 1px solid var(--border);
    border-radius: var(--radius-lg); transition: border-color .15s;
    overflow: hidden;
  }
  .prow:hover { border-color: var(--border-strong); }
  .prow.active { border-color: var(--text-muted); }

  .row-main {
    display: flex; align-items: center; gap: 10px;
    width: 100%; padding: 8px 10px; cursor: pointer;
    box-sizing: border-box;
  }
  .row-main.dis { cursor: default; opacity: .5; }

  .num {
    flex-shrink: 0; width: 22px; height: 22px;
    display: flex; align-items: center; justify-content: center;
    background: var(--surface-2); border: 1px solid var(--border);
    border-radius: var(--radius); color: var(--text-muted);
    font-family: var(--mono); font-size: 11px; font-weight: 500;
    cursor: pointer; transition: .15s; padding: 0;
  }
  .num:hover { color: var(--text); border-color: var(--border-strong); }
  .prow.active .num { color: var(--text); border-color: var(--text-muted); }

  .spark {
    flex: 1; min-width: 0; height: 32px;
    display: block;
    border-radius: 8px;
  }

  .rtt {
    flex-shrink: 0; min-width: 42px;
    font-family: var(--mono); font-size: 11px;
    color: var(--text-muted); text-align: right;
    text-transform: none;
  }
  .rtt .u { opacity: .55; margin-left: 1px; }

  .row-detail {
    padding: 8px 12px 10px 42px;
    border-top: 1px solid var(--border);
    display: flex; flex-direction: column; gap: 4px;
    font-size: 12px;
  }
  .dl { display: flex; justify-content: space-between; gap: 12px; align-items: center; min-height: 22px; }
  .dl .k { color: var(--text-faint); }
  .dl .v { color: var(--text); text-align: right; }
  .dl .v.mono { font-family: var(--mono); text-transform: none; font-size: 11px; }
  .dl .dim { color: var(--text-faint); margin-left: 4px; }

  .lbl-edit {
    background: var(--surface-2); color: var(--text);
    border: 1px solid var(--border-strong); border-radius: 6px;
    font-family: inherit; font-size: 12px; padding: 3px 8px;
    outline: none; max-width: 60%; text-align: right;
    text-transform: none;
  }

  .del-profile {
    align-self: flex-end; margin-top: 4px;
    background: transparent; color: var(--text-faint);
    border: none; cursor: pointer;
    font-family: inherit; font-size: 11px; padding: 4px 0;
    transition: color .15s;
  }
  .del-profile:hover { color: var(--bad); }

  .dot-active {
    flex-shrink: 0; width: 6px; height: 6px; border-radius: 50%;
    background: var(--ok); margin-left: 2px;
    visibility: hidden;
  }
  .dot-active.visible { visibility: visible; }
  .v.lbl { cursor: text; }

  .sw-hint { font-size: 11px; color: var(--text-faint); text-align: center; padding: 4px 0; }

  /* ─── cat mascot button ──────────────────────────────── */
  .power {
    display: flex; flex-direction: column; align-items: center;
    gap: 10px; padding: 8px 0;
  }
  .cat {
    display: flex; flex-direction: column; align-items: center; gap: 8px;
    background: transparent; border: none; padding: 0;
    cursor: pointer; font-family: inherit;
  }
  .cat:disabled { opacity: .6; cursor: default; }
  .cat-art {
    width: 132px; height: 132px;
    display: flex; align-items: center; justify-content: center;
    filter: grayscale(1) brightness(.78);
    transition: filter .6s ease, transform .25s ease;
  }
  .cat-art svg { width: 100%; height: 100%; }
  .cat:hover:not(:disabled) .cat-art { transform: translateY(-2px); }
  .cat.on .cat-art {
    filter: grayscale(0) brightness(1);
    animation: catshimmer 4s ease-in-out infinite;
  }
  @keyframes catshimmer {
    0%, 100% { filter: grayscale(0) brightness(1)   hue-rotate(0deg); }
    50%      { filter: grayscale(0) brightness(1.1) hue-rotate(20deg); }
  }
  /* eyes blink on the "on" state */
  .cat.on .eye { animation: blink 4.5s infinite; transform-origin: center; transform-box: fill-box; }
  @keyframes blink {
    0%, 92%, 100% { transform: scaleY(1); }
    95%           { transform: scaleY(.1); }
  }
  .state {
    font-size: 11px; font-weight: 500; letter-spacing: 0.04em;
    color: var(--text-muted);
  }
  .cat.on .state { color: var(--ok); }

  /* ─── routing block (collapsible) ────────────────────── */
  .route {
    background: var(--surface); border: 1px solid var(--border);
    border-radius: var(--radius-lg);
  }
  .route-head {
    display: flex; align-items: center; justify-content: space-between;
    padding: 14px 16px; cursor: pointer; user-select: none;
  }
  .route-title { font-size: 13px; font-weight: 500; color: var(--text); }

  /* Collapsible body — uses grid-template-rows 0fr/1fr trick. Bulletproof,
     no overflow:hidden quirks that swallowed the input on slide animations. */
  .route-body-wrap {
    display: grid; grid-template-rows: 0fr;
    transition: grid-template-rows .25s cubic-bezier(.4,0,.2,1);
  }
  .route-body-wrap.open { grid-template-rows: 1fr; }
  .route-body-inner { overflow: hidden; min-height: 0; }
  .route-body {
    display: flex; flex-direction: column; gap: 10px;
    padding: 0 12px 12px;
  }

  /* iOS-style switch (bigger, proportional) */
  .switch {
    position: relative; width: 46px; height: 28px;
    border-radius: 999px; border: none; padding: 0;
    background: var(--surface-2); cursor: pointer;
    transition: background .25s;
    flex-shrink: 0;
  }
  .switch .knob {
    position: absolute; top: 2px; left: 2px;
    width: 24px; height: 24px; border-radius: 50%;
    background: #ddd; transition: .25s cubic-bezier(.4,0,.2,1);
    box-shadow: 0 1px 2px rgba(0,0,0,0.3);
  }
  .switch.on { background: var(--ok); }
  .switch.on .knob { left: 20px; background: #fff; }

  .tabs {
    display: flex; gap: 4px;
    background: var(--bg); border: 1px solid var(--border);
    border-radius: var(--radius); padding: 4px;
    height: 40px; box-sizing: border-box;
  }
  .tabs button {
    flex: 1; padding: 0 8px; font-size: 12px; font-weight: 500;
    font-family: inherit; cursor: pointer;
    background: transparent; color: var(--text-muted);
    border: none; border-radius: 10px; transition: .15s;
  }
  .tabs button:hover:not(.active) { color: var(--text); }
  .tabs button.active { background: var(--surface-2); color: var(--text); }

  .adder { display: flex; gap: 6px; align-items: stretch; }
  .adder input {
    flex: 1; min-width: 0;
    padding: 0 14px; height: 40px;
    font-size: 12px; font-family: inherit; border-radius: var(--radius);
    border: 1px solid var(--border); background: var(--bg);
    color: var(--text); outline: none; transition: .15s;
    text-transform: none;
    box-sizing: border-box;
  }
  .adder input::placeholder { color: var(--text-faint); }
  .adder input:focus { border-color: var(--border-strong); }
  .add {
    flex-shrink: 0;
    width: 40px; height: 40px; padding: 0;
    border-radius: var(--radius);
    border: 1px solid var(--border); background: var(--bg);
    color: var(--text-muted);
    cursor: pointer; transition: .15s; font-family: inherit;
    display: flex; align-items: center; justify-content: center;
    line-height: 1;
  }
  .add::before {
    content: ""; width: 13px; height: 13px;
    background:
      linear-gradient(currentColor, currentColor) center/13px 1.5px no-repeat,
      linear-gradient(currentColor, currentColor) center/1.5px 13px no-repeat;
  }
  .add:hover { color: var(--text); border-color: var(--border-strong); }

  .hint-mini {
    font-size: 11px; color: var(--text-faint);
    padding: 0 2px; letter-spacing: 0.02em;
  }

  .items-block {
    display: flex; flex-direction: column; gap: 4px;
    overflow-y: auto;
    scrollbar-gutter: stable;
  }
  .item {
    display: flex; align-items: center; justify-content: space-between;
    background: var(--surface); border: 1px solid var(--border);
    border-radius: var(--radius); padding: 10px 14px;
    transition: border-color .15s;
  }
  .item:hover { border-color: var(--border-strong); }
  .val {
    font-family: var(--mono);
    font-size: 12px; color: var(--text);
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
    text-transform: none;
  }
  .del {
    border: none; background: transparent;
    color: var(--text-faint); cursor: pointer;
    font-size: 12px; padding: 4px 6px; border-radius: var(--radius);
    flex-shrink: 0; transition: .15s; font-family: inherit;
  }
  .del:hover { color: var(--bad); }

  /* ─── apply bar ──────────────────────────────────────── */
  .apply-bar {
    display: flex; align-items: center; justify-content: space-between;
    background: var(--surface); border: 1px solid var(--border-strong);
    border-radius: var(--radius); padding: 10px 14px; font-size: 12px;
    color: var(--text-muted);
  }
  .apply {
    padding: 7px 16px; border-radius: var(--radius); border: none;
    background: var(--text); color: var(--bg);
    font-weight: 600; font-size: 12px; font-family: inherit;
    cursor: pointer; transition: .15s;
  }
  .apply:hover:not(:disabled) { background: #fff; }
  .apply:disabled { opacity: .5; cursor: default; }
</style>
