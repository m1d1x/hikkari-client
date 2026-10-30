"""
probes.py — набор проверок, которые гоняются ЧЕРЕЗ локальный SOCKS-порт прокси.

Каждый probe:
  * принимает httpx.Client, уже настроенный на socks5://127.0.0.1:PORT
  * возвращает dict с плоскими полями (легко ложатся в колонки датасета)
  * НИКОГДА не бросает наружу — ошибка сети превращается в {"<name>_ok": False, ...}

Важно: все HTTP-запросы идут через прокси, значит меряют выходной IP КОНФИГА,
а не твою домашнюю машину. Это тот самый нюанс, на котором обычно ошибаются.
"""
from __future__ import annotations

import time
from typing import Any

import httpx


# ---------------------------------------------------------------------------
# 1. IP + ASN + hosting-флаг  (ip-api.com — бесплатно, без ключа)
# ---------------------------------------------------------------------------
def probe_ipapi(client: httpx.Client) -> dict[str, Any]:
    """Базовая идентификация выходного узла: IP, страна, ASN, isp, hosting?"""
    fields = "status,country,countryCode,regionName,city,isp,org,as,asname,mobile,proxy,hosting,query"
    try:
        r = client.get(f"http://ip-api.com/json/?fields={fields}", timeout=12)
        j = r.json()
        if j.get("status") != "success":
            return {"ipapi_ok": False, "ipapi_err": j.get("message", "fail")}
        return {
            "ipapi_ok": True,
            "ip": j.get("query"),
            "country": j.get("countryCode"),
            "city": j.get("city"),
            "asn": j.get("as"),
            "asname": j.get("asname"),
            "isp": j.get("isp"),
            "org": j.get("org"),
            # ip-api сам помечает datacenter/hosting и мобильных операторов —
            # hosting=True почти всегда означает "Cloudflare даст челлендж"
            "is_hosting": bool(j.get("hosting")),
            "is_mobile": bool(j.get("mobile")),
            "ipapi_proxy_flag": bool(j.get("proxy")),
        }
    except Exception as e:  # noqa: BLE001 — probe не имеет права падать
        return {"ipapi_ok": False, "ipapi_err": type(e).__name__}


# ---------------------------------------------------------------------------
# 2. Fraud-score / proxy-detect  (IPQualityScore — нужен API-ключ, опционально)
# ---------------------------------------------------------------------------
def probe_ipqs(client: httpx.Client, api_key: str | None, exit_ip: str | None) -> dict[str, Any]:
    """
    Числовой fraud_score 0-100 + флаги proxy/vpn/tor/recent_abuse.
    IPQS резолвит по IP, поэтому ходить через прокси не обязательно — важно лишь
    передать ПРАВИЛЬНЫЙ exit_ip (его отдаёт probe_ipapi). Ключ можно не давать —
    тогда шаг тихо пропускается.
    """
    if not api_key or not exit_ip:
        return {"ipqs_ok": False, "ipqs_skipped": True}
    try:
        # намеренно с ПРЯМОГО соединения (без прокси): нам нужна оценка IP, а не маршрут
        with httpx.Client(timeout=15) as direct:
            url = f"https://ipqualityscore.com/api/json/ip/{api_key}/{exit_ip}"
            r = direct.get(url, params={"strictness": 1, "allow_public_access_points": "true"})
            j = r.json()
        if not j.get("success", True):
            return {"ipqs_ok": False, "ipqs_err": j.get("message", "fail")}
        return {
            "ipqs_ok": True,
            "fraud_score": j.get("fraud_score"),
            "is_proxy": bool(j.get("proxy")),
            "is_vpn": bool(j.get("vpn")),
            "is_tor": bool(j.get("tor")),
            "recent_abuse": bool(j.get("recent_abuse")),
            "connection_type": j.get("connection_type"),
            "abuse_velocity": j.get("abuse_velocity"),
        }
    except Exception as e:  # noqa: BLE001
        return {"ipqs_ok": False, "ipqs_err": type(e).__name__}


# ---------------------------------------------------------------------------
# 3. TLS-фингерпринт JA3/JA4  (tls.peet.ws — критично для Cloudflare)
# ---------------------------------------------------------------------------
def probe_tls_fingerprint(client: httpx.Client) -> dict[str, Any]:
    """
    JA3/JA4 именно ЭТОГО соединения через прокси. Cloudflare часто режет не по IP,
    а по несовпадению TLS-фингерпринта с заявленным браузером — поэтому два прокси
    с одинаковой репутацией могут вести себя по-разному.
    """
    try:
        r = client.get("https://tls.peet.ws/api/all", timeout=15)
        j = r.json()
        tls = j.get("tls", {}) or {}
        return {
            "tls_ok": True,
            "ja3": tls.get("ja3"),
            "ja3_hash": tls.get("ja3_hash"),
            "ja4": tls.get("ja4"),
            "peet_ip": j.get("ip"),
            "http_version": j.get("http_version"),
        }
    except Exception as e:  # noqa: BLE001
        return {"tls_ok": False, "tls_err": type(e).__name__}


