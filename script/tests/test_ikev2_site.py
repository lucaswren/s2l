import ipaddress
from pathlib import Path
import sys
from types import SimpleNamespace
import unittest
from unittest.mock import mock_open, patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from ikev2_site import attach, bindings, firewall_payload, render_profiles, wan_nat_payload, wan_next_hop


class SiteProfilesTest(unittest.TestCase):
    def config(self):
        return {
            "gateway": "192.0.2.10",
            "local_subnet": "10.20.0.0/24",
            "sites": [
                {"name": "sg01", "gateway": "192.0.2.11", "subnet": "10.21.0.0/24", "reqid": 101},
                {"name": "us01", "gateway": "192.0.2.12", "subnet": "10.22.0.0/24", "reqid": 102},
            ],
        }

    def test_two_peers_get_mirrored_selectors_and_distinct_reqids(self):
        profiles = render_profiles(self.config())
        server = profiles["responder"]
        self.assertEqual(server.count("keyexchange=ikev2"), 2)
        self.assertEqual(server.count("type=tunnel"), 2)
        self.assertIn("rightid=192.0.2.11", server)
        self.assertIn("rightsubnet=10.21.0.0/24", server)
        self.assertIn("reqid=101", server)
        us = profiles["initiators"]["us01"]
        self.assertIn("leftid=192.0.2.12", us)
        self.assertIn("leftsubnet=10.22.0.0/24", us)
        self.assertIn("rightsubnet=10.20.0.0/24", us)
        self.assertIn("reqid=102", us)
        self.assertNotIn("sg01", us)

    def test_rejects_ambiguous_selectors_and_profile_injection(self):
        for change in (
            {"subnet": "10.21.0.128/25"},
            {"subnet": "10.20.0.0/25"},
            {"subnet": "0.0.0.0/0"},
            {"subnet": "192.0.2.0/24"},
            {"reqid": 101},
            {"name": "sg01"},
            {"gateway": "192.0.2.11"},
            {"name": "us01\n    auto=start"},
        ):
            with self.subTest(change=change):
                config = self.config()
                config["sites"][1].update(change)
                with self.assertRaises(ValueError):
                    render_profiles(config)

    def test_rejects_ipv6(self):
        config = self.config()
        config["sites"][0]["subnet"] = "2001:db8::/64"
        with self.assertRaises(ipaddress.AddressValueError):
            render_profiles(config)

    def bound_config(self):
        config = self.config()
        state = {"mappings": []}
        for index, site in enumerate(config["sites"], 101):
            site["mapping_id"] = f"map-{index}"
            state["mappings"].append({
                "id": site["mapping_id"], "status": "running", "table_id": index,
                "tun_name": f"tun{index}", "subnet": f"10.0.{index}.0/24",
            })
        return config, state

    def test_two_sites_are_bound_to_distinct_tuns_and_ipsec_policies(self):
        config, state = self.bound_config()
        sites = bindings(config, state)
        closed = firewall_payload(sites, False)
        active = firewall_payload(sites, True)
        self.assertNotIn("ACCEPT", closed)
        self.assertEqual(active.count("-j ACCEPT"), 4)
        for subnet, _, tun, reqid in sites:
            self.assertIn(f"-s {subnet} -o {tun} -m policy --dir in --pol ipsec --strict --mode tunnel --reqid {reqid} -j ACCEPT", active)
            self.assertIn(f"-d {subnet} -i {tun} -m policy --dir out --pol ipsec --strict --mode tunnel --reqid {reqid}", active)
            self.assertIn(f"-A S2L-SITE-OUTPUT -d {subnet} -j DROP", active)
        config["sites"][1]["mapping_id"] = config["sites"][0]["mapping_id"]
        with self.assertRaises(ValueError):
            bindings(config, state)

    def test_installs_terminal_routes_before_opening_guard(self):
        config, state = self.bound_config()
        commands = []

        def run(args, **kwargs):
            commands.append((args, kwargs.get("input", "")))
            if args[0] == "ip" and "route" in args:
                return SimpleNamespace(stdout=f'[{{"dst":"default","dev":"tun{args[-1]}"}}]')
            return SimpleNamespace(stdout="[]" if args[0] == "ip" else "")

        fcntl = SimpleNamespace(flock=lambda *_: None, LOCK_EX=1, LOCK_NB=2)
        with patch.dict(sys.modules, {"fcntl": fcntl}), patch("builtins.open", mock_open()), patch("subprocess.run", side_effect=run):
            attach(config, state)

        guard, sg_stop, sg_route, us_stop, us_route, admit = commands[-6:]
        self.assertNotIn("-j ACCEPT", guard[1])
        for stop, lookup, subnet, table in (
            (sg_stop, sg_route, "10.21.0.0/24", "101"),
            (us_stop, us_route, "10.22.0.0/24", "102"),
        ):
            self.assertEqual(stop[0][-3:], ("from", subnet, "unreachable"))
            self.assertEqual(lookup[0][-4:], ("from", subnet, "lookup", table))
        self.assertEqual(sum("S2L-SITE-FORWARD" in line and "-j ACCEPT" in line for line in admit[1].splitlines()), 4)
        self.assertIn("-s 192.0.2.11 -d 192.0.2.10 -p udp -m multiport --dports 500,4500 -j ACCEPT", admit[1])

    def test_restore_keeps_binding_when_a_mapping_is_offline(self):
        config, state = self.bound_config()
        state["mappings"][0]["status"] = "error"
        with self.assertRaises(ValueError):
            bindings(config, state)
        self.assertEqual([row[1] for row in bindings(config, state, allow_offline=True)], [101, 102])

    def test_wan_policy_and_nat_are_scoped_to_explicit_site(self):
        config, state = self.bound_config()
        config["sites"][1].update(egress="wan", wan_interface="ens3")
        sites = bindings(config, state)
        self.assertEqual(sites[1], ("10.22.0.0/24", 20102, "ens3", 102))
        rules = firewall_payload(sites, True)
        self.assertIn("-s 10.22.0.0/24 -o ens3 -m policy", rules)
        self.assertNotIn("-s 10.21.0.0/24 -o ens3", rules)
        nat = wan_nat_payload(sites)
        self.assertIn("-s 10.22.0.0/24 -o ens3 -j MASQUERADE", nat)
        self.assertNotIn("10.21.0.0/24", nat)
        config["sites"][1]["wan_interface"] = "tun101"
        with self.assertRaises(ValueError):
            bindings(config, state)

    def test_wan_uses_one_gateway_on_requested_interface(self):
        defaults = [{"dev": "ens3", "gateway": "192.0.2.1"},
                    {"dev": "ens4", "gateway": "192.0.2.2"}]
        self.assertEqual(wan_next_hop(defaults, "ens3"), "192.0.2.1")
        self.assertIsNone(wan_next_hop(defaults, "ens5"))
        self.assertIsNone(wan_next_hop(defaults + [defaults[0]], "ens3"))

    def test_offline_wan_does_not_block_other_site_or_get_default_fallback(self):
        config, state = self.bound_config()
        config["sites"][1].update(egress="wan", wan_interface="ens3")
        commands = []

        def run(args, **kwargs):
            commands.append((args, kwargs.get("input", "")))
            return SimpleNamespace(stdout="[]" if args[0] == "ip" else "")

        fcntl = SimpleNamespace(flock=lambda *_: None, LOCK_EX=1, LOCK_NB=2)
        with patch.dict(sys.modules, {"fcntl": fcntl}), patch("builtins.open", mock_open()), patch("subprocess.run", side_effect=run):
            attach(config, state, allow_offline=True)

        rules = [args for args, _ in commands if args[:4] == ("ip", "-4", "rule", "add")]
        self.assertEqual([args[-1] for args in rules], ["unreachable", "101", "unreachable", "20102"])
        self.assertFalse(any(args[:4] == ("ip", "-4", "route", "add") for args, _ in commands))
        self.assertIn("-s 10.21.0.0/24 -o tun101", commands[-1][1])
