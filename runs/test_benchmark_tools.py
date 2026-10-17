"""Small regression tests for scientific accounting and failure isolation."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
from types import SimpleNamespace


def load(name):
    path=Path(__file__).with_name(name+'.py')
    spec=importlib.util.spec_from_file_location(name,path)
    module=importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


screen=load('screen_opportunities')
runner=load('run_benchmarks')
online=load('online_policy_runner')
candidates=load('run_idea_candidates')


def access(gpu,reads=(),writes=()):
    return dict(page=4096,gpu=gpu,reads=list(reads),writes=list(writes))


def plan(*epochs):
    return dict(page_bytes=4096,sector_bytes=32,epochs=[dict(accesses=e) for e in epochs])


class ScientificAccounting(unittest.TestCase):
    def test_candidate_wrapper_preserves_shared_suite_and_scan(self):
        args=candidates.suite_arguments(['--scan','--timeout','900'])
        self.assertEqual(args[0],'run')
        self.assertEqual(args[args.index('--bench')+1],'lud,gaussian,pathfinder,backprop,gramschmidt')
        self.assertEqual(args[args.index('--policies')+1],'inv,delta,delta-sub')
        self.assertEqual(args[args.index('--page-bytes')+1],'4096,65536,2097152')
        self.assertEqual(args[-2:],['--timeout','900'])

    def test_five_candidate_commands_use_their_cdna3_binaries(self):
        catalog=runner.read(Path(__file__).resolve().parents[1]/'benchmark/mybenchmark/catalog.json')['benchmarks']
        selected=[b for b in catalog if b['id'] in candidates.BENCHMARKS]
        self.assertEqual(len(selected),5)
        a=SimpleNamespace(policies=['inv','delta','delta-sub'],pages=[4096],links=[200],faults=[900],
            repeats=1,bandwidth_gbs=300,replica_pages_per_gpu=0,gpus='1,2,3,4',gpu_ids=[1,2,3,4],
            profile='small',mode='policy',timeout=10,root=Path('/unused'))
        commands=[];samples=[]
        def execute(cmd,*args):commands.append(cmd);return dict(status='TIMEOUT')
        def build(_,sample,__):samples.append(sample);return Path('/fake')/sample
        with tempfile.TemporaryDirectory() as d:
            root=Path(d)
            def dump(path,obj):path.parent.mkdir(parents=True,exist_ok=True);runner.dump(path,obj)
            api=dict(build=build,dump=dump,execute=execute,base_row=runner.base_row,load_legacy=lambda root:None)
            with patch('builtins.print'):
                for b in selected:
                    self.assertTrue(b['core_replica'])
                    online.run_online(b,a,root,root/b['id'],api)
        self.assertEqual(len(samples),5)
        self.assertEqual(len(commands),15)
        for cmd in commands:
            self.assertIn('-arch=cdna3',cmd);self.assertIn('-gpu=mi300x',cmd)
            self.assertIn('-page-bytes=4096',cmd);self.assertIn('-core-protocol=ideal',cmd)
            self.assertNotIn('-arch=gcn3',cmd)
            self.assertFalse(any(x.startswith('-core-replica-plan=') for x in cmd))

    def test_different_architectures_never_share_speedup_baseline(self):
        base=dict(benchmark='lud',profile='small',mode='policy',repeat=0,status='OK',page_bytes=4096)
        rows=[dict(base,policy='inv',arch='gcn3',gpu='r9nano',application_us=100),
              dict(base,policy='delta',arch='cdna3',gpu='mi300x',application_us=50)]
        with tempfile.TemporaryDirectory() as d:runner.summarize(rows,Path(d))
        self.assertNotIn('speedup_vs_inv',rows[1])

    def test_default_is_exactly_three_ideal_policies(self):
        self.assertEqual(runner.DEFAULT_POLICIES,['inv','delta','delta-sub'])

    def test_page_sizes_never_share_inv_baseline(self):
        base=dict(benchmark='BFS',profile='small',mode='policy',repeat=0,status='OK',protocol=online.PROTOCOL)
        rows=[dict(base,policy='inv',page_bytes=4096,application_us=100),
              dict(base,policy='inv',page_bytes=65536,application_us=200),
              dict(base,policy='delta',page_bytes=65536,application_us=150)]
        with tempfile.TemporaryDirectory() as d:runner.summarize(rows,Path(d))
        self.assertAlmostEqual(rows[2]['speedup_vs_inv'],200/150)

    def test_ordering_warning_preserves_measured_time(self):
        base=dict(benchmark='BFS',profile='small',mode='policy',repeat=0,status='OK',protocol=online.PROTOCOL)
        rows=[dict(base,policy='inv',application_us=100),dict(base,policy='delta',application_us=101)]
        with tempfile.TemporaryDirectory() as d:runner.summarize(rows,Path(d))
        self.assertEqual(rows[1]['application_us'],101)
        self.assertLess(rows[1]['speedup_vs_inv'],1)
        self.assertIn('sanity_warnings',rows[1])

    def test_resummarize_removes_stale_failed_speedup(self):
        row=dict(benchmark='ST',profile='small',mode='policy',repeat=0,status='VALIDATION_FAILED',speedup_vs_inv=10)
        with tempfile.TemporaryDirectory() as d:runner.summarize([row],Path(d))
        self.assertNotIn('speedup_vs_inv',row)

    def test_three_page_twelve_parameter_scan_without_running_simulator(self):
        a=SimpleNamespace(policies=['inv','delta','delta-sub'],pages=[4096,65536,2097152],links=[0,200],faults=[900,20000],
            repeats=1,bandwidth_gbs=300,replica_pages_per_gpu=0,gpus='1,2,3,4',gpu_ids=[1,2,3,4],
            profile='small',mode='policy',timeout=10,root=Path('/unused'))
        calls=[]
        def execute(cmd,directory,timeout):
            calls.append(cmd);return dict(status='TIMEOUT')
        with tempfile.TemporaryDirectory() as d:
            directory=Path(d)
            def dump(path,obj):
                path.parent.mkdir(parents=True,exist_ok=True);runner.dump(path,obj)
            api=dict(build=lambda *args:Path('/fake/binary'),dump=dump,execute=execute,base_row=runner.base_row,load_legacy=lambda root:None)
            with patch('builtins.print'):
                rows=online.run_online(dict(id='FIR',sample='fir',profiles=dict(small=['-length=1024'])),a,directory,directory,api)
        self.assertEqual(len(calls),36)
        self.assertEqual(len({Path(r['directory']).name for r in rows}),36)
        self.assertEqual({r['page_bytes'] for r in rows},set(a.pages))
        for cmd in calls:
            self.assertIn('-core-protocol=ideal',cmd)
            self.assertTrue(any(x.startswith('-page-bytes=') for x in cmd))
            self.assertFalse(any(x.startswith('-core-replica-plan=') for x in cmd))
    def test_different_protocols_never_share_speedup_baseline(self):
        base=dict(benchmark='ST',profile='smoke',mode='policy',repeat=0,status='OK')
        rows=[dict(base,policy='inv',protocol='kernel-barrier',application_us=100),
              dict(base,policy='i1-adaptive',protocol='kernel-internal-online-v1',application_us=50)]
        with tempfile.TemporaryDirectory() as d:
            runner.summarize(rows,Path(d))
        self.assertNotIn('speedup_vs_inv',rows[1])
    def test_multiple_writes_one_invalidation_and_later_refill(self):
        p=plan([access(2,[0,1])],[access(1,writes=[0])],
               [access(1,writes=[1])],[access(2,[0])])
        s=screen.analyze(p,[dict(page=4096,home_gpu=1)])
        self.assertEqual((s['invalidations'],s['refills']),(1,1))
        self.assertEqual(s['dirty_fraction_p50'],2/128)
        self.assertEqual(s['merged_write_epochs_p50'],2)
        self.assertEqual(s['reread_distance_p50'],2)
        self.assertEqual(s['peak_stale_bytes'],4096)
        self.assertEqual(s['refill_events'][0]['required_dirty_sectors'],1)

    def test_no_refill_is_missing_distribution_not_zero(self):
        s=screen.analyze(plan([access(2,[0])],[access(1,writes=[0])]),[dict(page=4096,home_gpu=1)])
        self.assertEqual(s['refills'],0)
        self.assertIsNone(s['dirty_fraction_p50'])
        self.assertEqual(s['stale_copies_at_end'],1)

    def test_retirement_ends_retained_copy_lifetime(self):
        p=plan([access(2,[0])],[access(1,writes=[0])],[])
        p['epochs'][0]['kind']='kernel'
        p['epochs'][1]['kind']='host-write'
        p['epochs'][2].update(kind='retire',retired_pages=[4096])
        s=screen.analyze(p,[dict(page=4096,home_gpu=1)])
        self.assertEqual(s['stale_copies_at_end'],0)
        self.assertEqual(s['kernel_count'],1)
        self.assertEqual(s['active_gpus'],[2])

    def test_requested_skip_is_not_failure_or_speedup(self):
        rows=[dict(benchmark='VGG16',profile='smoke',mode='policy',repeat=0,status='SKIPPED_BY_REQUEST')]
        with tempfile.TemporaryDirectory() as d:
            runner.summarize(rows,Path(d))
            self.assertEqual(json.loads((Path(d)/'failures.json').read_text()),[])
            self.assertEqual(len(json.loads((Path(d)/'skipped.json').read_text())),1)
        self.assertNotIn('speedup_vs_inv',rows[0])

    def test_unknown_home_rejected(self):
        with self.assertRaises(ValueError):
            screen.analyze(plan([access(2,[0])]),[])

    def test_failed_candidate_not_given_speedup(self):
        base=dict(benchmark='ST',profile='smoke',mode='policy',repeat=0,link_ns=200,fault_ns=900,bandwidth_gbs=300)
        rows=[dict(base,policy='inv',status='OK',application_us=100),
              dict(base,policy='i1',status='OK',application_us=80),
              dict(base,policy='i1-adaptive',status='VALIDATION_FAILED',application_us=1)]
        with tempfile.TemporaryDirectory() as d:
            runner.summarize(rows,Path(d))
            saved=json.loads((Path(d)/'summary.json').read_text())
        self.assertEqual(saved[1]['speedup_vs_inv'],1.25)
        self.assertNotIn('speedup_vs_inv',saved[2])

    def test_mapping_faults_and_activity_come_from_actual_metrics(self):
        m={('Driver','kernel_time'):1e-6,('GPU[1].CommandProcessor','kernel_time'):1e-6,
           ('GPU[2].CommandProcessor','kernel_time'):1e-6,('GPU[1].MMU','vm_local_faults'):3,
           ('GPU[1].CU[0]','cu_inst_count'):100,('GPU[2].CU[0]','cu_inst_count'):0}
        s=runner.metric_summary(m)
        self.assertEqual(s['active_gpus'],[1])
        self.assertEqual(s['mapping_faults'],3)
        self.assertEqual(s['kernel_us'],1)
        self.assertIsNone(s['kernel_count'])


if __name__=='__main__':
    unittest.main()
