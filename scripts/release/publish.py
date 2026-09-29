#!/usr/bin/env python3
"""Publish signed public artifacts with a credential scoped to release publication."""
import argparse
import os
import pathlib
import re
import sys
import urllib.error
import urllib.parse
import urllib.request
import uuid


def publish(origin, token, manifest, artifact):
    url = urllib.parse.urlsplit(origin)
    if (url.scheme != "https" or not url.hostname or url.username or url.password
            or url.path or url.query or url.fragment or not re.fullmatch(r"[A-Za-z0-9.-]+", url.hostname)):
        raise ValueError("Use an HTTPS origin without credentials, path or query")
    if not 32 <= len(token) <= 256 or any(c.isspace() for c in token):
        raise ValueError("Invalid scoped publisher credential")
    if not 0 < manifest.stat().st_size <= 16384 or not 0 < artifact.stat().st_size <= 32 * 1024 * 1024:
        raise ValueError("Invalid artifact size")
    boundary = uuid.uuid4().hex
    body = bytearray()
    for field, path, name in (("manifest", manifest, "manifest.json"), ("artifact", artifact, "gateway")):
        body.extend((f'--{boundary}\r\nContent-Disposition: form-data; name="{field}"; filename="{name}"'
                     '\r\nContent-Type: application/octet-stream\r\n\r\n').encode())
        body.extend(path.read_bytes())
        body.extend(b"\r\n")
    body.extend(f"--{boundary}--\r\n".encode())
    request = urllib.request.Request(origin + "/api/gateway-release-publisher/releases", data=body, headers={
        "Authorization": "Bearer " + token,
        "Content-Type": "multipart/form-data; boundary=" + boundary,
        "User-Agent": "Stitkovac-Gateway-Release/1",
    }, method="POST")

    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, req, fp, code, msg, headers, newurl):
            return None

    opener = urllib.request.build_opener(NoRedirect)
    with opener.open(request, timeout=120) as response:
        if response.status != 201:
            raise ValueError("Publication was not confirmed")
    print("Signed release published; no gateway rollout was started")


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("manifest", type=pathlib.Path)
    parser.add_argument("artifact", type=pathlib.Path)
    args = parser.parse_args()
    try:
        publish(os.environ.get("GATEWAY_RELEASE_ORIGIN", ""), os.environ.get("GATEWAY_PUBLISHER_TOKEN", ""), args.manifest, args.artifact)
    except urllib.error.HTTPError as error:
        print(f"Release publication rejected (HTTP {error.code})", file=sys.stderr)
        sys.exit(1)
    except (ValueError, OSError, urllib.error.URLError):
        print("Release publication failed; verify inputs, server configuration and connectivity", file=sys.stderr)
        sys.exit(1)
