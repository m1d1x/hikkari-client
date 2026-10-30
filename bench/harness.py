#!/usr/bin/env python3
"""
harness.py — бенчмарк-раннер «белизны» и проходимости egress-конфигов Nekobox.

Что делает:
  1. Читает список egress-кандидатов (профили Nekobox или произвольный JSON).
  2. Для каждого поднимает sing-box как локальный SOCKS-прокси (singbox.py).
  3. Через этот прокси гоняет pipeline проверок (probes.py): IP/ASN, fraud-score,
     JA3/JA4, Cloudflare-trace, латентность и — опционально — реальный CF-челлендж.
  4. Складывает всё в датасет: JSONL (сырьё, append-only) + CSV/Parquet (для анализа)
     и печатает лидерборд в консоль.

Ключевые свойства для быстрой итерации:
  * параллелизм (несколько прокси одновременно — они локальные и не конфликтуют);
  * кэш IP-репутации по exit-IP (репутация меняется медленно — не жжём лимиты IPQS);
  * append-only лог с timestamp — видно дрейф репутации во времени, ничего не затирается.

Запуск:
  python harness.py --profiles ../dist/profiles.json --singbox ../dist/sing-box.exe
  python harness.py --profiles ../dist/profiles.json --singbox ../dist/sing-box.exe \
      --ipqs-key $IPQS_KEY --cf-challenge --concurrency 6
"""
from __future__ import annotations

import argparse
import concurrent.futures as cf
import json
import os
import sys
import time
from datetime import datetime, timezone

import httpx

from probes import (
    probe_cf_trace,
    probe_cloudflare_challenge,
    probe_ipapi,
    probe_ipqs,
    probe_latency,
    probe_tls_fingerprint,
)
from singbox import Profile, load_profiles, singbox_proxy

# ---- простой файловый кэш репутации по IP (медленно меняющийся сигнал) -------
_REP_CACHE_PATH = os.path.join(os.path.dirname(__file__), ".rep-cache.json")


def _load_rep_cache() -> dict[str, dict]:
    try:
        with open(_REP_CACHE_PATH, encoding="utf-8") as f:
            return json.load(f)
    except (OSError, json.JSONDecodeError):
        return {}


def _save_rep_cache(cache: dict[str, dict]) -> None:
    try:
        with open(_REP_CACHE_PATH, "w", encoding="utf-8") as f:
            json.dump(cache, f, ensure_ascii=False)
    except OSError:
        pass


def _now_iso() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


def bench_one(
    profile: Profile,
    *,
    singbox_bin: str,
    ipqs_key: str | None,
    do_cf_challenge: bool,
    rep_cache: dict[str, dict],
    rep_ttl_sec: int,
) -> dict:
    """Полный прогон одного профиля. Возвращает плоскую строку датасета."""
    row: dict = {
        "ts": _now_iso(),
        "profile": profile.name,
        "label": profile.label,
        "egress_type": profile.egress_type,
        "kind": profile.kind,
    }
    row.update(profile.meta)  # прокидываем ручные метки (dc, region, note...)

    t0 = time.perf_counter()
    with singbox_proxy(profile, singbox_bin) as proxy:
        if proxy is None:
            row["up"] = False
            row["error"] = "egress-failed-to-start"
            return row
        row["up"] = True

        # httpx-клиент с маршрутом через локальный SOCKS этого egress
        transport_url = proxy.socks_url
        with httpx.Client(
            proxy=transport_url,
            timeout=15,
            headers={"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) NekkoBench/1.0"},
        ) as client:
            # порядок важен: сперва узнаём exit-IP, он нужен для IPQS и кэша
            ipapi = probe_ipapi(client)
            row.update(ipapi)
            exit_ip = ipapi.get("ip")

            # --- репутация: сперва кэш, потом IPQS ---
            cached = rep_cache.get(exit_ip or "") if exit_ip else None
            if cached and (time.time() - cached.get("_cached_at", 0)) < rep_ttl_sec:
                row.update({k: v for k, v in cached.items() if not k.startswith("_")})
                row["ipqs_cached"] = True
            else:
                ipqs = probe_ipqs(client, ipqs_key, exit_ip)
                row.update(ipqs)
                if exit_ip and ipqs.get("ipqs_ok"):
                    rep_cache[exit_ip] = {**ipqs, "_cached_at": time.time()}

            row.update(probe_tls_fingerprint(client))
            row.update(probe_cf_trace(client))
            row.update(probe_latency(client))

        # тяжёлый CF-челлендж — вне httpx-клиента, свой браузер через тот же порт
        if do_cf_challenge:
            row.update(probe_cloudflare_challenge(proxy.socks_url))

    row["total_ms"] = round((time.perf_counter() - t0) * 1000, 1)
    return row


