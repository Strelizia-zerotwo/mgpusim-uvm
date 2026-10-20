#!/usr/bin/env python3
"""Run the recommended workloads with explicit start/target input presets."""
import argparse
from pathlib import Path
import sys
import run_benchmarks

FIRST=['BFS','FW','gaussian','lud']
SECOND=['gramschmidt','C2D','NW']


def suite_arguments(argv):
    p=argparse.ArgumentParser(description=__doc__,epilog='Other run_benchmarks options are forwarded, including --timeout, --page-bytes, --fault-ns, --link-ns, --bfs-graph, --lud-size and --dry-run.')
    p.add_argument('--tier',choices=['first','second','all'],default='first')
    p.add_argument('--scale',choices=['start','target'],default='start')
    p.add_argument('--bench',help='optional comma-separated subset of the seven recommended IDs')
    p.add_argument('--scan',action='store_true',help='3 page sizes x 2 fault delays x 2 link delays')
    p.add_argument('--root',type=Path,default=run_benchmarks.ROOT)
    a,remaining=p.parse_known_args(argv)
    ids=a.bench.split(',') if a.bench else FIRST if a.tier=='first' else SECOND if a.tier=='second' else FIRST+SECOND
    if not ids or any(x not in FIRST+SECOND for x in ids) or len(set(ids))!=len(ids):
        p.error('--bench requires unique IDs from '+','.join(FIRST+SECOND))
    defaults=['run','--root',str(a.root),'--mode','policy','--protocol','kernel-internal',
        '--profile','small','--input-config',str(a.root/'benchmark/mybenchmark/presets'/('recommended-'+a.scale+'.json')),
        '--bench',','.join(ids),'--policies','inv,delta,delta-sub','--timeout','7200']
    if a.scan:defaults+=['--page-bytes','4096,65536,2097152','--fault-ns','900,20000','--link-ns','0,200']
    return defaults+remaining


def main(argv=None):
    return run_benchmarks.main(suite_arguments(sys.argv[1:] if argv is None else argv))


if __name__=='__main__':
    sys.exit(main())
