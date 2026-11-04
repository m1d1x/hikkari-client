# Встроенные базы netinfo

`asn.csv.gz` и `area.csv.gz` встраиваются в клиент через `go:embed` (`core/netinfo/netinfo.go`). По ним клиент
локально определяет сеть и область пользователя; адрес никуда не отправляется.

- Источник: [IP Geolocation by DB-IP](https://db-ip.com), наборы IP to ASN Lite и IP to City Lite
  (https://db-ip.com/db/lite.php).
- Версия: 2026-11.
- Лицензия: [Creative Commons Attribution 4.0 International](https://creativecommons.org/licenses/by/4.0/).
- Что изменено: оставлены только диапазоны РФ, данные приведены к формату `core/netinfo`.

Как пересобрать: `go run ./core/netinfo/gen` из `engine`, параметры описаны в начале `core/netinfo/gen/main.go`.