# ---------------------------------------------------------------------------
# 4. Cloudflare edge trace  (cdn-cgi/trace — быстрый признак "как CF нас видит")
# ---------------------------------------------------------------------------
def probe_cf_trace(client: httpx.Client) -> dict[str, Any]:
    """
    Cloudflare отдаёт key=value: свой IP по мнению CF, дата-центр (colo), warp-статус.
    Быстро (текст, не браузер), даёт понять, к какому эджу CF нас цепляет.
    """
    try:
        r = client.get("https://cloudflare.com/cdn-cgi/trace", timeout=12)
        kv = dict(
            line.split("=", 1) for line in r.text.strip().splitlines() if "=" in line
        )
        return {
            "cf_trace_ok": True,
            "cf_ip": kv.get("ip"),
            "cf_colo": kv.get("colo"),   # трёхбуквенный код дата-центра CF (напр. FRA)
            "cf_warp": kv.get("warp"),
            "cf_loc": kv.get("loc"),
        }
    except Exception as e:  # noqa: BLE001
        return {"cf_trace_ok": False, "cf_trace_err": type(e).__name__}


# ---------------------------------------------------------------------------
# 5. Латентность egress  (медиана нескольких запросов к стабильному эндпоинту)
# ---------------------------------------------------------------------------
def probe_latency(client: httpx.Client, samples: int = 3) -> dict[str, Any]:
    """Медианная задержка HTTPS-запроса через прокси — груборезкий показатель скорости."""
    times: list[float] = []
    for _ in range(samples):
        try:
            t0 = time.perf_counter()
            client.get("https://www.cloudflare.com/cdn-cgi/trace", timeout=12)
            times.append((time.perf_counter() - t0) * 1000)
        except Exception:  # noqa: BLE001
            continue
    if not times:
        return {"latency_ok": False}
    times.sort()
    return {
        "latency_ok": True,
        "latency_ms": round(times[len(times) // 2], 1),
        "latency_min_ms": round(times[0], 1),
        "latency_samples": len(times),
    }


# ---------------------------------------------------------------------------
# 6. Прохождение Cloudflare-челленджа  (Playwright — тяжёлый, опциональный)
# ---------------------------------------------------------------------------
def probe_cloudflare_challenge(socks_url: str, target: str = "https://nowsecure.nl") -> dict[str, Any]:
    """
    Реально ли egress проходит Cloudflare JS-challenge. Требует Playwright
    (`pip install playwright && playwright install chromium`). Заходим через прокси
    на защищённый CF сайт и смотрим:
      passed     — получили cf_clearance / контент отдался
      challenged — застряли на interstitial ("Checking your browser…")
      blocked    — 1020 / Access denied / таймаут
    Если Playwright не установлен — тихо отдаём skipped, harness не падает.
    """
    try:
        from playwright.sync_api import sync_playwright
    except ImportError:
        return {"cf_challenge": "skipped", "cf_challenge_reason": "playwright-not-installed"}

    # socks5://127.0.0.1:PORT → server-форма для Chromium
    server = socks_url.replace("socks5://", "socks5://")
    try:
        with sync_playwright() as pw:
            browser = pw.chromium.launch(headless=True, proxy={"server": server})
            ctx = browser.new_context()
            page = ctx.new_page()
            resp = page.goto(target, wait_until="domcontentloaded", timeout=30000)
            status = resp.status if resp else 0
            # дать челленджу шанс либо решиться, либо застрять
            page.wait_for_timeout(6000)
            html = page.content().lower()
            title = (page.title() or "").lower()
            has_clearance = any(c["name"] == "cf_clearance" for c in ctx.cookies())

            challenged = any(
                s in html or s in title
                for s in ("checking your browser", "just a moment", "verify you are human")
            )
            blocked = status in (403, 1020) or "access denied" in html or "error 1020" in html

            if has_clearance or (status == 200 and not challenged and not blocked):
                verdict = "passed"
            elif blocked:
                verdict = "blocked"
            elif challenged:
                verdict = "challenged"
            else:
                verdict = f"unknown-{status}"

            browser.close()
            return {"cf_challenge": verdict, "cf_status": status, "cf_clearance": has_clearance}
    except Exception as e:  # noqa: BLE001
        return {"cf_challenge": "error", "cf_challenge_reason": type(e).__name__}
