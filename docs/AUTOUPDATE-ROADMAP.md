# AUTOUPDATE-ROADMAP

Автообновление клиента с наших серверов, по модели Telegram, на апдейтере из Nekko.
Сначала Windows. Статус: фазы 0–4 и 6 готовы (6 ждёт сервер). Дата: 2026-10-30.

---

## 0. Решения

- **Апдейтер берём из Nekko целиком** (`updater/nekko-updater`, см. `updater/VENDORED.md`):
  подписанный ed25519-манифест, дешёвая проба `latest.json`, контент-адресные `blobs/<sha256>`
  (качаем только изменившиеся файлы), замена двумя rename с `.old`, испытательный старт и
  авто-откат, список отказанных версий, утилита публикации `nekko-manifest`.
  Windows-половину, которой в Nekko нет, пишем здесь и переносим туда обратно.
- **Обновление всегда включено**, отключить нельзя. Качается само в фоне, применяется кнопкой
  «Перезапустить» (как в Telegram). Слишком старая версия (`minimum_version`) → блокирующий экран.
- **UAC — один раз, при установке.** Дальше его нет ни при запуске, ни при обновлении: всё
  привилегированное делает служба (`LocalSystem`), GUI — обычный процесс (`asInvoker`).
  Так устроены WireGuard, Tailscale, Mullvad, Proton.
- **Без сторонних установщиков и фреймворков.** Установщик, служба, лаунчер — свои.
- **Без Authenticode-сертификата.** Доверие обновлению — только подпись манифеста; файлы,
  скачанные самим апдейтером, не получают Mark-of-the-Web, так что `unblock.bat` им не нужен.
  SmartScreen будет только на первом скачивании установщика из браузера.

## 1. Раскладка на диске

```
C:\Program Files\Hikkari\        ← пишет только SYSTEM/админ
    Hikkari.exe                  ← лаунчер: путь не меняется, на него ярлык
    HikkariService.exe           ← служба: TUN, sing-box, обновления
    hikkari-updater.exe          ← CLI над nekko-updater, зовёт служба
    app\                         ← текущая версия: GUI, nekkobox.dll, sing-box, wintun, VERSION
    app.old\                     ← предыдущая, пока новая на испытании
    .staging\                    ← tree\, parts\, READY.json, PENDING.json, REFUSED.json
    core\                        ← служба: свой config.json, cache.db, service.log
%LOCALAPPDATA%\Hikkari\          ← профили, конфиг GUI, логи, кэши (не трогаются заменой)
```

- **Данные пользователя — в `%LOCALAPPDATA%`, а не в `ProgramData`.** ProgramData выбирали,
  чтобы его читала служба; но служба получает конфиг **содержимым** через pipe и держит свою
  копию в `core\`, так что общая папка не нужна. Per-user папка не требует настройки ACL
  установщиком и не показывает профили одного пользователя другим (как на macOS).

- **Бинарники — только в `Program Files`.** Служба исполняет их от SYSTEM; если бы они лежали
  в папке, куда может писать пользователь (`%LocalAppData%`), любая программа подменила бы
  `sing-box.exe` и получила SYSTEM.
- **Лаунчер, служба и апдейтер живут вне `app\`.** Windows не даёт переименовать папку, внутри
  которой запущен exe или загружена dll, — поэтому заменяет её процесс снаружи, после того как
  всё внутри завершилось. Это и делает два rename из Nekko рабочими на Windows.
- **`.staging\` — рядом с `app\`, не внутри** (иначе первый rename унёс бы его с собой);
  `Layout::prepare_dirs` это проверяет.

## 2. Как идёт обновление

```
служба, каждые 30 мин и при запуске GUI:  latest.json ──(новее?)──▶ manifest.json ──verify──▶ prepare → .staging\tree
                                                                              │ READY.json