def score_row(row: dict) -> float:
    """
    Единый «индекс белизны» 0-100 для сортировки лидерборда. Эвристика — крути под себя.
    Высокий = чистый и проходимый.
    """
    if not row.get("up"):
        return 0.0
    score = 100.0
    fs = row.get("fraud_score")
    if isinstance(fs, (int, float)):
        score -= fs                          # прямой штраф за fraud_score
    if row.get("is_hosting"):
        score -= 25                          # datacenter → CF почти всегда челленджит
    if row.get("is_proxy") or row.get("is_vpn"):
        score -= 15
    if row.get("recent_abuse"):
        score -= 20
    cfc = row.get("cf_challenge")
    if cfc == "passed":
        score += 15
    elif cfc == "challenged":
        score -= 20
    elif cfc == "blocked":
        score -= 40
    lat = row.get("latency_ms")
    if isinstance(lat, (int, float)) and lat > 800:
        score -= 10
    return max(0.0, min(100.0, round(score, 1)))


def write_outputs(rows: list[dict], out_dir: str) -> None:
    os.makedirs(out_dir, exist_ok=True)
    stamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")

    # 1) append-only JSONL — сырьё, копится между прогонами (виден дрейф во времени)
    jsonl = os.path.join(out_dir, "runs.jsonl")
    with open(jsonl, "a", encoding="utf-8") as f:
        for r in rows:
            f.write(json.dumps(r, ensure_ascii=False) + "\n")

    # 2) снапшот текущего прогона — CSV всегда, Parquet если есть pandas+pyarrow
    try:
        import pandas as pd

        df = pd.DataFrame(rows)
        df.to_csv(os.path.join(out_dir, f"run-{stamp}.csv"), index=False, encoding="utf-8")
        try:
            df.to_parquet(os.path.join(out_dir, f"run-{stamp}.parquet"), index=False)
        except Exception:  # pyarrow может отсутствовать — не критично
            pass
    except ImportError:
        # без pandas — руками пишем CSV по объединённому набору колонок
        import csv

        cols: list[str] = []
        for r in rows:
            for k in r:
                if k not in cols:
                    cols.append(k)
        with open(os.path.join(out_dir, f"run-{stamp}.csv"), "w", newline="", encoding="utf-8") as f:
            w = csv.DictWriter(f, fieldnames=cols)
            w.writeheader()
            w.writerows(rows)

    print(f"\n[+] Датасет: {jsonl}  и  run-{stamp}.csv  (dir: {out_dir})")


def print_leaderboard(rows: list[dict]) -> None:
    ranked = sorted(rows, key=score_row, reverse=True)
    print("\n" + "=" * 92)
    print(f"{'score':>5}  {'profile':<14} {'type':<10} {'country':<7} {'fraud':>5} "
          f"{'host':>4} {'cf':<11} {'lat':>6}  asn")
    print("-" * 92)
    for r in ranked:
        print(
            f"{score_row(r):>5.0f}  "
            f"{str(r.get('profile',''))[:14]:<14} "
            f"{str(r.get('egress_type',''))[:10]:<10} "
            f"{str(r.get('country','?')):<7} "
            f"{str(r.get('fraud_score','-')):>5} "
            f"{('Y' if r.get('is_hosting') else '-'):>4} "
            f"{str(r.get('cf_challenge','-'))[:11]:<11} "
            f"{str(r.get('latency_ms','-')):>6}  "
            f"{str(r.get('asname') or r.get('asn') or '')[:28]}"
        )
    print("=" * 92)


