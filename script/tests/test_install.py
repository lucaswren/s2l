from pathlib import Path
import sys
import unittest
from unittest.mock import patch
import tempfile
sys.path.insert(0,str(Path(__file__).resolve().parents[1]))
import install_preflight
import install_state

class InstallChecks(unittest.TestCase):
    def test_supported_versions(self):
        for value in ('go version go1.22.12 linux/amd64','go version go1.27.0 linux/arm64'):
            install_preflight.go_version(value)
        for value in ('go version go1.21.13 linux/amd64','garbage','devel go1.21'):
            with self.assertRaises(ValueError):install_preflight.go_version(value)
        install_preflight.singbox_version('sing-box version 1.11.15\nEnvironment: go1.24 linux/amd64')
        for value in ('sing-box version 1.12.0','sing-box version 1.11.15-dev','sing-box version 1.11.15.1'):
            with self.assertRaises(ValueError):install_preflight.singbox_version(value)

    @unittest.skipUnless(sys.platform == 'linux', 'Linux installation journal')
    def test_recovery_refuses_replaced_directory(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory)/'journal';root.mkdir(mode=0o700)
            app=Path(directory)/'app';app.mkdir();(app/'foreign').write_text('keep')
            state=dict(token='old',complete=False,files=[dict(kind='absent') for _ in install_state.FILES],units={name:{} for name in install_state.UNITS},sysctl={key:'0' for key in install_state.KEYS})
            with patch.object(install_state,'ROOT',root),patch.object(install_state,'APP',app),patch.object(install_state,'secure_root'),patch.object(install_state,'run') as run:
                install_state.save(state)
                with self.assertRaisesRegex(RuntimeError,'ownership'):install_state.recover()
                run.assert_not_called()
                self.assertEqual((app/'foreign').read_text(),'keep')

    @unittest.skipUnless(sys.platform == 'linux', 'Linux installation journal')
    def test_journal_is_private(self):
        with tempfile.TemporaryDirectory() as directory:
            with patch.object(install_state,'ROOT',Path(directory)):
                install_state.save(dict(complete=False))
                self.assertEqual((Path(directory)/'journal.json').stat().st_mode&0o777,0o600) if sys.platform!='win32' else None
