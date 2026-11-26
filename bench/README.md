# Nekobox egress benchmark

Гоняет каждый egress-конфиг (профиль Nekobox или произвольный sing-box outbound)
через локальный SOCKS-прокси и собирает по нему сводку: IP/ASN, hosting-флаг,
fraud-score, JA3/JA4 TLS-фингерпринт, дата-центр Cloudflare, латентность и —
опционально — реально ли egress проходит Cloudflare-челлендж. Результат копится в
датасет (JSONL + CSV/Parquet) с лидербордом, чтобы быстро сравнивать настройки.

## Как это устроено

```
профиль → sing-box (mixed inbound на 127.0.0.1:PORT) → egress
                          │
      httpx через socks5://127.0.0.1:PORT ──► pipeline проверок ──► строка датасета
```

Каждый egress поднимается как **локальный прокси-порт** (без TUN/auto_route), поэтому
десятки конфигов гоняются параллельно и независимо. Все проверки идут **через прокси** —
значит меряют выходной IP конфига, а не твою машину.

| Файл | Роль |
|------|------|
| `harness.py` | оркестратор: параллелизм, кэш репутации, вывод датасета, лидерборд |
| `singbox.py` | генерит sing-box-конфиг под профиль и поднимает/убивает процесс |
| `probes.py`  | сами проверки (ip-api, IPQS, tls.peet.ws, cf-trace, латентность, CF-челлендж) |

## Установка

```bash
pip install -r requirements.txt
# для флага --cf-challenge дополнительно:
playwright install chromium
```

## Запуск

```bash
# минимум — профили Nekobox через движковый sing-box
python harness.py --profiles ../dist/profiles.json --singbox ../dist/sing-box.exe

# только конкретные профили
python harness.py --profiles ../dist/profiles.json --singbox ../dist/sing-box.exe --only nekko-hy2,main

# с fraud-score (IPQualityScore) и реальным CF-челленджем
IPQS_KEY=xxxx python harness.py --profiles ../dist/profiles.json \
    --singbox ../dist/sing-box.exe --cf-challenge --concurrency 6

# произвольный список egress-ов (не из Nekobox) — см. egress.example.json
python harness.py --egress-json egress.example.json --singbox ../dist/sing-box.exe
```

## Ключевые флаги

| Флаг | Смысл |
|------|-------|
| `--ipqs-key` / `IPQS_KEY` | ключ IPQualityScore; без него fraud-score просто пропускается |
| `--cf-challenge` | реальный проход Cloudflare через Playwright (медленно, режет параллелизм до 1) |
| `--concurrency N` | сколько egress-ов гонять параллельно (по умолч. 4) |
| `--rep-ttl SEC` | TTL кэша репутации по IP (по умолч. 6ч) — чтобы не жечь лимиты IPQS |

## Датасет

- `results/runs.jsonl` — **append-only** сырьё, копится между прогонами (виден дрейф репутации во времени).
- `results/run-<timestamp>.csv` / `.parquet` — снапшот конкретного прогона для анализа в pandas.

Пример анализа:

```python
import pandas as pd
df = pd.read_json("results/runs.jsonl", lines=True)
# средний fraud-score по типу egress
print(df.groupby("egress_type")["fraud_score"].mean())
# что реально проходит Cloudflare
print(df[df.cf_challenge == "passed"][["profile","asname","ja4","fraud_score"]])
```

## Как читать результат

- **`is_hosting=True` или высокий `fraud_score`** → IP из датацентра, Cloudflare почти
  всегда даст челлендж. Лечится сменой egress на residential/mobile ASN.
- **`cf_challenge=challenged` при чистом IP** → почти наверняка дело в **TLS-фингерпринте**
  (`ja4`). Сравни ja4 своего egress с реальным Chrome; для vless крути `utls.fingerprint`.
- **`cf_challenge=blocked` (1020)** → IP в блэклисте CF; меняй узел.

> Замечание из первого прогона: `nekko-hy2` вышел через AEZA-AS (это хостинг), но ip-api
> пометил `is_hosting=false`. Ip-api по датацентрам врёт — именно поэтому для честной
> «белизны» нужен `--ipqs-key`: у IPQS база датацентров/proxy заметно точнее.

## Что можно докинуть

- ещё источники репутации: AbuseIPDB, Scamalytics, Spamhaus DNSBL (в `probes.py` по образцу);
- прогон против конкретной твоей цели за Cloudflare (заменить `target` в `probe_cloudflare_challenge`);
- матрицу «конфиг × настройка» (перебор `utls.fingerprint`, `flow`, портов) — сгенерить
  вариации в `--egress-json` и сравнить по лидерборду.
```