def load_candidates(args) -> list[Profile]:
    """Источник кандидатов: profiles.json Nekobox ИЛИ произвольный список egress-ов."""
    if args.profiles:
        profs = load_profiles(args.profiles)
    elif args.egress_json:
        with open(args.egress_json, encoding="utf-8") as f:
            raw = json.load(f)
        profs = [
            Profile(
                name=e["name"],
                label=e.get("label", e["name"]),
                kind=e.get("kind", "outbound"),
                config=e["config"],
                meta=e.get("meta", {}),
            )
            for e in raw
        ]
    else:
        sys.exit("нужен --profiles или --egress-json")

    if args.only:
        wanted = {x.strip() for x in args.only.split(",")}
        profs = [p for p in profs if p.name in wanted]
    return profs


def main() -> None:
    ap = argparse.ArgumentParser(description="Nekobox egress whiteness/passability benchmark")
    ap.add_argument("--profiles", help="путь к nekkobox profiles.json")
    ap.add_argument("--egress-json", help="альтернатива: JSON-массив {name,kind,config,meta}")
    ap.add_argument("--singbox", required=True, help="путь к sing-box.exe")
    ap.add_argument("--only", help="прогнать только эти профили (через запятую)")
    ap.add_argument("--ipqs-key", default=os.getenv("IPQS_KEY"), help="ключ IPQualityScore (или env IPQS_KEY)")
    ap.add_argument("--cf-challenge", action="store_true", help="реальный CF-челлендж через Playwright (медленно)")
    ap.add_argument("--concurrency", type=int, default=4, help="сколько egress-ов гонять параллельно")
    ap.add_argument("--rep-ttl", type=int, default=21600, help="TTL кэша репутации, сек (по умолч. 6ч)")
    ap.add_argument("--out", default=os.path.join(os.path.dirname(__file__), "results"))
    args = ap.parse_args()

    profiles = load_candidates(args)
    if not profiles:
        sys.exit("нет кандидатов для прогона")

    print(f"[*] Кандидатов: {len(profiles)} | параллелизм: {args.concurrency} | "
          f"CF-челлендж: {'вкл' if args.cf_challenge else 'выкл'}")

    rep_cache = _load_rep_cache()
    rows: list[dict] = []

    # CF-челлендж запускает браузер — при нём режем параллелизм, иначе съест RAM
    workers = 1 if args.cf_challenge else max(1, args.concurrency)

    with cf.ThreadPoolExecutor(max_workers=workers) as pool:
        futs = {
            pool.submit(
                bench_one,
                p,
                singbox_bin=args.singbox,
                ipqs_key=args.ipqs_key,
                do_cf_challenge=args.cf_challenge,
                rep_cache=rep_cache,
                rep_ttl_sec=args.rep_ttl,
            ): p
            for p in profiles
        }
        for fut in cf.as_completed(futs):
            p = futs[fut]
            try:
                row = fut.result()
            except Exception as e:  # noqa: BLE001
                row = {"ts": _now_iso(), "profile": p.name, "up": False, "error": f"crash:{e}"}
            rows.append(row)
            up = "OK " if row.get("up") else "DOWN"
            print(f"  [{up}] {p.name:<14} ip={row.get('ip','?')} "
                  f"fraud={row.get('fraud_score','-')} cf={row.get('cf_challenge','-')}")

    _save_rep_cache(rep_cache)
    write_outputs(rows, args.out)
    print_leaderboard(rows)


if __name__ == "__main__":
    main()
