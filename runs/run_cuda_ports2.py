#!/usr/bin/env python3
"""Run SSSP-2, CC-2, COLOR-2, MIS-2, EMBEDDING-2; default inv,infinity."""
import sys
import run_cuda_ports

BENCHMARKS = [name+'-2' for name in run_cuda_ports.BENCHMARKS]

def suite_arguments(argv):
    return run_cuda_ports.suite_arguments(['--bench', ','.join(BENCHMARKS), *argv])

def main(argv=None):
    args=sys.argv[1:] if argv is None else argv
    if '--help' in args or '-h' in args:
        print(__doc__)
        print('Default benchmarks: '+','.join(BENCHMARKS))
    return run_cuda_ports.run_benchmarks.main(suite_arguments(args))

if __name__ == '__main__':
    raise SystemExit(main())
