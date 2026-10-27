#!/usr/bin/env python3
"""Run the 15-app large-input set in three disjoint terminal groups; resume matched INV/delta pairs."""
import argparse
from functools import lru_cache
import json
import math
from pathlib import Path
import shlex
import sys

import run_benchmarks as runner
import summarize_results as summary

GROUPS = {
    '1': ['BFS', 'SSSP-2', 'PR', 'MM', 'COLOR-2'],
    '2': ['CC-2', 'KM', 'ST', 'IM2COL', 'MIS-2'],
    # Known-slow large backward convolution goes last, so it cannot delay the others.
    '3': ['NW', 'lud', 'pathfinder', 'EMBEDDING-2', 'C2D'],
}
BENCHMARKS = [name for group in GROUPS.values() for name in group]
POLICIES = ['inv', 'delta']


def read(path, default=None):
    return summary.read(path, default)


def flag_key(flags):
    """Compare explicit input flags independent of order; reject ambiguous duplicates."""
    if not isinstance(flags, list) or not flags:
        return None
    values = {}
    for flag in flags:
        if not isinstance(flag, str) or not flag.startswith('-'):
            return None
        name, sep, value = flag.partition('=')
        if name in values:
            return None
        values[name] = value if sep else True
    return tuple(sorted(values.items()))


def matching_record(directory, benchmark, flags, bandwidth, args):
    """Require final numerical/protocol success and the actual saved config."""
    result = read(directory/'result.json', {})
    process = read(directory/'run.log.process.json', {})
    if (result.get('status') != 'OK' or process.get('status') != 'OK'
            or process.get('returncode') != 0
            or result.get('protocol') != summary.PROTOCOL
            or result.get('policy') not in POLICIES
            or result.get('benchmark') != benchmark['id']
            or 'PASSED' not in str(result.get('verification', ''))
            or result.get('gpu_activity_ok') is not True
            or result.get('active_gpus') != [1, 2, 3, 4]
            or flag_key(result.get('input_flags')) != flag_key(flags)
            or result.get('arch') != benchmark['arch']
            or result.get('gpu') != benchmark['gpu']
            or not isinstance(result.get('application_us'), (int, float))
            or not math.isfinite(result['application_us']) or result['application_us'] <= 0):
        return None
    # Reject other bandwidths before opening potentially large stats files.
    if result.get('bandwidth_gbs') != bandwidth or result.get('page_bytes') != args.page_bytes:
        return None
    header = summary.stats_header(directory/'stats.json')
    cfg = header.get('config', {})
    expected = dict(page_bytes=args.page_bytes, sector_bytes=32, transfer_gran_bytes=32,
                    fault_ns=args.fault_ns, link_ns=args.link_ns, bandwidth_gbs=bandwidth,
                    replica_pages_per_gpu=0, metadata_ns=0, shootdown_ns=0)
    if any(cfg.get(k) != value for k, value in expected.items()):
        return None
    if (cfg.get('policy') != result['policy'] or header.get('adapter') != 'core-'+summary.PROTOCOL
            or header.get('passive', {}).get('protocol') != summary.PROTOCOL
            or header.get('mapping_faults') != 0
            or header.get('passive', {}).get('stale_read_sectors') != 0
            or not header.get('initial_input_sha256') or not header.get('homes_sha256')):
        return None
    return dict(policy=result['policy'], suite=directory.parents[1].name, directory=str(directory),
                input_sha256=result.get('input_sha256'), initial=header['initial_input_sha256'],
                homes=header['homes_sha256'], config={k:v for k,v in cfg.items() if k!='policy'},
                application_us=result['application_us'])


@lru_cache(maxsize=None)
def build_identity(root, suite, sample):
    folder = Path(root)/'results/benchmark/_suites'/suite
    binary = read(folder/'bin'/(sample+'.sha256.json'), {}).get('sha256')
    fp = read(folder/'source-sha256.json', {})
    relevant = {k:v for k,v in fp.items() if '/timing/replica/' in k
                and k.endswith('.go') and not k.endswith('_test.go')}
    return binary, relevant


def matched_pair(inv, delta, root, sample):
    if any(not inv.get(k) or inv[k] != delta.get(k) for k in ['input_sha256', 'initial', 'homes', 'config']):
        return False
    if inv['suite'] == delta['suite']:
        return True
    left, right = build_identity(str(root), inv['suite'], sample), build_identity(str(root), delta['suite'], sample)
    return bool(left[0] and left[1] and left == right)


def find_completed_pair(benchmark, flags, bandwidth, args):
    if args.rerun:
        return None
    records = []
    base = args.root/'results/benchmark'/benchmark['id']
    for path in sorted(base.glob('*/policy/*/result.json')):
        if not path.parent.name.startswith(('inv_', 'delta_')):
            continue
        value = matching_record(path.parent, benchmark, flags, bandwidth, args)
        if value:
            records.append(value)
    for inv in reversed([r for r in records if r['policy']=='inv']):
        for delta in reversed([r for r in records if r['policy']=='delta']):
            if matched_pair(inv, delta, args.root, benchmark['sample']):
                return dict(inv=inv, delta=delta)
    return None


