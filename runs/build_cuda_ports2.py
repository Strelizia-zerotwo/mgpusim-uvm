#!/usr/bin/env python3
"""Rebuild the AMD kernels for the version 2 in-place/grouped research benchmarks."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def resource_symbols(assembly):
    """Publish LLVM's resource counts in the ELF symbols MGPUSim reads.

    CDNA3 descriptors do not encode a useful SGPR allocation count. LLVM 18
    puts it in metadata but does not export the optional numbered_sgpr symbols.
    The current simulator consumes those symbols, so keep them with the binary.
    """
    symbols = []
    resources = {}
    for match in re.finditer(r'(?ms)^\s*\.amdhsa_kernel\s+(\w+)\s*$(.*?)^\s*\.end_amdhsa_kernel\s*$', assembly):
        name, descriptor = match.groups()
        sgpr = re.search(r'\.amdhsa_next_free_sgpr\s+(\d+)', descriptor)
        vgpr = re.search(r'\.amdhsa_next_free_vgpr\s+(\d+)', descriptor)
        if not sgpr or not vgpr:
            raise ValueError('missing register resource metadata for '+name)
        resources[name] = dict(numbered_sgpr=int(sgpr[1]), num_vgpr=int(vgpr[1]))
        for suffix, count in resources[name].items():
            symbol = name+'.'+suffix
            symbols.extend(['.globl '+symbol, '.set '+symbol+', '+str(count)])
    if not resources:
        raise ValueError('no AMD kernel descriptors found')
    return '\n'.join(symbols)+'\n', resources


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=ROOT)
    parser.add_argument('--clang', type=Path)
    parser.add_argument('--lld', type=Path)
    args = parser.parse_args(argv)
    tool = args.root / '_toolchains/llvm18'
    clang = args.clang or shutil.which('clang-18') or tool / 'usr/bin/clang-18'
    lld = args.lld or shutil.which('ld.lld-18') or tool / 'usr/bin/ld.lld-18'
    folder = args.root / 'mgpusim/amd/benchmarks/cudaports2'
    source = folder / 'native/kernels.cl'
    env = dict(os.environ)
    libraries = str(tool / 'usr/lib/x86_64-linux-gnu')
    env['LD_LIBRARY_PATH'] = libraries + (':' + env['LD_LIBRARY_PATH'] if env.get('LD_LIBRARY_PATH') else '')
    output = folder / 'kernels_gfx942.hsaco'
    with tempfile.TemporaryDirectory(prefix='cuda-ports-') as tmp:
        obj = Path(tmp) / 'kernels.o'
        assembly = Path(tmp) / 'kernels.s'
        commands = [
            [str(clang), '-target', 'amdgcn-amd-amdhsa', '-mcpu=gfx942',
             '-x', 'cl', '-cl-std=CL2.0', '-nogpulib', '-O2',
             '-mcode-object-version=4', '-ffp-contract=off', '-S', str(source), '-o', str(assembly)],
            [str(clang), '-target', 'amdgcn-amd-amdhsa', '-mcpu=gfx942',
             '-mcode-object-version=4', '-x', 'assembler', '-c', str(assembly), '-o', str(obj)],
            [str(lld), '-shared', '--no-undefined', str(obj), '-o', str(output)],
        ]
        versions = {}
        for name, command in [('clang', clang), ('lld', lld)]:
            versions[name] = subprocess.check_output([str(command), '--version'], env=env, text=True).splitlines()[0]
        subprocess.run(commands[0], env=env, check=True)
        text = assembly.read_text(encoding='utf-8')
        symbols, resources = resource_symbols(text)
        assembly.write_text(text+'\n'+symbols, encoding='utf-8')
        for command in commands[1:]:
            subprocess.run(command, env=env, check=True)
    manifest = dict(target='gfx942', source_sha256=hashlib.sha256(source.read_bytes()).hexdigest(),
                    binary_sha256=hashlib.sha256(output.read_bytes()).hexdigest(), tools=versions,
                    flags=commands[0][1:-4], register_resources=resources,
                    resource_contract='ELF numbered_sgpr/num_vgpr symbols for timing allocation')
    (folder / 'kernel-build.json').write_text(json.dumps(manifest, indent=2) + '\n', encoding='utf-8')
    print(output)
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
