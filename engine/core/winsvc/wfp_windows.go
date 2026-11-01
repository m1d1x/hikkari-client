//go:build windows

package winsvc

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"

	"NekkoVPN/core/netdoctor"
)

var (
	fwpuclnt                           = windows.NewLazySystemDLL("fwpuclnt.dll")
	procFwpmEngineOpen0                = fwpuclnt.NewProc("FwpmEngineOpen0")
	procFwpmEngineClose0               = fwpuclnt.NewProc("FwpmEngineClose0")
	procFwpmProviderCreateEnumHandle0  = fwpuclnt.NewProc("FwpmProviderCreateEnumHandle0")
	procFwpmProviderDestroyEnumHandle0 = fwpuclnt.NewProc("FwpmProviderDestroyEnumHandle0")
	procFwpmProviderEnum0              = fwpuclnt.NewProc("FwpmProviderEnum0")
	procFwpmFreeMemory0                = fwpuclnt.NewProc("FwpmFreeMemory0")
)

const (
	rpcCAuthnDefault = 0xffffffff // RPC_C_AUTHN_DEFAULT
	wfpEnumBatch     = 64
)

// fwpmProvider0 mirrors FWPM_PROVIDER0 from fwpmtypes.h.
type fwpmProvider0 struct {
	providerKey  windows.GUID
	name         *uint16 // displayData.name
	description  *uint16 // displayData.description
	flags        uint32
	providerData struct { // FWP_BYTE_BLOB
		size uint32
		data *byte
	}
	serviceName *uint16
}

// wfpProviders lists the registered WFP providers. It only reads: nothing is
// added to, changed in or removed from the filtering engine.
func wfpProviders() ([]netdoctor.WFPProvider, error) {
	if err := fwpuclnt.Load(); err != nil {
		return nil, fmt.Errorf("loading the WFP library: %w", err)
	}
	var engine uintptr
	if r, _, _ := procFwpmEngineOpen0.Call(0, rpcCAuthnDefault, 0, 0, uintptr(unsafe.Pointer(&engine))); r != 0 {
		return nil, fmt.Errorf("FwpmEngineOpen0: 0x%08x", uint32(r))
	}
	defer procFwpmEngineClose0.Call(engine)

	var enum uintptr
	if r, _, _ := procFwpmProviderCreateEnumHandle0.Call(engine, 0, uintptr(unsafe.Pointer(&enum))); r != 0 {
		return nil, fmt.Errorf("FwpmProviderCreateEnumHandle0: 0x%08x", uint32(r))
	}
	defer procFwpmProviderDestroyEnumHandle0.Call(engine, enum)

	var out []netdoctor.WFPProvider
	for {
		batch, more, err := wfpProviderBatch(engine, enum)
		if err != nil {
			return nil, err
		}
		out = append(out, batch...)
		if !more {
			return out, nil
		}
	}
}

// wfpProviderBatch copies one page out of the memory the engine allocated.
func wfpProviderBatch(engine, enum uintptr) (batch []netdoctor.WFPProvider, more bool, err error) {
	var entries **fwpmProvider0
	var count uint32
	r, _, _ := procFwpmProviderEnum0.Call(engine, enum, wfpEnumBatch,
		uintptr(unsafe.Pointer(&entries)), uintptr(unsafe.Pointer(&count)))
	if r != 0 {
		return nil, false, fmt.Errorf("FwpmProviderEnum0: 0x%08x", uint32(r))
	}
	if entries == nil {
		return nil, false, nil
	}
	defer procFwpmFreeMemory0.Call(uintptr(unsafe.Pointer(&entries)))
	for _, p := range unsafe.Slice(entries, count) {
		batch = append(batch, netdoctor.WFPProvider{
			Key:         p.providerKey.String(),
			Name:        windows.UTF16PtrToString(p.name),
			ServiceName: windows.UTF16PtrToString(p.serviceName),
		})
	}
	return batch, count == wfpEnumBatch, nil
}
