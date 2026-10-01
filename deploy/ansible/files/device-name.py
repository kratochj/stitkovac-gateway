#!/usr/bin/env python3
"""Resolve an appliance identity from a permanent Wi-Fi MAC, never a random one."""
import json
from pathlib import Path
import re
import subprocess


def validate_mac(value):
    if not re.fullmatch(r'(?:[0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}', value):
        raise ValueError('Invalid Wi-Fi MAC address')
    octets = bytes.fromhex(value.replace(':', ''))
    if octets == bytes(6) or octets[0] & 1:
        raise ValueError('Wi-Fi MAC must be a nonzero unicast address')
    return value.lower()


def permanent_mac(interface, sysfs=Path('/sys/class/net')):
    if not re.fullmatch(r'[a-zA-Z0-9_-]{1,15}', interface):
        raise ValueError('Invalid Wi-Fi interface name')
    result = subprocess.run(['ip', '-j', 'link', 'show', 'dev', interface],
                            check=True, capture_output=True, text=True, timeout=10)
    links = json.loads(result.stdout)
    if len(links) != 1 or links[0].get('ifname') != interface:
        raise ValueError('Wi-Fi interface not found')
    # Linux exposes IFLA_PERM_ADDRESS when the current MAC differs from hardware.
    if links[0].get('permaddr'):
        return validate_mac(links[0]['permaddr'])
    # Without an explicit permanent address, only trust a kernel-marked permanent MAC.
    device = sysfs / interface
    before = (device / 'addr_assign_type').read_text().strip()
    address = (device / 'address').read_text().strip()
    after = (device / 'addr_assign_type').read_text().strip()
    if before != '0' or after != '0':
        raise ValueError('Permanent Wi-Fi MAC unavailable; refusing a generated device name')
    return validate_mac(address)


def device_name(mac):
    return 'stitkovac-gw-' + '-'.join(validate_mac(mac).split(':')[-2:])


def main():
    import argparse
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--interface', required=True)
    parser.add_argument('--test-mac', default='', help='Explicit fixture for disposable hardware-free smoke tests only')
    args = parser.parse_args()
    mac = validate_mac(args.test_mac) if args.test_mac else permanent_mac(args.interface)
    print(json.dumps({'mac': mac, 'name': device_name(mac)}))


if __name__ == '__main__':
    main()
