#!/usr/bin/env python3
"""Separate identical L2TP tuples after IPsec authentication, before xl2tpd.

Invoked by strongSwan updown. Requires forced NAT-T and unique XFRM marks.
Never changes PPP routes, SOCKS mappings or any host-wide conntrack state.
"""
import fcntl
from collections import Counter
import ipaddress
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import tempfile
import time

from manage_config import write_atomic

ROOT = Path('/run/s2l-ipsec-nat')
FIRST, LAST = 20000, 65535
CONTROL = Path('/run/xl2tpd/l2tp-control')
QUARANTINE = 30
QUIET = 2
RECLAIM_BATCH = 16


def control_status(command):
    """Read a complete xl2tpd response, without its CLI's 4 KiB truncation.

    A regular file avoids blocking xl2tpd on a dead FIFO reader. Keep its fd:
    xl2tpd closes and unlinks the response only AFTER writing every row.
    """
    with tempfile.NamedTemporaryFile(dir=ROOT, prefix='query-', delete=False) as reply:
        path = Path(reply.name)
        try:
            fd = os.open(CONTROL, os.O_WRONLY | os.O_NONBLOCK | os.O_NOFOLLOW)
            try:
                meta = os.fstat(fd)
                if not stat.S_ISFIFO(meta.st_mode) or meta.st_uid != 0:
                    raise RuntimeError('invalid xl2tpd control pipe')
                message = f'@{path} {command}\n'.encode()
                if len(message) > 512 or os.write(fd, message) != len(message):
                    raise RuntimeError('xl2tpd query write failed')
            finally:
                os.close(fd)
            deadline = time.monotonic() + 3
            while time.monotonic() < deadline:
                meta = os.fstat(reply.fileno())
                if meta.st_size > 8 * 1024 * 1024:
                    raise RuntimeError('xl2tpd response too large')
                if meta.st_nlink == 0:
                    reply.seek(0)
                    return reply.read().decode('ascii')
                time.sleep(.02)
            raise RuntimeError('xl2tpd query timed out')
        finally:
            path.unlink(missing_ok=True)


def status_fields(text, kind):
    lines = text.splitlines()
    if not lines or lines[0] != '00 OK':
        raise RuntimeError('xl2tpd status unavailable')
    fields = {}
    for line in lines[1:]:
        match = re.fullmatch(r'00 ' + kind + r' ([a-z0-9_.]+)=([^\r\n]*)', line)
        if not match or match[1] in fields:
            raise RuntimeError('incomplete xl2tpd status')
        fields[match[1]] = match[2]
    return fields


def available_status(text):
    """Include unassigned/half-open tunnels, which status-lns omits."""
    profile_lines, tunnels, totals = [], {}, {}
    for line in text.splitlines():
        if line == '00 OK' or re.fullmatch(r'00 AVAILABLE (lns|lac)\.[a-z0-9_.]+=.*', line):
            profile_lines.append(line)
            continue
        match = re.fullmatch(r'00 AVAILABLE tunnel (0x[0-9a-f]+), id (-?[0-9]+), ourtid ([0-9]+) has ([0-9]+) calls and self (?:0x[0-9a-f]+|\(nil\))', line)
        if match:
            key = (number(match[3], 65535), match[1])
            if key in tunnels:
                raise RuntimeError('duplicate xl2tpd tunnel')
            tunnels[key] = (int(match[2]), int(match[4]))
            continue
        match = re.fullmatch(r'00 AVAILABLE (tunnels|calls) count=([0-9]+)', line)
        if not match or match[1] in totals:
            raise RuntimeError('incomplete xl2tpd global status')
        totals[match[1]] = int(match[2])
    # xl2tpd 1.3.18 does not maintain tunnels.calls (it stays zero even
    # with live PPP calls). Use the per-tunnel counts and LNS lists below.
    if set(totals) != {'tunnels', 'calls'} or totals['tunnels'] != len(tunnels):
        raise RuntimeError('incomplete xl2tpd global tunnel list')
    return status_fields('\n'.join(profile_lines), 'AVAILABLE'), tunnels


