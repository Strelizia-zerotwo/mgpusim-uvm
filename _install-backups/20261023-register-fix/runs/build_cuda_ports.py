#!/usr/bin/env python3
"""Rebuild the AMD kernels for the CUDA-derived research benchmarks."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=ROOT)
    parser.add_argument('--clang', type=Path)
    parser.add_argument('--lld', type=Path)
    args = parser.parse_args(argv)
    tool = args.root / '_toolchains/llvm18'
    clang = args.clang or shutil.which('clang-18') or tool / 'usr/bin/clang-18'
    lld = args.lld or shutil.which('ld.lld-18') or tool / 'usr/bin/ld.lld-18'
    folder = args.root / 'mgpusim/amd/benchmarks/cudaports'
    source = folder / 'native/kernels.cl'
    env = dict(os.environ)
    libraries = str(tool / 'usr/lib/x86_64-linux-gnu')
    env['LD_LIBRARY_PATH'] = libraries + (':' + env['LD_LIBRARY_PATH'] if env.get('LD_LIBRARY_PATH') else '')
    output = folder / 'kernels_gfx942.hsaco'
    with tempfile.TemporaryDirectory(prefix='cuda-ports-') as tmp:
        obj = Path(tmp) / 'kernels.o'
        commands = [
            [str(clang), '-target', 'amdgcn-amd-amdhsa', '-mcpu=gfx942',
             '-x', 'cl', '-cl-std=CL2.0', '-nogpulib', '-O2',
             '-mcode-object-version=4', '-ffp-contract=off', '-c', str(source), '-o', str(obj)],
            [str(lld), '-shared', '--no-undefined', str(obj), '-o', str(output)],
        ]
        versions = {}
        for name, command in [('clang', clang), ('lld', lld)]:
            versions[name] = subprocess.check_output([str(command), '--version'], env=env, text=True).splitlines()[0]
        for command in commands:
            subprocess.run(command, env=env, check=True)
    manifest = dict(target='gfx942', source_sha256=hashlib.sha256(source.read_bytes()).hexdigest(),
                    binary_sha256=hashlib.sha256(output.read_bytes()).hexdigest(), tools=versions,
                    flags=commands[0][1:-4])
    (folder / 'kernel-build.json').write_text(json.dumps(manifest, indent=2) + '\n', encoding='utf-8')
    print(output)
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
