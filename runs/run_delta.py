#!/usr/bin/env python3
"""Compare INV, delta, delta-sub and infinity on the five -2 workloads and NW."""
import argparse
from pathlib import Path
import sys

import run_benchmarks

BENCHMARKS = ['SSSP-2', 'CC-2', 'COLOR-2', 'MIS-2', 'EMBEDDING-2', 'NW']
POLICIES = ['inv', 'delta', 'delta-sub', 'infinity']


def suite_arguments(argv):
    parser = argparse.ArgumentParser(
        description=__doc__,
        epilog='Other run_benchmarks flags are forwarded, including --page-bytes, '
               '--fault-ns, --link-ns, --bandwidth-gbs, --repeats and --dry-run.')
    parser.add_argument('--bench', default=','.join(BENCHMARKS),
                        help='comma-separated IDs: '+','.join(BENCHMARKS))
    parser.add_argument('--policies', default=','.join(POLICIES),
                        help='default inv,delta,delta-sub,infinity; may select a subset')
    parser.add_argument('--root', type=Path, default=run_benchmarks.ROOT)
    parser.add_argument('--profile', choices=['small', 'smoke'], default='small')
    parser.add_argument('--input-config', type=Path,
                        help='override the matched small inputs in delta-start.json')
    parser.add_argument('--timeout', type=int, default=86400,
                        help='host seconds per policy simulation; default 86400')
    args, remaining = parser.parse_known_args(argv)
    names = [name.strip().upper() for name in args.bench.split(',')]
    policies = [name.strip() for name in args.policies.split(',')]
    if any(name not in BENCHMARKS for name in names) or len(set(names)) != len(names):
        parser.error('--bench requires unique IDs from '+','.join(BENCHMARKS))
    if any(name not in POLICIES for name in policies) or len(set(policies)) != len(policies):
        parser.error('--policies requires unique IDs from '+','.join(POLICIES))
    if args.timeout <= 0:
        parser.error('--timeout must be positive')
    command = ['run', '--root', str(args.root), '--mode', 'policy',
               '--protocol', 'kernel-internal', '--profile', args.profile,
               '--bench', ','.join(names), '--policies', ','.join(policies),
               '--timeout', str(args.timeout)]
    config = args.input_config
    if config is None and args.profile == 'small':
        config = args.root/'benchmark/mybenchmark/presets/delta-start.json'
    if config is not None:
        command += ['--input-config', str(config)]
    return command + remaining


def main(argv=None):
    return run_benchmarks.main(suite_arguments(sys.argv[1:] if argv is None else argv))


if __name__ == '__main__':
    raise SystemExit(main())
