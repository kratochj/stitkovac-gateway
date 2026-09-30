#!/usr/bin/env python3
"""Run the image builder in a disposable ARM64 Linux container on macOS/Linux."""
import argparse
from pathlib import Path
import subprocess

REPO = Path(__file__).resolve().parents[2]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--base', type=Path, required=True)
    parser.add_argument('--sha256', required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--release', type=Path, required=True)
    args = parser.parse_args()
    base, target, release = args.base.absolute(), args.output.absolute(), args.release.absolute()
    if base.is_symlink() or not base.is_file() or target.exists() or target.is_symlink():
        raise ValueError('Require a regular base image and a new output path')
    if not target.parent.is_dir() or not release.is_dir():
        raise ValueError('Output parent and signed release directory must exist')
    subprocess.run(['docker', 'build', '-t', 'stitkovac-image-builder:local', str(REPO / 'scripts/image')], check=True)
    cmd = ['docker', 'run', '--rm', '--privileged']
    for source, destination, readonly in [
        (REPO / 'scripts/image', '/source/scripts/image', True),
        (REPO / 'deploy', '/source/deploy', True), (REPO / 'bin', '/source/bin', True),
        (release, '/release', True), (base.parent, '/base', True), (target.parent, '/build', False),
    ]:
        cmd += ['--mount', f'type=bind,src={source},dst={destination}' + (',readonly' if readonly else '')]
    cmd += ['stitkovac-image-builder:local', '--base', '/base/' + base.name, '--sha256', args.sha256,
            '--output', '/build/' + target.name, '--release', '/release']
    subprocess.run(cmd, check=True)


if __name__ == '__main__':
    main()
