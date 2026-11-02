package api

import "errors"

// errServiceUpdating: the service is older than this GUI and its scan is not
// safe to run; the update replaces it within about a minute.
var errServiceUpdating = errors.New("служба ещё обновляется, повторите проверку через минуту")

var errUnsupportedNetworkReset = errors.New("сброс сетевого стека не поддерживается на этой системе")