def arguments(argv):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=runner.ROOT)
    parser.add_argument('--group', choices=['1', '2', '3', 'all'], default='all')
    parser.add_argument('--bench', help='optional subset of the 15 IDs; independent of --group')
    parser.add_argument('--bandwidths', default='32,64', help='comma-separated GB/s, default 32,64')
    parser.add_argument('--input-config', type=Path, help='default presets/main15-large.json')
    parser.add_argument('--page-bytes', type=int, choices=[4096,65536,2097152], default=4096)
    parser.add_argument('--fault-ns', type=int, default=900)
    parser.add_argument('--link-ns', type=int, default=200)
    parser.add_argument('--timeout', type=int, default=86400, help='host seconds per policy')
    parser.add_argument('--build-timeout', type=int, default=600)
    parser.add_argument('--rerun', action='store_true', help='explicitly rerun complete pairs too')
    parser.add_argument('--dry-run', action='store_true', help='audit and print commands; never build/run')
    args = parser.parse_args(argv)
    args.root = args.root.resolve()
    args.input_config = (args.input_config or args.root/'benchmark/mybenchmark/presets/main15-large.json').resolve()
    try:
        args.bandwidths = [float(value) for value in args.bandwidths.split(',')]
    except ValueError:
        parser.error('--bandwidths requires numbers')
    if (not args.bandwidths or any(not math.isfinite(v) or v <= 0 for v in args.bandwidths)
            or len(set(args.bandwidths)) != len(args.bandwidths)
            or args.timeout <= 0 or args.build_timeout <= 0 or args.fault_ns < 0 or args.link_ns < 0):
        parser.error('positive unique bandwidths/timeouts and nonnegative fault/link are required')
    lookup = {name.upper():name for name in BENCHMARKS}
    if args.bench:
        requested = [v.strip().upper() for v in args.bench.split(',')]
        if any(v not in lookup for v in requested) or len(set(requested)) != len(requested):
            parser.error('--bench requires unique IDs from '+','.join(BENCHMARKS))
        args.selected = [lookup[v] for v in requested]
    else:
        args.selected = BENCHMARKS if args.group == 'all' else GROUPS[args.group]
    preset = read(args.input_config, {})
    catalog = read(args.root/'benchmark/mybenchmark/catalog.json', {}).get('benchmarks', [])
    try:
        runner.apply_input_config(catalog, 'small', preset)
    except (ValueError, KeyError, TypeError) as exc:
        parser.error('invalid input preset: '+str(exc))
    by_id = {b['id']:b for b in catalog}
    if (set(args.selected)-set(preset.get('benchmarks', {}))
            or any(name not in by_id or not by_id[name].get('core_replica') for name in args.selected)):
        parser.error('every selected benchmark needs an explicit preset and kernel-internal adapter')
    return args, by_id, preset


def make_plan(args, catalog):
    cases = []
    for bandwidth in args.bandwidths:
        for name in args.selected:
            benchmark = catalog[name]
            flags = benchmark['profiles']['small']
            complete = find_completed_pair(benchmark, flags, bandwidth, args)
            cases.append(dict(benchmark=name, bandwidth_gbs=bandwidth, input_flags=flags,
                              action='SKIP_MATCHED_PAIR' if complete else 'RUN_INV_AND_DELTA',
                              completed=complete))
    return cases


def run_arguments(args, bandwidth, names):
    return ['run', '--root', str(args.root), '--mode', 'policy', '--protocol', 'kernel-internal',
            '--profile', 'small', '--input-config', str(args.input_config), '--bench', ','.join(names),
            '--policies', 'inv,delta', '--bandwidth-gbs', str(bandwidth),
            '--page-bytes', str(args.page_bytes), '--fault-ns', str(args.fault_ns),
            '--link-ns', str(args.link_ns), '--replica-pages-per-gpu', '0',
            '--timeout', str(args.timeout), '--build-timeout', str(args.build_timeout)]


def main(argv=None):
    args, catalog, preset = arguments(argv)
    cases = make_plan(args, catalog)
    print('INPUT:', preset['name'], args.input_config, flush=True)
    print('CAPACITY: existing platform capacity (currently 4 GiB/GPU); no 2 GiB override', flush=True)
    for row in cases:
        print(f"{row['benchmark']} BW={row['bandwidth_gbs']:g}: {row['action']}", flush=True)
    count = sum(row['action']=='RUN_INV_AND_DELTA' for row in cases)
    print(f'PENDING: {count} pairs = {2*count} simulations', flush=True)
    status = 0
    for bandwidth in args.bandwidths:
        names = [r['benchmark'] for r in cases if r['bandwidth_gbs']==bandwidth and r['action']=='RUN_INV_AND_DELTA']
        if not names:
            continue
        command = run_arguments(args, bandwidth, names)
        print('COMMAND:', shlex.join(['python3', str(args.root/'runs/run_benchmarks.py'), *command]), flush=True)
        if not args.dry_run:
            code = runner.main(command)
            status = max(status, code)
    return status


if __name__ == '__main__':
    raise SystemExit(main())
