#!/usr/bin/env python3
"""Compare INV with free in-kernel replication on BFS, NW, LUD and FW."""
import sys
import run_recommended

BENCHMARKS=['BFS','NW','lud','FW']


def suite_arguments(argv):
    # The later user arguments may select a subset, page sizes or another scale.
    # Inputs and all simulation controls use the existing recommended runner.
    return run_recommended.suite_arguments(['--bench',','.join(BENCHMARKS),*argv])


def main(argv=None):
    args=sys.argv[1:] if argv is None else argv
    if '--help' in args or '-h' in args:
        print(__doc__)
        print('Default: BFS,NW,lud,FW; inv,infinity; recommended-start inputs.')
    return run_recommended.run_benchmarks.main(suite_arguments(args))


if __name__=='__main__':
    sys.exit(main())