GUI:                 баннер «Доступно X — Перезапустить»  ◀──────── pipe ────┘
клик:                GUI ─▶ служба: apply ─▶ GUI выходит
служба:              ждёт выхода GUI → стоп sing-box → rename app→app.old, tree→app → PENDING
                     → пишет «готово» лаунчеру
лаунчер:             recover() → старт app\ GUI → GUI ок → confirm() (app.old удалён)
                     GUI умер / 2 старта без confirm → rollback → старая версия + REFUSED
служба:              туннель был включён → поднимает снова
```

- Пока туннель включён, проверка и докачка идут **через туннель** — домен обновлений могут
  блокировать. Плюс зеркала канала (подпись делает зеркалам доверие ненужным).
- Применение — только по клику (или при следующем запуске, если пользователь закрыл приложение).

## 3. Фазы

### Phase 0 — апдейтер в репозитории ✅
- [x] `updater/nekko-updater` перенесён из Nekko, 85 тестов зелёные.
- [x] `swap::apply` работает на Windows (раньше — заглушка «not implemented»).
- [x] Провал первого rename больше не оставляет висящий `PENDING.json` (+ тест).
- [x] Windows: staging должен быть на том же диске и **не внутри** установки.
- [x] CI `updater.yml`: `cargo fmt --check` + `cargo test` на windows-latest и ubuntu-latest.

### Phase 1 — `hikkari-updater.exe` (Rust, CLI над крейтом) ✅
- [x] HTTP-транспорт на `ureq` (blocking, без gzip — иначе Range-докачка ломается), `--proxy`.
- [x] Команды `status | check | update | apply | recover | confirm | rollback`, вывод — JSON-строки
      на stdout (служба читает прогресс и результат), диагностика — stderr.
- [x] Установленная версия — из `app\VERSION` (файл внутри дерева, покрыт манифестом), а не из
      бинаря апдейтера: он живёт вне `app\` и меняется реже.
- [x] Канал и ключи вшиваются при сборке: `HIKKARI_UPDATE_URL`, `HIKKARI_UPDATE_KEYS=id:hex[,id:hex]`.
      `--base-url`/`--trust` есть только в debug-сборке — release работает от SYSTEM и доверяет
      ровно тому, с чем собран.
- [x] Сквозные тесты через настоящий бинарь и HTTP: публикация → check → update (неизменённые
      файлы не качаются) → apply → испытание → confirm; откат после 2 стартов и отказ повторно
      ставить ту же версию; принудительный rollback; mandatory; чужая подпись ничего не ставит.
- Манифест `asInvoker` не нужен: эвристика «installer detection» Windows касается только
  32-битных exe, запущенных интерактивно; наш — 64-битный и запускается службой.

### Phase 2 — `HikkariService.exe` (Go, `x/sys/windows/svc`) ✅
- [x] sing-box запускает служба (`core/winsvc`, LocalSystem); GUI-движок на Windows — клиент pipe
      (`vpn.ServiceManager`). Job Object, taskkill-подметание, flushdns — внутри службы.
- [x] Pipe `\\.\pipe\hikkari-service` (`core/svcproto`): SYSTEM/админы — всё, интерактивные
      пользователи — чтение/запись, сетевые — запрет. Один JSON-запрос на соединение:
      `start` (с содержимым конфига), `stop`, `status`.
- [x] **Санитайзер конфига** (`svcproto.Sanitize`) — граница безопасности: от SYSTEM sing-box
      открывает пути из конфига, поэтому разрешён только список секций, `log.output` убирается,
      `cache_file.path` принудительно в `core\`, Clash API только на loopback и без `external_ui`,
      любые `*_path` / `*directory` и локальные rule-set — отказ. Тесты на реальном конфиге GUI.
- [x] Служба отказывается работать не из `Program Files\Hikkari` (иначе SYSTEM исполнял бы
      бинарники из папки, куда пишет пользователь).
- [x] `HikkariService.exe install` (двойной клик → один UAC): копирует сборку в
      `Program Files\Hikkari`, переносит профили portable-версии в `%LOCALAPPDATA%\Hikkari`,
      регистрирует службу (автозапуск, перезапуск при падении), запускает. `uninstall`, `debug`.
- [x] GUI: `requireAdministrator` → `asInvoker`; данные — `%LOCALAPPDATA%\Hikkari`.
- [x] CI: zip в целевой раскладке (`Hikkari\HikkariService.exe`, `hikkari-updater.exe`, `app\`,
      `app\VERSION`); smoke-тест на Windows-раннере — установка, отказ санитайзера, подъём TUN
      через службу, Clash API, остановка, удаление. `engine.yml`: vet/test на Windows и Linux.
- Известное: пользователь, который может запустить VPN, может направить трафик машины как
  угодно — это суть VPN-клиента (так же у Tailscale). Файловая система закрыта санитайзером.

### Phase 3 — цикл обновлений + лаунчер `Hikkari.exe` ✅
- [x] Служба (`winsvc/updates_windows.go`): первая проверка через 1 мин после старта, дальше каждые
      30 мин, плюс `check_update` при каждом запуске GUI (не чаще раза в 5 мин). Проверка — это
      ~100-байтная проба; манифест качается только при новой версии, поэтому интервал можно
      держать коротким. Состояние (`idle/downloading/ready/error/unavailable`, прогресс, версия,
      mandatory) — в `status` pipe.
- [x] Pipe: `apply_update` (гасит ядро, `apply` с 3 попытками — антивирус может держать файл,
      поднимает ядро на новой версии, если туннель был включён), `recover_update` (гасит ядро
      только если реально есть что переставлять), `confirm_update`, `rollback_update`.
      Долгая загрузка не блокирует подключение/отключение: у обновлений свой замок.
- [x] Лаунчер `Hikkari.exe` (без консоли, без админа): `recover` → старт `app\nekkobox.exe` →
      версия на испытании, упавшая с ошибкой в первые 20 с, откатывается сразу и стартует старая;
      живая через 20 с (или закрытая штатно) — подтверждается. Лог — `%LOCALAPPDATA%\Hikkari\launcher.log`.
- [x] Движок: `UpdateStatus` / `CheckUpdate` / `RestartToUpdate` (contract.md). GUI: плашка
      «Доступна версия X — Перезапустить» (для mandatory — «Нужно обновиться»), опрос раз в 5 с.
- [x] Закрытие окна идёт через `onWindowClose` → `Shutdown` → туннель гасится. Ядро теперь живёт в
      службе, а не в процессе GUI, и `State.dispose` при закрытии окна не гарантирован.
- [x] CI: smoke-тест настоящего обновления на Windows — апдейтер с локальным каналом и временным
      ключом, публикация «1.0.1», check → staged → apply при работающем ядре → ядро снова
      поднято на новой версии → trial → confirm.
- [ ] Single-instance; передача «показать окно» уже запущенному GUI.

### Phase 4 — установщик `HikkariSetup.exe` (Go) ✅
- [x] Один файл: весь комплект вшит (`cmd/hikkari-setup`, `go:embed`). Один UAC → распаковка в
      `Program Files\Hikkari.setup` (не в `%TEMP%`: пока установщик копирует, программа того же
      пользователя без прав могла бы подменить там бинарь, который потом запустит SYSTEM) →
      `winsvc.Install` → предложение запустить приложение **без** прав (через explorer.exe).
- [x] Установка: закрывает открытый GUI/лаунчер, копирует, служба (автозапуск, перезапуск при
      падении), ярлыки «Пуск» и рабочий стол → `Hikkari.exe`, запись в «Программы и компоненты»
      (версия, размер, `UninstallString`/`QuietUninstallString`). Служба обновляет версию в этой
      записи после каждого обновления/отката.
- [x] Удаление (`HikkariService.exe uninstall`, его и вызывают «Программы и компоненты»):
      подтверждение → служба, процессы, ярлыки, запись, `Program Files\Hikkari` (сама папка — через
      пару секунд после выхода: удаляющий exe лежит в ней). Профили в `%LOCALAPPDATA%\Hikkari`
      остаются.
- [x] Профили portable-версии: переносятся из `--import DIR` (и из папки распакованного комплекта).
      Portable-версии дальше нет — только установщик.
- [x] Иконка приложения у `Hikkari.exe` и `HikkariSetup.exe` (rsrc в CI).
- [x] CI: установка через `HikkariSetup.exe --quiet`, проверка ярлыков и записи, обновление меняет
      версию в записи, удаление через `QuietUninstallString` не оставляет ничего; артефакт
      `HikkariSetup`.
- SmartScreen на первом запуске скачанного установщика будет (нет сертификата): «Подробнее →
  Выполнить в любом случае». Обновления его не вызывают.

### Phase 5 — UI
- [ ] Баннер «Доступно обновление — Перезапустить», прогресс, «что нового».
- [ ] Блокирующий экран для версий ниже `minimum_version`.

### Phase 6 — релиз в CI ✅ (ждёт сервер)
- [x] Тег `v X.Y.Z` → `scripts/set-version.sh`: `pubspec.yaml` (`X.Y.Z+run_number`), `updater/Cargo.toml`
      и `Cargo.lock`; Go-бинари берут её из pubspec через ldflags, `app\VERSION` — тоже.
- [x] Только релизная сборка вшивает канал: `HIKKARI_UPDATE_URL` и `HIKKARI_UPDATE_KEYS` из
      переменных репозитория. Сборки веток остаются без канала (`unavailable`), smoke-тест это проверяет;
      для релиза — наоборот, канал обязан быть. Тест обновления берёт «текущая + 1 patch».
- [x] Релиз падает до сборки, если нет конфигурации или `HIKKARI_RELEASE_KEY_ID` не входит в
      `HIKKARI_UPDATE_KEYS`; после подписи манифест проверяется `nekko-manifest verify` публичным
      ключом, с которым собраны клиенты.
- [x] Подпись — на Windows-раннере (дерево `bundle/Hikkari/app` ровно то, что в установщике), ключ на
      диске только в этом шаге. Выкладка — отдельный job `publish-channel` (environment `release`),
      `scripts/publish-update-channel.sh`: блобы → манифесты → проба, rsync без `--delete`, затем
      проба/манифест/блоб с публичного адреса.
- Настройка (Settings → Secrets and variables → Actions):
  - переменные: `HIKKARI_UPDATE_URL` (`https://update.<домен>`), `HIKKARI_UPDATE_KEYS` (`release-1:<pub>`),
    `HIKKARI_RELEASE_KEY_ID` (`release-1`), `HIKKARI_DEPLOY_HOST` (`deploy@31.57.158.79`);
  - секреты: `HIKKARI_RELEASE_KEY` (hex приватного ключа), `HIKKARI_DEPLOY_SSH_KEY`,
    `HIKKARI_DEPLOY_KNOWN_HOSTS` (`ssh-keyscan host`).
  - Сервер (main, Рига, 31.57.158.79): Caddy `file_server` для `/srv/hikkari/update` на `update.hikkari.net`.
    Ключ CI у пользователя `deploy` прибит к `rrsync /srv/hikkari/update`: только запись файлов канала.
