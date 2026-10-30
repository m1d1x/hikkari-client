"""
singbox.py — поднимает ОДИН конфиг через sing-box как локальный SOCKS-прокси.

Идея: для каждого профиля мы генерируем минимальный sing-box-конфиг, где есть
локальный `mixed`-инбаунд (SOCKS5+HTTP на 127.0.0.1:PORT) и ровно один egress —
outbound/endpoint из профиля. Никакого TUN и auto_route: это НЕ системный VPN, а
просто локальный прокси-порт, поэтому десятки таких можно поднимать параллельно на
разных портах и гонять через них проверки независимо.

Профили берём как есть из nekkobox `profiles.json` — они уже распарсены движком в
готовые sing-box-блоки, так что ссылки повторно парсить не нужно.
"""
from __future__ import annotations

import json
import os
import socket
import subprocess
import tempfile
import time
from contextlib import contextmanager
from dataclasses import dataclass, field
from typing import Any, Iterator


@dataclass
class Profile:
    """Один egress-кандидат: имя + готовый sing-box-блок (outbound или endpoint)."""

    name: str
    label: str
    kind: str                       # "outbound" | "endpoint"
    config: dict[str, Any]
    meta: dict[str, Any] = field(default_factory=dict)  # произвольные метки: dc, region...

    @property
    def egress_type(self) -> str:
        return str(self.config.get("type", "unknown"))


def load_profiles(profiles_json_path: str) -> list[Profile]:
    """Читает nekkobox profiles.json → список Profile."""
    with open(profiles_json_path, encoding="utf-8") as f:
        data = json.load(f)
    out: list[Profile] = []
    for p in data.get("profiles", []):
        out.append(
            Profile(
                name=p["name"],
                label=p.get("label", p["name"]),
                kind=p.get("kind", "outbound"),
                config=dict(p["config"]),
            )
        )
    return out


def _free_port() -> int:
    """Просим ОС выдать свободный порт (закрываем сразу — гонка маловероятна)."""
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def _wait_port(port: int, timeout: float) -> bool:
    """Ждём, пока sing-box откроет локальный порт (значит, инбаунд поднялся)."""
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
            s.settimeout(0.4)
            if s.connect_ex(("127.0.0.1", port)) == 0:
                return True
        time.sleep(0.15)
    return False


def build_singbox_config(profile: Profile, listen_port: int) -> dict[str, Any]:
    """
    Собираем минимальный конфиг: mixed-инбаунд → egress профиля → direct для
    служебных резолвов. route.final гонит ВЕСЬ трафик в egress.
    """
    tag = profile.config.get("tag") or f"egress-{profile.name}"
    egress = dict(profile.config)
    egress["tag"] = tag

    cfg: dict[str, Any] = {
        # тихий лог: нам важны не строки sing-box, а результат проверок
        "log": {"level": "error", "timestamp": False},
        "inbounds": [
            {
                "type": "mixed",
                "tag": "local",
                "listen": "127.0.0.1",
                "listen_port": listen_port,
                # без сниффинга — нам нужен чистый egress, а не роутинг по доменам
                "sniff": False,
            }
        ],
        "outbounds": [{"type": "direct", "tag": "direct"}],
        "route": {"final": tag, "auto_detect_interface": True},
    }

    # sing-box 1.13: wireguard/awg живут в top-level "endpoints", всё прочее — в "outbounds"
    if profile.kind == "endpoint":
        cfg["endpoints"] = [egress]
    else:
        cfg["outbounds"].insert(0, egress)

    return cfg


@dataclass
class RunningProxy:
    port: int
    proc: subprocess.Popen
    config_path: str

    @property
    def socks_url(self) -> str:
        return f"socks5://127.0.0.1:{self.port}"


@contextmanager
def singbox_proxy(
    profile: Profile,
    singbox_bin: str,
    *,
    startup_timeout: float = 12.0,
    workdir: str | None = None,
) -> Iterator[RunningProxy | None]:
    """
    Контекст-менеджер: поднимает sing-box для профиля, отдаёт RunningProxy
    (или None, если egress не поднялся за timeout), гарантированно убивает процесс.

    Использование:
        with singbox_proxy(p, "sing-box.exe") as proxy:
            if proxy: requests.get(..., proxies={"all": proxy.socks_url})
    """
    port = _free_port()
    cfg = build_singbox_config(profile, port)

    fd, cfg_path = tempfile.mkstemp(prefix=f"bench-{profile.name}-", suffix=".json", dir=workdir)
    with os.fdopen(fd, "w", encoding="utf-8") as f:
        json.dump(cfg, f, ensure_ascii=False)

    # sing-box run -c config.json ; stdout/stderr гасим — весь сигнал в проверках
    proc = subprocess.Popen(
        [singbox_bin, "run", "-c", cfg_path],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        cwd=workdir or os.path.dirname(os.path.abspath(singbox_bin)),
    )

    running: RunningProxy | None = None
    try:
        if _wait_port(port, startup_timeout) and proc.poll() is None:
            running = RunningProxy(port=port, proc=proc, config_path=cfg_path)
        yield running
    finally:
        if proc.poll() is None:
            proc.terminate()
            try:
                proc.wait(timeout=4)
            except subprocess.TimeoutExpired:
                proc.kill()
        try:
            os.unlink(cfg_path)
        except OSError:
            pass
