"""Matched workload inputs and real four-policy command routing."""
import contextlib
import copy
import io
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import online_policy_runner as online
import run_benchmarks as runner
import run_delta

ROOT = Path(__file__).resolve().parents[1]


class DeltaWorkflow(unittest.TestCase):
    def test_defaults_and_explicit_overrides(self):
        args = run_delta.suite_arguments([])
        self.assertEqual(args[args.index('--bench')+1], ','.join(run_delta.BENCHMARKS))
        self.assertEqual(args[args.index('--policies')+1], 'inv,delta,delta-sub,infinity')
        self.assertEqual(args[args.index('--protocol')+1], 'kernel-internal')
        self.assertTrue(args[args.index('--input-config')+1].endswith('delta-start.json'))
        args = run_delta.suite_arguments(['--bench','nw,sssp-2','--policies','delta,delta-sub',
                                         '--page-bytes','65536','--timeout','1800'])
        self.assertEqual(args[args.index('--bench')+1], 'NW,SSSP-2')
        self.assertEqual(args[args.index('--policies')+1], 'delta,delta-sub')
        self.assertEqual(args[args.index('--page-bytes')+1], '65536')
        self.assertEqual(args[args.index('--timeout')+1], '1800')
        self.assertNotIn('--input-config', run_delta.suite_arguments(['--profile','smoke']))
        args = run_delta.suite_arguments(['--input-config','/custom/inputs.json'])
        self.assertEqual(args[args.index('--input-config')+1], '/custom/inputs.json')

    def test_reject_duplicate_or_unknown_workloads_and_policies(self):
        for argv in [['--bench','NW,NW'],['--bench',''],['--bench','SSSP'],
                     ['--policies','delta,delta'],['--policies','i1'],['--timeout','0']]:
            with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
                run_delta.suite_arguments(argv)

    def test_inputs_match_completed_experiments(self):
        catalog = runner.read(ROOT/'benchmark/mybenchmark/catalog.json')['benchmarks']
        before = copy.deepcopy(catalog)
        preset = runner.read(ROOT/'benchmark/mybenchmark/presets/delta-start.json')
        self.assertEqual(set(preset['benchmarks']),set(run_delta.BENCHMARKS))
        for name in run_delta.BENCHMARKS[:-1]:
            original = next(b for b in before if b['id']==name)
            self.assertEqual(preset['benchmarks'][name],original['profiles']['small'])
        self.assertEqual(preset['benchmarks']['NW'],['-length=2048'])
        runner.apply_input_config(catalog,'small',preset)
        self.assertEqual(next(b for b in catalog if b['id']=='SSSP'),
                         next(b for b in before if b['id']=='SSSP'))

    def test_six_workloads_route_all_policies_at_both_page_sizes(self):
        catalog = runner.read(ROOT/'benchmark/mybenchmark/catalog.json')['benchmarks']
        runner.apply_input_config(catalog,'small',runner.read(ROOT/'benchmark/mybenchmark/presets/delta-start.json'))
        selected = [next(b for b in catalog if b['id']==name) for name in run_delta.BENCHMARKS]
        args = SimpleNamespace(policies=run_delta.POLICIES,pages=[4096,65536],links=[200],faults=[900],
            repeats=1,bandwidth_gbs=300,replica_pages_per_gpu=0,gpus='1,2,3,4',gpu_ids=[1,2,3,4],
            profile='small',mode='policy',timeout=86400,root=ROOT)
        captured=[]
        def execute(command,directory,timeout):
            config=runner.read(Path(next(x.split('=',1)[1] for x in command if x.startswith('-core-replica-config='))))
            captured.append((command,config))
            return dict(status='TIMEOUT')
        with tempfile.TemporaryDirectory() as tmp:
            out=Path(tmp)
            def dump(path,value):
                path.parent.mkdir(parents=True,exist_ok=True)
                runner.dump(path,value)
            api=dict(build=lambda _,sample,__:Path('/fake')/sample,dump=dump,execute=execute,
                     base_row=runner.base_row,load_legacy=lambda _:None)
            with patch('builtins.print'):
                for b in selected:
                    self.assertTrue(b['core_replica'])
                    rows=online.run_online(b,args,out,out/b['id'],api)
                    self.assertEqual({r['policy'] for r in rows},set(run_delta.POLICIES))
        self.assertEqual(len(captured),48)
        for command,config in captured:
            self.assertIn('-core-protocol=ideal',command)
            self.assertIn('-unified-gpus=1,2,3,4',command)
            self.assertIn('-verify',command)
            self.assertFalse(any(x.startswith('-core-replica-plan=') for x in command))
            self.assertIn(config['policy'],run_delta.POLICIES)
            self.assertEqual(config['fault_ns'],900)
            self.assertEqual(config['link_ns'],200)
            self.assertEqual(config['sector_bytes'],32)
            self.assertEqual(config['metadata_ns'],0)
        nw=[cmd for cmd,_ in captured if cmd[0].endswith('/nw')]
        self.assertEqual(len(nw),8)
        self.assertTrue(all('-length=2048' in cmd for cmd in nw))


if __name__=='__main__':
    unittest.main()
