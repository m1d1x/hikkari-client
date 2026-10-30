#!/usr/bin/env python3
"""Check the built macOS FFI routing contract with an isolated preview executor.

Build first: cd app && flutter build macos --debug
No VPN is started/stopped; routing/catalog/native config files must stay absent.
"""
import argparse
import ctypes
import json
import os
from pathlib import Path
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def main():
    if os.environ.get('HIKKARI_ROUTING_CONTRACT_SMOKE') != '1':
        environment = dict(os.environ)
        environment.update(HIKKARI_ROUTING_CONTRACT_SMOKE='1',
                           HIKKARI_PRESET_CATALOG_URL='', HIKKARI_PRESET_EVENTS_URL='')
        os.execve(sys.executable, [sys.executable, str(Path(__file__).resolve()), *sys.argv[1:]], environment)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--library', type=Path, default=ROOT / 'app/build/macos/Build/Products/Debug/nekkobox.app/Contents/Frameworks/libnekkobox.dylib')
    arguments = parser.parse_args()
    library = ctypes.CDLL(str(arguments.library.resolve()))
    library.NekkoboxCall.argtypes = [ctypes.c_char_p, ctypes.c_char_p]
    library.NekkoboxCall.restype = ctypes.c_void_p
    library.NekkoboxFree.argtypes = [ctypes.c_void_p]

    def request(method, arguments=None, *, expect_ok=True):
        pointer = library.NekkoboxCall(method.encode(), json.dumps(arguments or {}).encode())
        try:
            response = json.loads(ctypes.string_at(pointer))
        finally:
            library.NekkoboxFree(pointer)
        assert response['ok'] is expect_ok, (method, response)
        return response.get('data')

    with tempfile.TemporaryDirectory(prefix='hikkari-routing-contract-') as directory:
        request('Init', {'baseDir': directory, 'engineId': 'sing-box', 'routingPreview': True})
        try:
            request('AddRule', {'mode': 'bypass', 'category': 'domains', 'value': 'https://Example.com/path'})
            status = request('RoutingStatus')
            assert status['status'] == 'preview' and status['preview'], status
            assert status['appliedRevision'] == 0, status
            assert status['manual']['bypass']['domains'] == ['example.com'], status
            document = request('RoutingDocument')
            preview = request('PreviewRoutingDocument', {'document': document})
            assert preview['policy']['defaultTarget'] == 'vpn', preview
            request('PreviewRoutingDocument', {'document': {**document, 'schemaVersion': 999}}, expect_ok=False)
            request('FlushRouting')
            request('StartVPN', expect_ok=False)
            request('RestartVPN', expect_ok=False)
            request('StopVPN')
        finally:
            request('Shutdown')
        for name in ('routing.json', 'routing-presets.json', 'config.json'):
            assert not (Path(directory) / name).exists(), f'Preview wrote {name}'
    print('PASS: native FFI commands, canonicalization, preview status, document validation, flush and shutdown; no native routing/config writes.')


if __name__ == '__main__':
    main()