def l2tp_peers():
    available, global_tunnels = available_status(control_status('x'))
    # S2L manages an LNS, not outgoing LAC sessions. Unknown configurations
    # must not be treated as an empty server.
    lacs = int(available['lac.count'])
    # Upstream creates an unnamed default LAC template even on an LNS-only
    # server. It is not an outgoing session; any real LAC tunnel would also
    # fail the global-versus-assigned census below.
    if lacs not in (0, 1) or (lacs == 1 and available.get('lac.0.name') != ''):
        raise RuntimeError('unexpected xl2tpd LAC configuration')
    count = int(available['lns.count'])
    if not 0 < count <= 256 or len(available) != count + 2 + lacs:
        raise RuntimeError('incomplete xl2tpd profile list')
    peers, assigned = set(), []
    for i in range(count):
        name = available[f'lns.{i}.name']
        if not re.fullmatch(r'[A-Za-z0-9_.-]{1,79}', name):
            raise RuntimeError('unsupported xl2tpd profile name')
        fields = status_fields(control_status('y ' + name), 'STATUS')
        tunnels = int(fields['tunnels.count'])
        if not 0 <= tunnels <= 65535:
            raise RuntimeError('invalid xl2tpd tunnel count')
        expected = {'tunnels.count'}
        for j in range(tunnels):
            prefix = f'tunnels.{j}.'
            host, port = fields[prefix + 'peer'].rsplit(':', 1)
            peers.add((str(ipaddress.IPv4Address(host)), number(port, 65535)))
            tunnel_id = number(fields[prefix + 'id'], 65535)
            calls = int(fields[prefix + 'calls.count'])
            if not 0 <= calls <= 65535:
                raise RuntimeError('invalid xl2tpd call count')
            assigned.append((tunnel_id, calls))
            expected.update(prefix + s for s in ('id', 'peer', 'calls.count'))
            for k in range(calls):
                for field in ('id', 'tx_bytes', 'rx_bytes', 'tx_pkts', 'rx_pkts'):
                    key = f'{prefix}calls.{k}.{field}'
                    int(fields[key])
                    expected.add(key)
        if set(fields) != expected:
            raise RuntimeError('incomplete xl2tpd tunnel list')
    # Half-open tunnels can lack an LNS and a connected UDP socket. Reclaim
    # nothing unless EVERY global tunnel is accounted for by the LNS snapshots,
    # and the global identities/profile list stayed stable across those queries.
    if (Counter(assigned) != Counter(global_tunnels.values())
            or available_status(control_status('x')) != (available, global_tunnels)):
        raise RuntimeError('xl2tpd tunnel snapshot changed or contains unassigned tunnels')
    return peers


def sa_identity(block):
    if re.search(r'(?m)^\s+dir (?:in|out|fwd)\b', block):
        return None  # A policy template with an SPI is still not an SA.
    address = re.search(r'^src (\S+) dst (\S+)', block)
    spi = re.search(r'\bproto esp spi (0x[0-9a-f]+)\b', block)
    if not address or not spi:
        return None
    return '|'.join((str(ipaddress.ip_address(address[1])),
                     str(ipaddress.ip_address(address[2])), str(int(spi[1], 16))))


def capture_unmarked(a):
    # ipsec.conf/stroke marks inbound POLICIES but not inbound SAs. At the
    # final down hook, remember the remaining unmarked SA identities before
    # allowing a later generation to reuse this endpoint. Rekey has finished;
    # future SAs cannot belong to the retiring CHILD_SA. Include any overlap
    # conservatively, and never persist authentication/encryption keys.
    identities = []
    for block in re.split(r'(?m)(?=^src )', run('ip', 'xfrm', 'state').stdout):
        if (not re.search(r'\breqid\s+' + str(a['reqid']) + r'\b', block)
                or re.search(r'(?m)^\s+mark ', block)):
            continue
        identity = sa_identity(block)
        if identity is None:
            raise RuntimeError('unrecognized unmarked IPsec SA')
        identities.append(identity)
    return identities


