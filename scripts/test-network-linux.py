"""Exercise the Linux peer boundary and real NM parser without touching host Wi-Fi."""
import http.client
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time

ROOT = Path(tempfile.mkdtemp(prefix="gateway-network-"))
os.chmod(ROOT, 0o755)
DATA = ROOT / "data"
PROFILES = ROOT / "profiles"
DATA.mkdir(mode=0o700)
PROFILES.mkdir(mode=0o700)
CONFIG = ROOT / "config.json"
SOCKET = ROOT / "run/control.sock"
config = dict(dataDir=str(DATA), profileDir=str(PROFILES), wifiInterface="wlan0",
              printerInterface="eth0", printerCIDR="192.168.77.1/24", apCIDR="192.168.78.1/24",
              apSSID="Stitkovac-GW-test", apPassword="private-service-password", country="CZ",
              apID="11111111-1111-4111-8111-111111111111", agentUID=1000, agentGID=1000,
              gpioChip="", gpioLine=17, simulation=True)
CONFIG.write_text(json.dumps(config))
os.chmod(CONFIG, 0o600)
BINARY = "/src/bin/gateway-network-linux-arm64"


class UnixHTTP(http.client.HTTPConnection):
    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX)
        self.sock.settimeout(5)
        self.sock.connect(str(SOCKET))


def request(method, path, body=None):
    connection = UnixHTTP("network.local", timeout=5)
    connection.request(method, path, json.dumps(body) if body else None)
    response = connection.getresponse()
    data = response.read()
    status = response.status
    connection.close()
    return status, data


def wait_mode(mode):
    for _ in range(100):
        if SOCKET.exists():
            try:
                status, body = request("GET", "/status")
                if status == 200 and json.loads(body)["mode"] == mode:
                    return json.loads(body)
            except (ConnectionRefusedError, FileNotFoundError, ConnectionResetError):
                pass
        time.sleep(0.1)
    raise AssertionError("Helper did not reach expected mode")


def start():
    return subprocess.Popen([BINARY, "--config", str(CONFIG), "--socket", str(SOCKET)],
                            stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)


process = start()
try:
    status = wait_mode("ap")
    assert status["wifiAdminSupported"] and not status["wifiAdminEnabled"]
    assert request("POST", "/wifi-admin", {"enabled": True})[0] == 200
    # The web UID may use the socket, but another UID in the same group may not.
    for uid, expected in ((1000, 200), (1001, 403)):
        child = os.fork()
        if child == 0:
            os.setgroups([])
            os.setgid(1000)
            os.setuid(uid)
            status, _ = request("GET", "/status")
            os._exit(0 if status == expected else 1)
        _, result = os.waitpid(child, 0)
        assert result == 0, "SO_PEERCRED authorization failed"
    trial = dict(ssid="Shop ; \\ 1;2;", password="space \\; password", security="wpa-psk",
                 hidden=True, country="CZ", serverURL="https://cloud.stitkovac.app")
    assert request("POST", "/wifi", trial)[0] == 200
    assert wait_mode("uplink")["ssid"] == trial["ssid"]
    profile = PROFILES / ("gateway-" + json.loads((DATA / "selection.json").read_text())["known"]["id"] + ".nmconnection")
    # Parse and rewrite using actual libnm. Keep secrets entirely inside the container.
    parsed = subprocess.run(["nmcli", "--offline", "connection", "modify", "connection.id", "parsed"],
                            input=profile.read_bytes(), capture_output=True)
    assert parsed.returncode == 0, "NetworkManager rejected generated keyfile"
    normalized = ROOT / "normalized.nmconnection"
    normalized.write_bytes(parsed.stdout)
    os.chmod(normalized, 0o600)
    assert b"mode=infrastructure" in parsed.stdout and b"hidden=true" in parsed.stdout
    # Round-trip through libnm using its introspection API via the system Python.
    import gi
    gi.require_version("NM", "1.0")
    from gi.repository import GLib, NM
    keyfile = GLib.KeyFile.new()
    keyfile.load_from_file(str(profile), GLib.KeyFileFlags.NONE)
    connection = NM.keyfile_read(keyfile, str(PROFILES), NM.KeyfileHandlerFlags.NONE, None, None)
    assert bytes(connection.get_setting_wireless().get_ssid().get_data()).decode() == trial["ssid"]
    assert connection.get_setting_wireless_security().get_psk() == trial["password"]
    ap_file = GLib.KeyFile.new()
    ap_file.load_from_file(str(PROFILES / ("gateway-" + config["apID"] + ".nmconnection")), GLib.KeyFileFlags.NONE)
    ap = NM.keyfile_read(ap_file, str(PROFILES), NM.KeyfileHandlerFlags.NONE, None, None)
    assert ap.get_setting_wireless().get_mode() == "ap"
    assert ap.get_setting_ip4_config().get_method() == "manual"
    assert ap.get_setting_ip4_config().get_never_default()
    assert ap.get_setting_wireless_security().get_psk() == config["apPassword"]
    # A killed process must discard a durable pending trial and retain the accepted SSID.
    trial["ssid"] = "Interrupted"
    assert request("POST", "/wifi", trial)[0] == 200
    process.kill()
    process.wait(timeout=5)
    process = start()
    status = wait_mode("uplink")
    assert status["wifiAdminEnabled"], "Wi-Fi administration preference lost on helper restart"
    assert request("POST", "/wifi-admin", {"enabled": False})[0] == 200
    assert not json.loads(request("GET", "/status")[1])["wifiAdminEnabled"]
    assert status["ssid"] == "Shop ; \\ 1;2;" and status["error"] == "interrupted"
    print("Linux helper: UID boundary, NM keyfile round-trip and process-kill recovery passed.")
