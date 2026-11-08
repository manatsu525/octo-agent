"""Offline installer contract: both Linux architectures use fork assets + SHA256."""
import hashlib
import io
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest

SCRIPT = Path(__file__).resolve().parents[1] / 'landing/install.sh'


class LinuxInstaller(unittest.TestCase):
    def test_architectures(self):
        for machine, arch in [('aarch64', 'arm64'), ('arm64', 'arm64'),
                              ('x86_64', 'amd64'), ('amd64', 'amd64')]:
            with self.subTest(machine=machine), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                tools = root / 'bin'
                tools.mkdir()
                archive = f'octo_1.17.21-m4_linux_{arch}.tar.gz'
                payload = b'#!/bin/sh\necho fixture\n'
                with tarfile.open(root / archive, 'w:gz') as tar:
                    info = tarfile.TarInfo('octo')
                    info.size = len(payload)
                    info.mode = 0o755
                    tar.addfile(info, io.BytesIO(payload))
                digest = hashlib.sha256((root / archive).read_bytes()).hexdigest()
                (root / 'checksums.txt').write_text(f'{digest}  {archive}\n')
                (tools / 'uname').write_text(f'#!/bin/sh\ncase "$1" in -s) echo Linux;; -m) echo {machine};; esac\n')
                (tools / 'curl').write_text('''#!/usr/bin/env python3
import os, pathlib, shutil, sys
args = sys.argv[1:]
url = next(x for x in args if x.startswith('https://'))
assert url.startswith('https://github.com/manatsu525/octo-agent/releases/download/v1.17.21-m4/')
shutil.copy(pathlib.Path(os.environ['FIXTURES']) / url.rsplit('/', 1)[1], args[args.index('-o') + 1])
''')
                for tool in tools.iterdir():
                    tool.chmod(0o755)
                dest = root / 'installed'
                dest.mkdir()
                env = dict(os.environ, HOME=tmp, FIXTURES=tmp,
                           PATH=f'{tools}:{dest}:' + os.environ['PATH'],
                           OCTO_VERSION='1.17.21-m4', OCTO_INSTALL_DIR=str(dest))
                env.pop('OCTO_REPO', None)
                result = subprocess.run(['sh', str(SCRIPT)], env=env, capture_output=True, text=True)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual((dest / 'octo').read_bytes(), payload)
                self.assertIn(archive, result.stdout)


if __name__ == '__main__':
    unittest.main()
