import importlib
import json
from pathlib import Path
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))


class ManagedSitesTest(unittest.TestCase):
    def setUp(self):
        self.root = Path(self.enterContext(tempfile.TemporaryDirectory()))
        if sys.platform != "linux":
            self.enterContext(patch.dict(sys.modules, {"fcntl": SimpleNamespace()}))
        self.manager = importlib.import_module("manage_ikev2_site")
        self.enterContext(patch.object(self.manager, "SAVED", self.root / "saved.json"))
        self.enterContext(patch.object(self.manager, "ACTIVE", self.root / "active"))
        self.enterContext(patch.object(self.manager, "BOOT_ID", self.root / "boot-id"))
        self.manager.BOOT_ID.write_text("before")
        self.enterContext(patch.object(self.manager, "write_atomic", lambda path, data: path.write_bytes(data)))
        self.ipsec = self.manager.ipsec
        self.enterContext(patch.object(self.ipsec, "read_state", return_value={"enabled": False}))
        self.enterContext(patch.object(self.ipsec, "active", return_value=False))
        self.state = {"mappings": [{"id": "map", "status": "running", "table_id": 101,
                                    "tun_name": "tun101", "subnet": "10.0.101.0/24"}]}
        self.request = {"enabled": True, "gateway": "192.0.2.10", "local_subnet": "198.51.100.0/24",
                        "sites": [{"name": "sg01", "gateway": "192.0.2.11", "subnet": "10.21.0.0/24",
                                   "reqid": 101, "mapping_id": "map", "psk": "fixture-secret-abcdefghij"}]}

    def test_saved_key_is_not_returned_and_can_be_preserved(self):
        key = self.request["sites"][0]["psk"]
        self.assertNotIn(key, json.dumps(self.manager.save(self.request, self.state)))
        self.assertIn(key, self.manager.SAVED.read_text())
        self.request["sites"][0]["psk"] = ""
        self.assertTrue(self.manager.save(self.request, self.state)["pending"])
        self.assertIn(key, self.manager.SAVED.read_text())
        self.request["sites"][0]["gateway"] = "192.0.2.12"
        with self.assertRaises(ValueError):
            self.manager.save(self.request, self.state)

    def test_restore_does_not_start_ipsec_when_guards_fail(self):
        self.manager.save(self.request, self.state)
        self.manager.BOOT_ID.write_text("after")
        state_path = self.root / "state.json"
        state_path.write_text(json.dumps(self.state))
        with patch.object(self.ipsec, "ready", return_value=True), patch.object(self.ipsec, "check_conflicts"), \
                patch.object(self.ipsec, "run") as run, \
                patch.object(self.manager.ikev2_site, "attach", side_effect=RuntimeError("guard failed")):
            with self.assertRaises(RuntimeError):
                self.manager.restore(state_path)
            run.assert_not_called()
            self.assertFalse(self.manager.ACTIVE.exists())

    def test_boot_restores_once_and_keeps_saved_edits_pending(self):
        self.manager.save(self.request, self.state)
        state_path = self.root / "state.json"
        state_path.write_text(json.dumps(self.state))
        for attribute in ("CONF", "SECRET", "SYSTEM_SECRET"):
            self.enterContext(patch.object(self.ipsec, attribute, self.root / attribute))

        def start(*args):
            self.assertTrue(self.manager.ACTIVE.exists())
            self.assertIn("keyexchange=ikev2", self.ipsec.CONF.read_text())
            self.assertIn("fixture-secret-abcdefghij", self.ipsec.SECRET.read_text())
            self.assertEqual(args, ("systemctl", "start", self.ipsec.UNIT))

        with patch.object(self.ipsec, "ready", return_value=True), patch.object(self.ipsec, "check_conflicts"), \
                patch.object(self.ipsec, "prepare_apparmor"), patch.object(self.ipsec, "run", side_effect=start), \
                patch.object(self.manager.ikev2_site, "attach") as attach:
            self.manager.restore(state_path)
            attach.assert_not_called()
            self.manager.BOOT_ID.write_text("after")
            self.manager.restore(state_path)
            attach.assert_called_once_with(dict(self.request, saved_boot_id="before"), self.state, allow_offline=True)
            self.assertFalse(self.manager.status()["pending"])
            self.manager.save({"enabled": False}, self.state)
            self.manager.restore(state_path)
            attach.assert_called_once()
            self.assertTrue(self.manager.status()["pending"])

    def test_wan_save_requires_gateway_and_discards_stale_socks_selection(self):
        site = self.request["sites"][0]
        site.update(egress="wan", wan_interface="ens3")
        with patch.object(self.ipsec, "run", return_value=SimpleNamespace(stdout="[]")):
            with self.assertRaises(ValueError):
                self.manager.save(self.request, self.state)
        self.assertFalse(self.manager.SAVED.exists())
        routes = json.dumps([{"dst": "default", "dev": "ens3", "gateway": "192.0.2.1"}])
        with patch.object(self.ipsec, "run", return_value=SimpleNamespace(stdout=routes)):
            saved = self.manager.save(self.request, self.state)["sites"][0]
        self.assertEqual(saved["egress"], "wan")
        self.assertEqual(saved["wan_interface"], "ens3")
        self.assertNotIn("mapping_id", saved)
        self.assertNotIn("psk", saved)

    def test_wan_only_can_be_saved_before_the_first_mapping(self):
        state = self.manager.load_mappings(self.root / "missing-state.json")
        self.request["sites"][0].update(egress="wan", wan_interface="ens3")
        routes = json.dumps([{"dst": "default", "dev": "ens3", "gateway": "192.0.2.1"}])
        with patch.object(self.ipsec, "run", return_value=SimpleNamespace(stdout=routes)):
            self.assertEqual(self.manager.save(self.request, state)["sites"][0]["egress"], "wan")