def xfrm_in_use(a, text):
    # strongSwan can reuse a reqid on a later IKE association. Its unique
    # inbound/outbound marks distinguish the old generation from the new one.
    marks = [int(a[k].split('/')[0]) for k in ('incoming', 'outgoing')]
    for block in re.split(r'(?m)(?=^src )', text):
        if not re.search(r'\breqid\s+' + str(a['reqid']) + r'\b', block):
            continue
        found = re.search(r'(?m)^\s+mark (0x[0-9a-f]+)(?:/(0x[0-9a-f]+))?\s*$', block)
        if not found:
            identity = sa_identity(block)
            if (re.search(r'(?m)^\s+mark ', block) or identity is None
                    or 'unmarked_spis' not in a or identity in a['unmarked_spis']):
                return True  # Unknown policy/format or a retiring inbound SA.
            continue  # An unmarked SA installed after the final down snapshot.
        value = int(found[1], 16)
        mask = int(found[2], 16) if found[2] else 0xffffffff
        if any(m & mask == value & mask for m in marks):
            return True
    return False


def unused_endpoint(a, peers, sockets, xfrm):
    def busy(reason):
        a['blocked_by'] = reason
        return False
    if (a['peer'], a['slot']) in peers:
        return busy('xl2tpd tunnel')
    for row in sockets.splitlines():
        fields = row.split()
        if len(fields) < 5 or not fields[1].isdigit() or not fields[2].isdigit():
            raise RuntimeError('unrecognized UDP socket state')
        # The listener can contain decrypted SCCRQ packets not yet processed
        # by xl2tpd. A nonempty queue postpones reclamation of every candidate.
        if fields[4] == f"{a['peer']}:{a['slot']}":
            return busy('UDP socket')
        if int(fields[1]) or int(fields[2]):
            return busy('L2TP packet queue')
    # Never print XFRM dumps: they contain key material.
    if xfrm_in_use(a, xfrm):
        return busy('XFRM generation')
    result = run('conntrack', '-L', '-w', str(a['slot']))
    if result.stdout.strip():
        return busy('conntrack zone')
    a.pop('blocked_by', None)
    return True


def reclaim(state):
    now = time.monotonic()
    retired = state['retired']
    eligible = [a for a in retired.values() if now - a['retired_at'] >= QUARANTINE]
    if not eligible:
        return
    # Bound subprocess work per hook, and rotate past blocked endpoints.
    # Previously observed quiet candidates get their second observation first,
    # including the in-hook retry when the high-water counter is exhausted.
    cursor = state.get('reclaim_cursor', FIRST - 1)
    eligible.sort(key=lambda a: (not ('quiet_since' in a and now - a['quiet_since'] >= QUIET),
                                  a['slot'] <= cursor, a['slot']))
    candidates = eligible[:RECLAIM_BATCH]
    state['reclaim_cursor'] = candidates[-1]['slot']
    try:
        sockets = run('ss', '-H', '-n', '-u', '-a', 'sport = :1701').stdout
        xfrm = run('ip', 'xfrm', 'state').stdout + run('ip', 'xfrm', 'policy').stdout
        peers = l2tp_peers()
        state.pop('reclaim_error', None)
        for a in candidates:
            if not unused_endpoint(a, peers, sockets, xfrm):
                a.pop('quiet_since', None)
                continue
            if now - a.setdefault('quiet_since', now) < QUIET:
                continue
            # Recheck the receive queues AFTER the xl2tpd round trip. The
            # admission rule has remained closed throughout quarantine.
            if unused_endpoint(a, peers, run('ss', '-H', '-n', '-u', '-a', 'sport = :1701').stdout, xfrm):
                del retired[str(a['slot'])]
                state['free'].append(a['slot'])
            else:
                a.pop('quiet_since', None)
    except (KeyError, OSError, ValueError, RuntimeError, subprocess.SubprocessError) as error:
        # Failure to inspect is not proof of absence. Fresh slots can still
        # serve new connections; a full live/quarantine pool fails closed.
        for a in candidates:
            a.pop('quiet_since', None)
        state['reclaim_error'] = (str(error) if isinstance(error, RuntimeError)
                                  else 'endpoint inspection failed: ' + type(error).__name__)
    save(state)


def retire(state, key, closed=True):
    a = state['active'][key]
    # Keep the active journal until every removal and targeted conntrack
    # deletion succeeds, including when a preceding invocation crashed.
    edit_rules(a, False)
    if closed:
        a['unmarked_spis'] = capture_unmarked(a)
    else:
        # A failed up hook does not close charon's SA. Until the real down
        # notification arrives, it can still rekey to an unobserved SPI.
        a.pop('unmarked_spis', None)
    a['retired_at'] = time.monotonic()
    state['retired'][str(a['slot'])] = a
    del state['active'][key]
    save(state)


