// nekkobox-ffi is the cgo wrapper that exposes core/api as a C shared
// library (nekkobox.dll / libnekkobox.so / libnekkobox.dylib). Flutter on
// the desktop side loads it via dart:ffi and calls NekkoboxCall().
//
// Build:
//   go build -buildmode=c-shared -o nekkobox.dll ./cmd/nekkobox-ffi
//
// The exported surface is intentionally tiny — three functions:
//
//   char* NekkoboxCall(const char* method, const char* args_json);
//   void  NekkoboxFree(char* result);
//   void  NekkoboxShutdown();
//
// The single dispatch entry mirrors core/api.Core.Call(). Result strings are
// allocated with C.CString on the Go side; the caller MUST hand each one
// back via NekkoboxFree, otherwise it leaks for the lifetime of the process.
package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"sync"
	"unsafe"

	"NekkoVPN/core/api"
)

// One Core per process. dart:ffi has no notion of multiple instances and we
// don't need them — there is exactly one running engine per app.
var (
	coreMu   sync.Mutex
	coreInst = api.New()
)

//export NekkoboxCall
func NekkoboxCall(cMethod, cArgs *C.char) *C.char {
	method := C.GoString(cMethod)
	args := ""
	if cArgs != nil {
		args = C.GoString(cArgs)
	}
	result := coreInst.Call(method, args)
	return C.CString(result)
}

//export NekkoboxFree
func NekkoboxFree(p *C.char) {
	if p != nil {
		C.free(unsafe.Pointer(p))
	}
}

// NekkoboxShutdown is exposed separately for clean teardown when the host
// app is closing. Equivalent to Call("Shutdown", "") but without allocating
// a result string.
//
//export NekkoboxShutdown
func NekkoboxShutdown() {
	coreMu.Lock()
	defer coreMu.Unlock()
	_ = coreInst.Shutdown()
}

// main is required for buildmode=c-shared but never runs — the library has
// no entry point of its own; it just sits there waiting for FFI calls.
func main() {}