finally:
    process.terminate()
    process.wait(timeout=5)

# Render deterministic test configuration from the same Ansible templates.
import jinja2
values = dict(gateway_wifi_interface="wlan0", gateway_printer_interface="eth0",
              gateway_printer_address="192.168.77.1", gateway_ap_address="192.168.78.1",
              gateway_ap_network="192.168.78.0/24", gateway_ap_first="192.168.78.50",
              gateway_ap_last="192.168.78.199")
for name, command in (("gateway-network.nft", ["nft", "--check", "--file"]),
                      ("ap-dhcp.conf", ["dnsmasq", "--test", "--conf-file"])):
    source = Path("/src/deploy/ansible/templates") / (name + ".j2")
    dest = ROOT / name
    dest.write_text(jinja2.Template(source.read_text(), undefined=jinja2.StrictUndefined).render(**values))
    args = command + [str(dest)] if name.endswith(".nft") else command[:-1] + ["--conf-file=" + str(dest)]
    subprocess.run(args, check=True)
rules = str(ROOT / "gateway-network.nft")
subprocess.run(["nft", "--file", rules], check=True)
subprocess.run(["nft", "add", "element", "inet", "stitkovac_gateway", "service_ap_ifaces", "{", '\"wlan0\"', "}"], check=True)
subprocess.run(["nft", "--file", rules], check=True)
result = subprocess.check_output(["nft", "--json", "list", "set", "inet", "stitkovac_gateway", "service_ap_ifaces"])
sets = [item["set"] for item in json.loads(result)["nftables"] if "set" in item]
assert len(sets) == 1 and not sets[0].get("elem"), "Firewall reload left AP access enabled"
print("nftables apply/reload, AP access gating and service AP DHCP syntax passed.")

# Start the actual DHCP-only process with the production service UID/capability
# boundary, using a dummy AP interface inside this container only.
subprocess.run(["ip", "link", "add", "wlan0", "type", "dummy"], check=True)
subprocess.run(["ip", "addr", "add", "192.168.78.1/24", "dev", "wlan0"], check=True)
subprocess.run(["ip", "link", "set", "wlan0", "up"], check=True)
runtime = Path("/run/stitkovac-gateway-ap")
runtime.mkdir(mode=0o755)
os.chown(runtime, 1000, 1000)
capabilities = "+net_admin,+net_raw,+net_bind_service"
# The service configuration is public; its private parent here is a test fixture.
os.chmod(ROOT / "ap-dhcp.conf", 0o644)
dhcp = subprocess.Popen(["setpriv", "--reuid=1000", "--regid=1000", "--clear-groups",
                         "--inh-caps=" + capabilities, "--ambient-caps=" + capabilities,
                         "dnsmasq", "--keep-in-foreground", "--conf-file=" + str(ROOT / "ap-dhcp.conf")],
                        stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
try:
    time.sleep(0.3)
    assert dhcp.poll() is None, "DHCP startup failed: " + dhcp.stderr.read().decode() if dhcp.poll() is not None else ""
    process_status = Path(f"/proc/{dhcp.pid}/status").read_text()
    assert "Uid:\t1000\t1000\t1000\t1000" in process_status
    print("DHCP-only daemon starts as the unprivileged service UID with bounded capabilities.")
finally:
    if dhcp.poll() is None:
        dhcp.terminate()
    dhcp.wait(timeout=5)