def run(*args, check=True):
    result = subprocess.run(args, capture_output=True, text=True, timeout=15,
                            env={**os.environ, 'LC_ALL': 'C'})
    if check and result.returncode:
        # Do not log environment, PSKs, XFRM keys or command diagnostics.
        raise RuntimeError(f'IPsec NAT isolation: {args[0]} failed ({result.returncode})')
    return result


def number(value, maximum):
    n = int(value)
    if not 0 < n <= maximum:
        raise ValueError('invalid IPsec NAT identifier')
    return n


def mark(value):
    parts = value.split('/')
    n = int(parts[0], 0)
    if not 0 < n <= 0xffffffff or len(parts) != 2 or int(parts[1], 0) != 0xffffffff:
        raise ValueError('IPsec NAT requires a unique full-width mark')
    return f'{n}/0xffffffff'


def identity(env):
    return f"{number(env['PLUTO_REQID'], 0xffffffff)}:{mark(env['PLUTO_MARK_IN'])}"


def association(env, slot):
    return dict(me=str(ipaddress.IPv4Address(env['PLUTO_ME'])),
                peer=str(ipaddress.IPv4Address(env['PLUTO_PEER'])),
                reqid=number(env['PLUTO_REQID'], 0xffffffff),
                incoming=mark(env['PLUTO_MARK_IN']), outgoing=mark(env['PLUTO_MARK_OUT']),
                encap=number(env['PLUTO_UDP_ENC'], 65535), slot=slot)


def rules(a):
    peer, me, port = a['peer'], a['me'], str(a['slot'])
    incoming = ['-s', peer, '-d', me, '-p', 'udp', '--dport', '1701']
    outgoing = ['-s', me, '-d', peer, '-p', 'udp', '--sport', '1701', '--dport', port]
    # Install the outer mark LAST: no packet can enter a partly built path.
    # Remove it FIRST. The independent OUTPUT guard rejects unencrypted replies.
    return [
        ('raw', 'OUTPUT', outgoing + ['-j', 'CT', '--zone', port]),
        ('mangle', 'OUTPUT', outgoing + ['-j', 'MARK', '--set-xmark', a['outgoing']]),
        ('raw', 'PREROUTING', incoming + ['-m', 'mark', '--mark', a['incoming'], '-j', 'CT', '--zone', port]),
        ('nat', 'INPUT', incoming + ['-m', 'policy', '--dir', 'in', '--pol', 'ipsec', '--reqid', str(a['reqid']), '-j', 'SNAT', '--to-source', peer + ':' + port]),
        # Before this admission exists, INPUT drops decrypted early arrivals.
        # They must not create a confirmed conntrack entry without input SNAT.
        ('filter', 'S2L-IPSEC-PEERS', incoming + ['-m', 'policy', '--dir', 'in', '--pol', 'ipsec', '--reqid', str(a['reqid']), '-m', 'mark', '--mark', a['incoming'], '-j', 'ACCEPT']),
        ('mangle', 'PREROUTING', ['-s', peer, '-d', me, '-p', 'udp', '--sport', str(a['encap']), '--dport', '4500', '-j', 'MARK', '--set-xmark', a['incoming']]),
    ]


def edit_rules(a, install):
    ordered = rules(a)
    if not install:
        ordered.reverse()
    for table, chain, rule in ordered:
        # Tag every rule; teardown cannot remove an administrator's similar rule.
        rule = ['-m', 'comment', '--comment', 's2l-ipsec-nat', *rule]
        prefix = ('iptables', '-w', '5', '-t', table)
        checked = run(*prefix, '-C', chain, *rule, check=False)
        if checked.returncode not in (0, 1):
            raise RuntimeError('IPsec NAT rule inspection failed')
        if (checked.returncode == 1 and checked.stderr.strip()
                and 'Bad rule (does a matching rule exist in that chain?)' not in checked.stderr):
            raise RuntimeError('IPsec NAT rule inspection failed')
        exists = checked.returncode == 0
        if install and not exists:
            run(*prefix, '-I', chain, '1', *rule)
        elif not install and exists:
            run(*prefix, '-D', chain, *rule)
    if not install:
        result = run('conntrack', '-D', '-w', str(a['slot']), '-p', 'udp',
                     '-s', a['peer'], '-d', a['me'], '--dport', '1701', check=False)
        # conntrack also returns 1 for permission errors; never mistake those
        # for an empty zone and recycle potentially live connection state.
        empty = result.returncode == 1 and ': 0 flow entries have been deleted.' in result.stderr
        if result.returncode != 0 and not empty:
            raise RuntimeError('IPsec NAT connection cleanup failed')