- ⚠️ Ключ подписи отдельный от Nekko. Потеря ключа = все установки перестают принимать обновления,
  пока их не переустановят руками. Ротация: новый ключ сначала добавить вторым в `HIKKARI_UPDATE_KEYS`
  и выпустить с ним релиз, подписанный старым; только потом подписывать новым.
- [ ] Не проверено вживую: нет домена и сервера. Первый тег после настройки — проверка всей цепочки.

### Phase 7 — macOS
Та же модель, что на Windows: привилегированная служба, GUI без прав, пароль — один раз при установке.

```
/Applications/Hikkari.app                    ← root:wheel, ставится .pkg; заменяется обновлением целиком
    Contents/MacOS/nekkobox                  GUI (Flutter)
    Contents/MacOS/HikkariService            служба (root, LaunchDaemon net.hikkari.service)
    Contents/MacOS/hikkari-updater
    Contents/Frameworks/libnekkobox.dylib    движок
    Contents/Resources/sing-box, VERSION
/Applications/Hikkari.app.old                предыдущая, пока новая на испытании
/Applications/.Hikkari.staging/              tree/, parts/, READY/PENDING/REFUSED (тот же том, что .app)
/Library/Application Support/Hikkari/core/   служба: config.json, cache.db, service.log, run
/var/run/hikkari-service.sock                тот же протокол svcproto, что pipe на Windows
~/Library/Application Support/nekkobox/      данные GUI (как раньше)
```

