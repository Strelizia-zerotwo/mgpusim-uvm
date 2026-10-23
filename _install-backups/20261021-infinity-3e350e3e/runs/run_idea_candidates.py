#!/usr/bin/env python3
"""Run the five supplemental idea candidates using the ideal in-kernel model."""
import sys
import run_benchmarks

BENCHMARKS=['lud','gaussian','pathfinder','backprop','gramschmidt']


def suite_arguments(argv):
    argv=list(argv)
    scan='--scan' in argv
    argv=[arg for arg in argv if arg!='--scan']
    defaults=['run','--mode','policy','--protocol','kernel-internal',
              '--bench',','.join(BENCHMARKS),'--profile','small',
              '--policies','inv,delta,delta-sub']
    if scan:
        defaults+=['--page-bytes','4096,65536,2097152',
                   '--fault-ns','900,20000','--link-ns','0,200']
    return defaults+argv


def main(argv=None):
    argv=sys.argv[1:] if argv is None else argv
    if '--help' in argv or '-h' in argv:
        print(__doc__)
        print('Default: five candidates, small inputs, inv/delta/delta-sub.')
        print('--scan: expand the 3 page sizes x 2 fault delays x 2 link delays.')
        print('All run_benchmarks options below may be used to override defaults.\n')
    return run_benchmarks.main(suite_arguments(argv))


if __name__=='__main__':
    sys.exit(main())
