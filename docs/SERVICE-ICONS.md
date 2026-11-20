# Источники иконок сервисов

Каждый файл `app/assets/services/<id>.png` — официальный знак бренда, взятый у самого
бренда: иконка приложения из App Store, иконка/манифест его сайта или его же логотип из
Викимедиа (свойство P154 «логотип» на карточке компании). Ничего не дорисовано вручную.

Файл нормализован в 120×120 RGBA. `cover` — источник уже квадратная плитка (иконка
приложения или сайта), она обрезана по центру; `white` — логотип-надпись на прозрачном
фоне вписан целиком на белую плитку; `pad`/`fit` — широкий знак со своим фоном достроен
до квадрата его же цветом.

`ServiceIconBadge` (`app/lib/widgets/service_icon.dart`) берёт сначала этот PNG, затем
встроенный SVG из `brand_icons.dart`, затем favicon сервиса и только в конце — глиф
Material/SimpleIcons. PNG здесь главный: он и есть настоящий логотип.

| id | источник |
| --- | --- |
| `1c` | Викимедиа (P154): 1C logo.svg |
| `2gis` | iOS app icon (2ГИС) |
| `aeroflot` | Викимедиа (P154): Aeroflot logo (ru).svg, без подписи «Российские авиалинии» |
| `ai-other` | две марки строки: Microsoft Copilot + Perplexity (иконки их приложений) |
| `alfa` | alfabank.ru favicon (256px) |
| `antigravity` | antigravity.google/assets/image/antigravity-logo.png |
| `aviasales` | aviasales.ru icon.svg |
| `avito` | avito.ru favicon (256px) |
| `bbc` | iOS app icon (BBC) |
| `beeline` | iOS app icon (Билайн) |
| `bitrix24` | iOS app icon (Битрикс24) |
| `chess` | iOS app icon (Chess.com, LLC) |
| `cian` | iOS app icon (CIAN TECHNOLOGIES LIMITED) |
| `citilink` | iOS app icon (CITILINK, LLC) |
| `claude` | claude.ai favicon (256px) |
| `copilot` | iOS app icon (Microsoft Copilot) |
| `dailymotion` | iOS app icon (Dailymotion) |
| `discord` | iOS app icon (Discord) |
| `dns` | iOS app icon (DNS) |
| `dtf` | dtf.ru apple-touch-icon |
| `dw` | iOS app icon (Deutsche Welle) |
| `euronews` | iOS app icon (Euronews) |
| `faceit` | iOS app icon (FACEIT) |
| `fall-guys` | fallguys.com/apple-touch-icon.png (180px) |
| `figma` | iOS app icon (Figma) |
| `fortnite` | iOS app icon (Fortnite, Epic Games Sweden AB) |
| `gazprombank` | gazprombank.ru favicon (256px) |
| `gemini` | gemini.google.com icon |
| `github` | iOS app icon (GitHub) |
| `google` | iOS app icon (Google, com.google.GoogleMobile) |
| `gosuslugi` | iOS app icon (ЕПГУ, seller: Минкомсвязь России) |
| `habr` | iOS app icon (Хабр) |
| `hh` | iOS app icon (hh.ru) |
| `instagram` | iOS app icon (Instagram) |
| `ivi` | iOS app icon (Иви) |
| `kaspersky` | iOS app icon (Kaspersky) |
| `kinopoisk` | iOS app icon (Кинопоиск) |
| `kion` | kion.ru favicon (256px) |
| `kommersant` | kommersant.ru apple-touch-icon |
| `kontur` | kontur.ru favicon (48px, корпоративный знак «К») |
| `kuper` | kuper.ru favicon (256px) |
| `lamoda` | Викимедиа (P154): Lamoda logo.svg |
| `lenta` | Викимедиа (P154): Lenta.svg |
| `linkedin` | iOS app icon (LinkedIn Corporation) |
| `mailru` | Викимедиа (P154): Mail.Ru Logo 2018.svg |
| `medium` | iOS app icon (A Medium Corporation) |
| `meduza` | iOS app icon (Meduza) |
| `megafon` | iOS app icon (МегаФон) |
| `megamarket` | megamarket.ru favicon (256px) |
| `mir` | Викимедиа (P154): Mir-logo.SVG.svg |
| `mts` | Викимедиа: Mts-newlogo2023.png (638px) |
| `mullvad` | iOS app icon (Mullvad VPN AB) |
| `mvideo` | Викимедиа (P154): MVideo 2023 |
| `netflix` | iOS app icon (Netflix, Inc.) |
| `nordvpn` | iOS app icon (NordVPN) |
| `notion` | Викимедиа (P154): Notion-logo.svg |
| `novayagazeta` | novayagazeta.eu apple-touch-icon (192px) |
| `ok` | ok.ru apple-touch-icon |
| `okko` | okko.tv favicon (256px) |
| `openai` | openai.com icon |
| `ozon` | ozon.ru favicon (256px) |
| `patreon` | iOS app icon (Patreon, Inc.) |
| `perplexity` | iOS app icon (Perplexity AI) |
| `pikabu` | pikabu.ru apple-touch-icon |
| `pobeda` | flypobeda.ru og:image (512px) |
| `premier` | premier.one/favicon-512x512-v3.png |
| `proton` | iOS app icon (Proton Mail, Proton AG) |
| `psb` | psbank.ru favicon (256px) |
| `psiphon` | iOS app icon (Psiphon Inc.) |
| `quora` | iOS app icon (Quora, Inc.) |
| `raiffeisen` | iOS app icon (Райффайзенбанк) |
| `rbc` | Викимедиа (P154): RBK logo.svg |
| `reddit` | iOS app icon (Reddit, Inc.) |
| `regru` | reg.ru favicon (256px) |
| `rferl` | iOS app icon (RFE/RL) |
| `ria` | ria.ru favicon (256px) |
| `roblox` | iOS app icon (Roblox Corporation) |
| `rostelecom` | iOS app icon (Ростелеком) |
| `rumble` | iOS app icon (Rumble Inc.) |
| `rutube` | rutube.ru icon.svg |
| `rzd` | Викимедиа (P154): RZD.svg, только знак «РЖД» |
| `s7` | s7.ru favicon (256px) |
| `samokat` | samokat.ru favicon (256px) |
| `sber` | Викимедиа: Sberbank logo 2020 en.png (331px) |
| `sbis` | sbis.ru apple-touch-icon |
| `selectel` | selectel.ru apple-touch-icon |
| `signal` | iOS app icon (Signal Messenger, LLC) |
| `smotrim` | smotrim.ru apple-touch-icon |
| `snapchat` | iOS app icon (Snap, Inc.) |
| `soundcloud` | iOS app icon (SoundCloud Global Limited) |
| `sovcombank` | sovcombank.ru favicon (256px) |
| `speedtest` | iOS app icon (Speedtest by Ookla) |
| `spotify` | Викимедиа: Spotify_logo_without_text.svg |
| `start` | iOS app icon (START) |
| `steam` | iOS app icon (Steam, Valve) |
| `t2` | iOS app icon (Т2) |
| `tass` | iOS app icon (ТАСС) |
| `tbank` | tbank.ru favicon (256px) |
| `telegram` | iOS app icon (Telegram) |
| `tiktok` | iOS app icon (TikTok Pte. Ltd.) |
| `timeweb` | iOS app icon (Timeweb) |
| `tochka` | Викимедиа (P154): Tochka Logo.svg |
| `tor` | Викимедиа: Tor Browser icon.svg |
| `tvrain` | Викимедиа: TV Rain logo.svg |
| `vc` | vc.ru apple-touch-icon |
| `viber` | iOS app icon (Rakuten Viber) |
| `video-other` | две марки строки: Dailymotion + Rumble (иконки их приложений) |
| `vk` | vk.com apple-touch-icon |
| `vk-play` | Викимедиа: VK Play logo.svg |
| `vk-video` | Викимедиа: VK Video.svg |
| `vtb` | Викимедиа: VTB logo 2018.png (380px) |
| `wildberries` | iOS app icon (Wildberries OOO) |
| `wink` | wink.ru apple-touch-icon |
| `x` | iOS app icon (X) |
| `yandex` | iOS app icon (Яндекс) |
| `yandex-disk` | iOS app icon (Яндекс Диск) |
| `yandex-maps` | iOS app icon (Яндекс Карты) |
| `yandex-market` | market.yandex.ru apple-touch-icon |
| `yandex-music` | iOS app icon (Яндекс Музыка) |
| `yandex-taxi` | taxi.yandex.ru icon (Яндекс Go) |
| `yoomoney` | yoomoney.ru apple-touch-icon |
| `youtube` | iOS app icon (YouTube) |