- **Общая служба.** Сервер, цикл обновлений и протокол — один код (`core/service`) для Windows и
  macOS; платформа подставляет только транспорт (pipe / unix-сокет), запуск апдейтера и запись версии
  (реестр на Windows). Санитайзер тот же — от root sing-box так же опасен, как от SYSTEM.
- **sing-box от root запускается только из root-владеемого `.app`.** Служба отказывается работать,
  если бандл не `root:wheel` и доступен на запись кому-то кроме root (как «только из Program Files»).
  Поэтому установка — `.pkg` (встроенный установщик macOS, не сторонний): он кладёт бандл от root.
  Старая схема (LaunchDaemon `com.nekkobox.singbox`, копия sing-box, sentinel-файл, osascript)
  удаляется установщиком.
- **Самообновление службы и апдейтера бесплатно.** На macOS переименовать бандл с запущенными
  бинарями можно; после замены служба выходит, launchd поднимает уже новую, она восстанавливает
  туннель, если он был включён (файл `core/run`).
- **Без лаунчера.** «Перезапустить»: GUI → `apply_update` (служба гасит ядро, меняет бандл, поднимает
  ядро) → GUI запускает отложенный `open` нового бандла и выходит. Новый GUI при старте зовёт
  `recover_update`, через 20 с — `confirm_update`. Сторож в службе: нет `confirm` за 90 с после
  `apply` → откат (покрывает версию, которая не стартует вообще — лаунчер на Windows делает то же).
