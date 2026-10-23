#!/usr/bin/env python3
"""Run the five CUDA-derived MGPUSim research workloads, default INV/infinity."""
import argparse
from pathlib import Path
import sys
import run_benchmarks

BENCHMARKS = ['SSSP', 'CC', 'COLOR', 'MIS', 'EMBEDDING']
SUPPORTED = BENCHMARKS + [name+'-2' for name in BENCHMARKS]


def suite_arguments(argv):
    parser = argparse.ArgumentParser(description=__doc__, epilog='Other run_benchmarks flags are forwarded, including --page-bytes, --profile, --policies and --dry-run.')
    parser.add_argument('--bench', default=','.join(BENCHMARKS), help='comma-separated IDs: '+','.join(SUPPORTED))
    parser.add_argument('--root', type=Path, default=run_benchmarks.ROOT)
    parser.add_argument('--timeout', type=int, default=86400, help='host seconds per policy; default 86400 (24 hours)')
    args, remaining = parser.parse_known_args(argv)
    ids = [name.strip().upper() for name in args.bench.split(',')]
    if any(name not in SUPPORTED for name in ids) or len(set(ids)) != len(ids):
        parser.error('--bench requires unique IDs from '+','.join(SUPPORTED))
    return ['run', '--root', str(args.root), '--mode', 'policy', '--protocol', 'kernel-internal',
            '--profile', 'small', '--bench', ','.join(ids),
            '--policies', ','.join(run_benchmarks.DEFAULT_POLICIES),
            '--timeout', str(args.timeout), *remaining]


def main(argv=None):
    return run_benchmarks.main(suite_arguments(sys.argv[1:] if argv is None else argv))


if __name__ == '__main__':
    raise SystemExit(main())
