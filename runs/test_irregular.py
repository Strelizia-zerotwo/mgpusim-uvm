"""New workload identity, policy routing, source provenance and scope reporting."""
import contextlib
import hashlib
import io
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch
import online_policy_runner as online
import run_benchmarks as runner
import run_irregular as irregular

ROOT = Path(__file__).resolve().parents[1]


class IrregularPorts(unittest.TestCase):
    def test_wrapper_preserves_four_policies_and_rejects_misspellings(self):
        args = irregular.suite_arguments([])
        self.assertEqual(args[args.index('--bench')+1], ','.join(irregular.BENCHMARKS))
        self.assertEqual(args[args.index('--policies')+1], 'inv,infinity,delta,delta-sub')
        self.assertEqual(args[args.index('--protocol')+1], 'kernel-internal')
        args = irregular.suite_arguments(['--bench', 'scc,pta', '--policies', 'inv,delta-sub'])
        self.assertEqual(args[args.index('--bench')+1], 'SCC,PTA')
        for flags in [['--bench', 'SCC,SCC'], ['--bench', 'unknown'], ['--policies', 'inv,retain'], ['--policies', ''], ['--timeout', '0']]:
            with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
                irregular.suite_arguments(flags)

    def test_pinned_reference_sources_and_notices(self):
        folder = ROOT/'benchmark/mybenchmark/irregular'
        manifest = runner.read(folder/'SOURCES.json')
        self.assertEqual({r['algorithm'] for r in manifest['records']}, set(irregular.BENCHMARKS))
        for record in manifest['records']:
            self.assertEqual(len(record['commit']), 40)
            self.assertIn(record['commit'], record['source_url'])
            self.assertEqual(hashlib.sha256((folder/record['snapshot']).read_bytes()).hexdigest(), record['source_sha256'])
        for record in manifest['licenses']:
            self.assertEqual(hashlib.sha256((folder/record['path']).read_bytes()).hexdigest(), record['sha256'])

    def test_completion_does_not_falsely_require_solver_convergence(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            benchmark = dict(id='SYMGS', research_port=True, implementation='irregular-algorithmic-amd-v1')
            data = dict(algorithm='SYMGS', implementation=benchmark['implementation'], completed=True,
                        convergence_required=False, completion_criterion='requested complete sweeps')
            (directory/'workload.json').write_text(json.dumps(data), encoding='utf-8')
            self.assertEqual(online.cuda_workload_metadata(directory, benchmark), data)
            data.update(convergence_required=True, converged=False)
            (directory/'workload.json').write_text(json.dumps(data), encoding='utf-8')
            with self.assertRaises(RuntimeError):
                online.cuda_workload_metadata(directory, benchmark)
            data.update(converged=True, implementation='wrong')
            (directory/'workload.json').write_text(json.dumps(data), encoding='utf-8')
            with self.assertRaises(RuntimeError):
                online.cuda_workload_metadata(directory, benchmark)

    def test_all_seven_generate_all_four_real_policy_commands_at_both_page_sizes(self):
        catalog = runner.read(ROOT/'benchmark/mybenchmark/catalog.json')['benchmarks']
        selected = [b for b in catalog if b['id'] in irregular.BENCHMARKS]
        self.assertEqual(len(selected), 7)
        self.assertEqual(len([b for b in catalog if b['group']=='A']), 14)
        args = SimpleNamespace(policies=irregular.POLICIES, pages=[4096,65536], links=[200], faults=[900],
            repeats=1, bandwidth_gbs=300, replica_pages_per_gpu=0, gpus='1,2,3,4', gpu_ids=[1,2,3,4],
            profile='small', mode='policy', timeout=7200, root=ROOT)
        commands = []
        def execute(command, directory, timeout):
            commands.append(command)
            return dict(status='TIMEOUT')
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp)
            def dump(path, data):
                path.parent.mkdir(parents=True, exist_ok=True)
                runner.dump(path, data)
            api = dict(build=lambda _, sample, __: Path('/fake')/sample, dump=dump, execute=execute,
                       base_row=runner.base_row, load_legacy=lambda _:None)
            with patch('builtins.print'):
                for workload in selected:
                    self.assertTrue(workload['research_port'])
                    self.assertEqual(workload['group'], 'F')
                    online.run_online(workload,args,output,output/workload['id'],api)
        self.assertEqual(len(commands), 56)
        for command in commands:
            for flag in ['-unified-gpus=1,2,3,4', '-core-protocol=ideal', '-verify', '-arch=cdna3', '-gpu=mi300x']:
                self.assertIn(flag, command)
            self.assertFalse(any(v.startswith('-core-replica-plan=') for v in command))
        for key in ['workload_scope','workload_update_semantics','workload_levels','workload_triangles','workload_constraints']:
            self.assertIn(key, runner.COLUMNS)


if __name__ == '__main__':
    unittest.main()
