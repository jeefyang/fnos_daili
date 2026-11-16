#!/usr/bin/env python3
"""Build three gateway-probe fpk variants with the official fnpack 1.2.3."""
import json
import shutil
import subprocess
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[3]
BASE = HERE / "base"
WORK = REPO / ".tmp" / "gateway-build"
FNPACK = REPO / ".tmp-research" / "fnpack.exe"

VARIANTS = [
    {
        "name": "gwprobe",
        "prefix": "/app/gwprobe",
        "service": ["service_port = 0", "service_socket = app.sock", "checkport = false"],
        "note": "service_port=0 + undocumented service_socket",
    },
    {
        "name": "gwprobe2",
        "prefix": "/app/gwprobe2",
        "service": [],
        "note": "no service_port / service_socket / checkport",
    },
    {
        "name": "gwprobe3",
        "prefix": "/app/gwprobe3",
        "service": ["service_port = 28998", "checkport = false"],
        "note": "fallback with a real unused TCP port",
    },
]


def build_one(v):
    name = v["name"]
    dst = WORK / name
    if dst.exists():
        shutil.rmtree(dst)
    shutil.copytree(BASE, dst)

    (dst / "app" / "prefix").write_text(v["prefix"] + "\n", encoding="utf-8")

    ui = {
        ".url": {
            name + ".main": {
                "title": "Gateway Probe " + name,
                "icon": "images/icon_{0}.png",
                "type": "iframe",
                "protocol": "",
                "gatewayPrefix": v["prefix"],
                "gatewaySocket": "app.sock",
                "url": v["prefix"],
                "allUsers": False,
                "control": {"accessPerm": "readonly"},
            }
        }
    }
    (dst / "app" / "ui" / "config").write_text(
        json.dumps(ui, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
    )

    manifest = [
        "appname = " + name,
        "version = 1.0.0",
        "display_name = Gateway Probe " + name,
        "desc = Disposable gateway entry probe listening on app.sock only",
        "platform = x86",
        "source = thirdparty",
        "maintainer = fnos_daili",
        "distributor = fnos_daili",
        "os_min_version = 0.9.0",
        "desktop_uidir = ui",
        "desktop_applaunchname = " + name + ".main",
    ] + v["service"]
    (dst / "manifest").write_text("\n".join(manifest) + "\n", encoding="utf-8")

    proc = subprocess.run(
        [str(FNPACK), "build", "-d", str(dst)],
        cwd=str(dst),
        capture_output=True,
        text=True,
    )
    sys.stdout.write(proc.stdout)
    sys.stderr.write(proc.stderr)
    if proc.returncode != 0:
        raise SystemExit("fnpack failed for " + name + " rc=" + str(proc.returncode))
    fpk = dst / (name + ".fpk")
    if not fpk.exists():
        candidates = list(dst.glob("*.fpk"))
        if not candidates:
            raise SystemExit("no fpk produced for " + name)
        fpk = candidates[0]
    out = HERE / (name + ".fpk")
    shutil.copy2(fpk, out)
    print(name, "->", out, out.stat().st_size, "bytes", v["note"])


def main():
    if not FNPACK.exists():
        raise SystemExit("missing fnpack: " + str(FNPACK))
    if WORK.exists():
        shutil.rmtree(WORK)
    WORK.mkdir(parents=True)
    for v in VARIANTS:
        build_one(v)


if __name__ == "__main__":
    main()