- **Без Developer ID пока.** `.pkg` не подписан: первый запуск — «Системные настройки →
  Конфиденциальность → Всё равно открыть». Обновления качает служба, карантина у них нет.

- [ ] `core/service`: общий сервер + цикл обновлений; `winsvc` — только установка/SCM на Windows.
- [ ] Транспорт: unix-сокет `/var/run/hikkari-service.sock`; `svcclient`/`ServiceManager` для darwin.
- [ ] `vpn.ProcessManager` на darwin — обычный дочерний процесс службы (вместо launchd-супервизора).
- [ ] `hikkari-service` для darwin: daemon, `install` (из postinstall), `uninstall`, проверка владельца.
- [ ] `hikkari-updater`: раскладка macOS (`/Applications`, `Hikkari.app`, `Contents/Resources/VERSION`).
- [ ] Движок: `RestartToUpdate` / recover / confirm на macOS.
- [ ] CI `build-macos.yml`: `.app` → `.pkg`, smoke на раннере (sudo без пароля): установка, TUN через
      службу, обновление до +1 patch, удаление; релиз по тегу в канал `stable/macos`.

## 4. Открытые вопросы

1. **Домен и сервер канала** (`update.<домен>`) — будут позже; до тех пор адрес задаётся при сборке.
2. ~~Portable-установки~~ — решено: portable больше не выпускаем, установщик переносит профили.
3. **Самообновление лаунчера и службы** (они вне `app\`): план — новые копии едут внутри `app\`,
   после `confirm` служба сверяет хэш, переименовывает свой работающий exe в `.old` (Windows это
   разрешает) и перезапускается через SCM. Меняются редко — делаем после MVP.