def save(state):
    write_atomic(ROOT / 'state.json', (json.dumps(state) + '\n').encode())


def update(state, env, cleanup=False):
    # Old journals have no retired identities. Keep that legacy prefix
    # reserved until a full-stack cleanup (the updater already stops both).
    state.setdefault('retired', {})
    state.setdefault('free', [])
    active = state['active']
    if cleanup:
        for key in list(active):
            retire(state, key)
        # Reclaim retired endpoints only when BOTH protocol stacks are empty.
        # A normal IPsec-only restart leaves xl2tpd sockets alive and must not
        # recycle them. Inspect output privately; XFRM state can contain keys.
        if (not run('ss', '-H', '-u', '-a', 'sport = :1701').stdout.strip()
                and not run('ip', 'xfrm', 'state').stdout.strip()
                and not run('ip', 'xfrm', 'policy').stdout.strip()):
            state['next'] = FIRST
            state['retired'].clear()
            state['free'].clear()
            state.pop('reclaim_error', None)
            state.pop('reclaim_cursor', None)
            save(state)
        return
    if env.get('PLUTO_VERB') == 'gc':
        reclaim(state)
        return
    verb = env.get('PLUTO_VERB')
    if verb not in ('up-host', 'down-host'):
        return
    key = identity(env)
    if verb == 'down-host':
        if key in active:
            retire(state, key)
        for a in state['retired'].values():
            if key == f"{a['reqid']}:{a['incoming']}" and 'unmarked_spis' not in a:
                a['unmarked_spis'] = capture_unmarked(a)
                a['retired_at'] = time.monotonic()
                a.pop('quiet_since', None)
                save(state)
        return
    if key in active:
        # Duplicate up notifications repair missing rules, without allocating twice.
        edit_rules(active[key], True)
        return
    # Validate all untrusted updown values before allocating or reclaiming.
    association(env, FIRST)
    reclaim(state)
    if not state['free'] and state['next'] > LAST and any(
            'quiet_since' in a for a in state['retired'].values()):
        # At the high-water limit there may be no next updown notification:
        # charon can retain an established SA even if its hook returns failure.
        # Finish the short second observation here, so a safe first reconnect
        # succeeds without requiring an operator or another IKE reconnect.
        time.sleep(QUIET)
        reclaim(state)
    slot = min(state['free']) if state['free'] else state['next']
    if not FIRST <= slot <= LAST:
        raise RuntimeError('IPsec NAT endpoint pool busy; retry after retired endpoints become safe to reclaim')
    a = association(env, slot)
    if state['free']:
        state['free'].remove(slot)
    else:
        state['next'] = slot + 1
    active[key] = a
    save(state)  # Journal before mutation, so service restart can clean a crash.
    try:
        edit_rules(a, True)
    except Exception:
        retire(state, key, closed=False)
        raise


def main():
    os.umask(0o077)
    ROOT.mkdir(mode=0o700, parents=True, exist_ok=True)
    with (ROOT / 'lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        path = ROOT / 'state.json'
        state = json.loads(path.read_text()) if path.exists() else {'next': FIRST, 'active': {}}
        args = sys.argv[1:]
        if args not in ([], ['cleanup'], ['gc']):
            raise ValueError('usage: ipsec_nat.py [cleanup|gc]')
        update(state, {'PLUTO_VERB': 'gc'} if args == ['gc'] else os.environ,
               cleanup=args == ['cleanup'])


if __name__ == '__main__':
    try:
        main()
    except (KeyError, OSError, ValueError, RuntimeError, subprocess.SubprocessError) as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
