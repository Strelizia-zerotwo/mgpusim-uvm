#!/usr/bin/env python3
"""Run SCC,PPR,SYMGS,PTA,LP,LOUVAIN,DMR on the kernel-internal ideal protocol."""
import argparse
from pathlib import Path
import sys
import run_benchmarks

BENCHMARKS = ['SCC', 'PPR', 'SYMGS', 'PTA', 'LP', 'LOUVAIN', 'DMR']
POLICIES = ['inv', 'infinity', 'delta', 'delta-sub']


def suite_arguments(argv):
    parser = argparse.ArgumentParser(description=__doc__, epilog='Other run_benchmarks options are forwarded, including --page-bytes, --input-config, --profile and --dry-run.')
    parser.add_argument('--bench', default=','.join(BENCHMARKS))
    parser.add_argument('--policies', default=','.join(POLICIES))
    parser.add_argument('--root', type=Path, default=run_benchmarks.ROOT)
    parser.add_argument('--timeout', type=int, default=86400, help='host seconds per policy')
    args, remaining = parser.parse_known_args(argv)
    ids = [v.strip().upper() for v in args.bench.split(',')]
    policies = [v.strip().lower() for v in args.policies.split(',')]
    for values, choices, option in [(ids, BENCHMARKS, '--bench'), (policies, POLICIES, '--policies')]:
        if len(set(values)) != len(values) or any(v not in choices for v in values):
            parser.error(option+' requires unique values from '+','.join(choices))
    if args.timeout <= 0:
        parser.error('--timeout must be positive')
    return ['run', '--root', str(args.root), '--mode', 'policy', '--protocol', 'kernel-internal',
            '--profile', 'small', '--bench', ','.join(ids), '--policies', ','.join(policies),
            '--timeout', str(args.timeout), *remaining]


def main(argv=None):
    return run_benchmarks.main(suite_arguments(sys.argv[1:] if argv is None else argv))


if __name__ == '__main__':
    raise SystemExit(main())
