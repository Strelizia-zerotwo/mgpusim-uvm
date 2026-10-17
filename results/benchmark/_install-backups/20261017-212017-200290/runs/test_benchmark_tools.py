"""Small regression tests for scientific accounting and failure isolation."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest


def load(name):
    path=Path(__file__).with_name(name+'.py')
    spec=importlib.util.spec_from_file_location(name,path)
    module=importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


screen=load('screen_opportunities')
runner=load('run_benchmarks')


def access(gpu,reads=(),writes=()):
    return dict(page=4096,gpu=gpu,reads=list(reads),writes=list(writes))


def plan(*epochs):
    return dict(page_bytes=4096,sector_bytes=32,epochs=[dict(accesses=e) for e in epochs])


class ScientificAccounting(unittest.TestCase):
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
