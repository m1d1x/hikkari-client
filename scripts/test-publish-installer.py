#!/usr/bin/env python3
"""Exercise the publisher with a real HTTP server and local rsync transport."""
import functools
import http.server
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import threading
import unittest

SCRIPT = Path(__file__).with_name('publish-installer.sh').resolve()


class PublishTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.web = self.root / 'web'
        (self.web / 'stable/windows').mkdir(parents=True)
        (self.web / 'download').mkdir()
        self.channel('1.0.61')
        self.failure = None
        self.corrupt = False
        owner = self

        class Handler(http.server.SimpleHTTPRequestHandler):
            def do_GET(self):
                if owner.failure and self.path.endswith('latest-windows.json'):
                    self.send_error(owner.failure)
                elif owner.corrupt and self.path.endswith('.exe'):
                    self.send_response(200)
                    self.end_headers()
                    self.wfile.write(b'corrupt')
                else:
                    super().do_GET()

            def log_message(self, *_):
                pass

        self.http = http.server.ThreadingHTTPServer(('127.0.0.1', 0), functools.partial(Handler, directory=str(self.web)))
        self.thread = threading.Thread(target=self.http.serve_forever, daemon=True)
        self.thread.start()
        self.addCleanup(self.http.server_close)
        self.addCleanup(self.http.shutdown)
        self.bin = self.root / 'bin'
        self.bin.mkdir()
        wrapper = self.bin / 'rsync'
        real_rsync = shutil.which('rsync')
        wrapper.write_text(f'''#!{shutil.which('python3')}
import os,subprocess,sys
args = sys.argv[1:]
if '-e' in args:
    i = args.index('-e')
    del args[i:i+2]
args = [a.removeprefix('local:') for a in args]
# Reproduce same-second, same-length metadata replacements deterministically.
for a in args:
    if a.endswith('.json') and os.path.isfile(a):
        os.utime(a, (1700000000, 1700000000))
env = dict(os.environ, PATH=os.environ['PATH'].split(os.pathsep, 1)[1])
sys.exit(subprocess.call([{real_rsync!r}, *args], env=env))
''')
        wrapper.chmod(0o755)
        self.installer = self.root / 'tested.exe'
        self.installer.write_bytes(b'tested release installer')
        self.env = dict(os.environ, PATH=str(self.bin)+os.pathsep+os.environ['PATH'],
                        HIKKARI_UPDATE_URL=f'http://127.0.0.1:{self.http.server_port}',
                        HIKKARI_DEPLOY_HOST='local', HIKKARI_DEPLOY_ROOT=str(self.web))

    def channel(self, version):
        (self.web/'stable/windows/latest.json').write_text(json.dumps(dict(version=version)))

    def publish(self, version='1.0.61'):
        return subprocess.run(['bash', str(SCRIPT), str(self.installer), version, 'windows'], env=self.env, capture_output=True, text=True)

    def test_first_release_and_upgrade_replace_stable_url(self):
        result = self.publish()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.channel('1.0.62')
        self.installer.write_bytes(b'next tested installer')
        result = self.publish('1.0.62')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual((self.web/'download/hikkari-setup.exe').read_bytes(), self.installer.read_bytes())
        self.assertTrue((self.web/'download/hikkari-setup-1.0.61.exe').exists())
        meta = json.loads((self.web/'download/latest-windows.json').read_text())
        self.assertEqual(meta['version'], '1.0.62')
        self.assertEqual(meta['size'], self.installer.stat().st_size)

    def test_macos_uses_pkg_download_names(self):
        (self.web/'stable/macos').mkdir()
        (self.web/'stable/macos/latest.json').write_text('{"version":"1.0.61"}')
        result = subprocess.run(['bash', str(SCRIPT), str(self.installer), '1.0.61', 'macos'], env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual((self.web/'download/hikkari.pkg').read_bytes(), self.installer.read_bytes())
        self.assertTrue((self.web/'download/hikkari-1.0.61.pkg').exists())

    def test_channel_mismatch_does_not_publish(self):
        self.channel('1.0.60')
        self.assertNotEqual(self.publish().returncode, 0)
        self.assertEqual(list((self.web/'download').iterdir()), [])

    def test_late_retry_cannot_replace_newer_download(self):
        (self.web/'download/latest-windows.json').write_text('{"version":"1.0.62"}')
        self.assertNotEqual(self.publish().returncode, 0)
        self.assertFalse((self.web/'download/hikkari-setup.exe').exists())

    def test_metadata_outage_does_not_publish(self):
        self.failure = 401  # A non-retryable HTTP error; must not be treated as first release.
        self.assertNotEqual(self.publish().returncode, 0)
        self.assertEqual(list((self.web/'download').iterdir()), [])

    def test_corrupt_public_file_never_becomes_latest(self):
        self.corrupt = True
        result = self.publish()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('public installer differs', result.stderr)
        self.assertFalse((self.web/'download/hikkari-setup.exe').exists())
        self.assertFalse((self.web/'download/latest-windows.json').exists())


if __name__ == '__main__':
    unittest.main()
