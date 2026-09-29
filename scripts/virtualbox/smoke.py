#!/usr/bin/env python3
"""Exercise authenticated lab UI -> WSS -> gateway journal -> TCP -> PDF capture."""
import base64
import hashlib
import json
from pathlib import Path
import re
import ssl
import time
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
OUT = ROOT / 'dist/virtualbox'
BASE = 'https://127.0.0.1:9443'
password = json.loads((OUT / 'credentials.json').read_text())['gateway_admin_password']
authorization = 'Basic ' + base64.b64encode(('technik:' + password).encode()).decode()
context = ssl.create_default_context(cafile=str(OUT / 'lab-cert.pem'))

def request(path='/', data=None):
    headers = {'Authorization': authorization}
    if data is not None:
        headers['Origin'] = BASE
        headers['Content-Type'] = 'application/x-www-form-urlencoded'
    req = urllib.request.Request(BASE + path, data=data, headers=headers)
    with urllib.request.urlopen(req, context=context, timeout=5) as response:
        return response.read()


def files(body):
    return set(re.findall(r'/captures/([0-9A-Za-z.-]+)', body))


def main():
    deadline = time.monotonic() + 30
    while True:
        body = request().decode()
        if 'připojena přes WSS' in body and '192.168.77.50:9100' in body:
            break
        if time.monotonic() > deadline:
            raise SystemExit('Gateway WSS connection or DHCP lease did not become ready')
        time.sleep(1)
    results = []
    for kind in ('label', 'receipt'):
        before = files(body)
        sent_before = body.count('<td>SENT</td>')
        csrf = re.search(r'name="csrf" value="([a-f0-9]+)"', body).group(1)
        request('/print', urllib.parse.urlencode({'kind': kind, 'csrf': csrf}).encode())
        deadline = time.monotonic() + 15
        while True:
            body = request().decode()
            added = files(body) - before
            if added and body.count('<td>SENT</td>') > sent_before:
                break
            if time.monotonic() > deadline:
                raise SystemExit('Print did not reach SENT with a captured document')
            time.sleep(.2)
        name = sorted(added)[-1]
        document = request('/captures/' + name)
        assert document.startswith(b'%PDF-1.4') and document.endswith(b'%%EOF\n')
        expected = b'TESTOVACI STITEK' if kind == 'label' else b'TESTOVACI UCTENKA'
        assert expected in document
        (OUT / name).write_bytes(document)
        results.append({'kind': kind, 'capture': name, 'bytes': len(document), 'sha256': hashlib.sha256(document).hexdigest(), 'state': 'SENT'})
    (OUT / 'smoke-results.json').write_text(json.dumps(results, indent=2)+'\n')
    print(json.dumps(results, indent=2))

if __name__ == '__main__':
    main()
