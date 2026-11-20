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
| `alfa` | alfabank.ru favicon (256px) |
| `aviasales` | aviasales.ru icon.svg |
| `avito` | avito.ru favicon (256px) |
| `beeline` | iOS app icon (Билайн) |
| `bitrix24` | iOS app icon (Битрикс24) |
| `cian` | iOS app icon (CIAN TECHNOLOGIES LIMITED) |
| `citilink` | iOS app icon (CITILINK, LLC) |
| `claude` | claude.ai favicon (256px) |
| `discord` | iOS app icon (Discord) |
| `dns` | iOS app icon (DNS) |
| `dtf` | dtf.ru apple-touch-icon |
| `faceit` | iOS app icon (FACEIT) |
| `figma` | iOS app icon (Figma) |
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
| `mailru` | Викимедиа (P154): Mail.Ru Logo 2018.svg |
| `megafon` | iOS app icon (МегаФон) |
| `megamarket` | megamarket.ru favicon (256px) |
| `mir` | Викимедиа (P154): Mir-logo.SVG.svg |
| `mts` | Викимедиа: Mts-newlogo2023.png (638px) |
| `mvideo` | Викимедиа (P154): MVideo 2023 |
| `notion` | Викимедиа (P154): Notion-logo.svg |
| `ok` | ok.ru apple-touch-icon |
| `okko` | okko.tv favicon (256px) |
| `openai` | openai.com icon |
| `ozon` | ozon.ru favicon (256px) |
| `pikabu` | pikabu.ru apple-touch-icon |
| `pobeda` | flypobeda.ru og:image (512px) |
| `premier` | premier.one/favicon-512x512-v3.png |
| `psb` | psbank.ru favicon (256px) |
| `raiffeisen` | iOS app icon (Райффайзенбанк) |
| `rbc` | Викимедиа (P154): RBK logo.svg |
| `regru` | reg.ru favicon (256px) |
| `ria` | ria.ru favicon (256px) |
| `rostelecom` | iOS app icon (Ростелеком) |
| `rutube` | rutube.ru icon.svg |
| `rzd` | Викимедиа (P154): RZD.svg, только знак «РЖД» |
| `s7` | s7.ru favicon (256px) |
| `samokat` | samokat.ru favicon (256px) |
| `sber` | Викимедиа: Sberbank logo 2020 en.png (331px) |
| `sbis` | sbis.ru apple-touch-icon |
| `selectel` | selectel.ru apple-touch-icon |
| `smotrim` | smotrim.ru apple-touch-icon |
| `sovcombank` | sovcombank.ru favicon (256px) |
| `spotify` | Викимедиа: Spotify_logo_without_text.svg |
| `start` | iOS app icon (START) |
| `steam` | iOS app icon (Steam, Valve) |
| `t2` | iOS app icon (Т2) |
| `tass` | iOS app icon (ТАСС) |
| `tbank` | tbank.ru favicon (256px) |
| `telegram` | iOS app icon (Telegram) |
| `timeweb` | iOS app icon (Timeweb) |
| `tochka` | Викимедиа (P154): Tochka Logo.svg |
| `vc` | vc.ru apple-touch-icon |
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
