#!/usr/bin/env python3
"""Writes the demo goldens (testdata/demo) from the original HTNDemo.

htn-demo-oracle (tools/oracle/demo_oracle.cpp) drives the original demo's
world, daemon and agent sources headlessly; the Go and Nim htn-demo traces
must match its output byte for byte:

  python3 tools/demo/make_goldens.py --oracle build/oracle/htn-demo-oracle
"""
import argparse
import os
import subprocess

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
GOLDENS = {
    'runner.golden': ['runner'],
    'runner_rt.golden': ['runner', '--rt'],
    'simulate.golden': ['simulate', '--agents', '8', '--steps', '3600', '--snapshot-every', '300'],
}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--oracle', required=True)
    args = parser.parse_args()
    for name, arguments in GOLDENS.items():
        result = subprocess.run([args.oracle] + arguments, cwd=ROOT, capture_output=True, check=True)
        with open(os.path.join(ROOT, 'testdata', 'demo', name), 'wb') as f:
            f.write(result.stdout)


if __name__ == '__main__':
    main()
