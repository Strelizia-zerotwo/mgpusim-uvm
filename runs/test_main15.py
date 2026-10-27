"""Completion/resume checks and exact experiment routing for the main 15 apps."""
import contextlib
import io
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import run_main15 as main15
import run_benchmarks as runner

ROOT = Path(__file__).resolve().parents[1]


class Main15(unittest.TestCase):
    def fixture(self, root, policy, suite='suite-a', bandwidth=32, nodes=65536):
        directory = root/'results/benchmark/COLOR-2'/suite/'policy'/(policy+'_page-4096_link-200ns_fault-900ns_repeat-0')
        directory.mkdir(parents=True)
        flags = ['-node='+str(nodes), '-degree=8']
        config = dict(policy=policy, page_bytes=4096, sector_bytes=32, transfer_gran_bytes=32,
                      fault_ns=900, link_ns=200, bandwidth_gbs=bandwidth, replica_pages_per_gpu=0,
                      metadata_ns=0, shootdown_ns=0)
        result = dict(benchmark='COLOR-2', policy=policy, protocol=main15.summary.PROTOCOL,
                      status='OK', verification='BENCHMARK_VERIFY_AND_IDEAL_INVARIANTS_PASSED',
                      gpu_activity_ok=True, active_gpus=[1,2,3,4], arch='cdna3', gpu='mi300x',
                      application_us=100, input_flags=flags, input_sha256='same-input',
                      page_bytes=4096, bandwidth_gbs=bandwidth)
        stats = dict(config=config, adapter='core-'+main15.summary.PROTOCOL,
                     initial_input_sha256='same-initial', page_ownership=[dict(page=4096,home_gpu=1)],
                     mapping_faults=0, passive=dict(protocol=main15.summary.PROTOCOL, stale_read_sectors=0, events=[]))
        self.write(directory/'result.json', result)
        self.write(directory/'stats.json', stats)
        self.write(directory/'run.log.process.json', dict(status='OK',returncode=0))
        return directory, flags

    @staticmethod
    def write(path, value):
        path.write_text(json.dumps(value, indent=2)+'\n', encoding='utf-8')

    @staticmethod
    def args(root):
        return SimpleNamespace(root=root, page_bytes=4096, fault_ns=900, link_ns=200,
                               timeout=86400, build_timeout=600, rerun=False)

    def test_groups_cover_exactly_15_without_overlap_and_c2d_is_last(self):
        names = [name for group in main15.GROUPS.values() for name in group]
        self.assertEqual(len(names),15)
        self.assertEqual(len(set(names)),15)
        self.assertEqual(main15.GROUPS['3'][-1],'C2D')
        preset = runner.read(ROOT/'benchmark/mybenchmark/presets/main15-large.json')
        self.assertEqual(set(preset['benchmarks']),set(names))
        self.assertIn('-enable-backward',preset['benchmarks']['C2D'])
        self.assertIn('-node=65536',preset['benchmarks']['SSSP-2'])
        self.assertIn('-node=65536',preset['benchmarks']['CC-2'])
        self.assertEqual(preset['benchmarks']['NW'],['-length=3072'])

    def test_small_input_timeout_or_missing_stats_never_skips_large_pair(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);directory,flags=self.fixture(root,'inv',nodes=16384)
            benchmark=dict(id='COLOR-2',sample='cuda_color2',arch='cdna3',gpu='mi300x')
            args=self.args(root)
            self.assertIsNone(main15.matching_record(directory,benchmark,['-node=65536','-degree=8'],32,args))
            self.assertIsNone(main15.matching_record(directory,benchmark,flags,64,args))
            result=runner.read(directory/'result.json');result['status']='TIMEOUT';self.write(directory/'result.json',result)
            self.assertIsNone(main15.matching_record(directory,benchmark,flags,32,args))
            result['status']='OK';self.write(directory/'result.json',result)
            (directory/'stats.json').unlink()
            self.assertIsNone(main15.matching_record(directory,benchmark,flags,32,args))

    def test_pair_requires_actual_initial_data_homes_and_matching_costs(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);inv,flags=self.fixture(root,'inv');delta,_=self.fixture(root,'delta')
            benchmark=dict(id='COLOR-2',sample='cuda_color2',arch='cdna3',gpu='mi300x');args=self.args(root)
            self.assertIsNotNone(main15.find_completed_pair(benchmark,flags,32,args))
            stats=runner.read(delta/'stats.json');stats['page_ownership'][0]['home_gpu']=2;self.write(delta/'stats.json',stats)
            self.assertIsNone(main15.find_completed_pair(benchmark,flags,32,args))
            stats['page_ownership'][0]['home_gpu']=1;stats['config']['fault_ns']=300;self.write(delta/'stats.json',stats)
            self.assertIsNone(main15.find_completed_pair(benchmark,flags,32,args))

    def test_one_completed_policy_does_not_skip_a_pair(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);_,flags=self.fixture(root,'inv')
            benchmark=dict(id='COLOR-2',sample='cuda_color2',arch='cdna3',gpu='mi300x')
            self.assertIsNone(main15.find_completed_pair(benchmark,flags,32,self.args(root)))

    def test_dry_run_never_calls_runner_and_routes_two_policies_only(self):
        args,catalog,_=main15.arguments(['--root',str(ROOT),'--group','1','--dry-run'])
        command=main15.run_arguments(args,32,['BFS','SSSP-2'])
        self.assertEqual(command[command.index('--policies')+1],'inv,delta')
        self.assertEqual(command[command.index('--protocol')+1],'kernel-internal')
        self.assertEqual(command[command.index('--input-config')+1],str(args.input_config))
        with patch.object(main15,'find_completed_pair',return_value=None),patch.object(main15.runner,'main') as execute:
            with contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(main15.main(['--root',str(ROOT),'--group','1','--dry-run']),0)
            execute.assert_not_called()

    def test_force_rerun_and_explicit_subset(self):
        args,_,_=main15.arguments(['--root',str(ROOT),'--bench','LUD,c2d','--bandwidths','64','--rerun'])
        self.assertEqual(args.selected,['lud','C2D'])
        self.assertEqual(args.bandwidths,[64])
        self.assertTrue(args.rerun)
        self.assertIsNone(main15.find_completed_pair({},[],64,args))
        with contextlib.redirect_stderr(io.StringIO()),self.assertRaises(SystemExit):
            main15.arguments(['--root',str(ROOT),'--bench','BFS,BFS'])

    def test_both_bandwidths_of_other_apps_finish_before_large_convolution(self):
        args=SimpleNamespace(bandwidths=[32,64])
        cases=[dict(benchmark=name,bandwidth_gbs=bw,action='RUN_INV_AND_DELTA')
               for bw in args.bandwidths for name in ['lud','pathfinder','C2D']]
        self.assertEqual(main15.scheduled_jobs(args,cases),
                         [(32,['lud','pathfinder']),(64,['lud','pathfinder']),
                          (32,['C2D']),(64,['C2D'])])


if __name__ == '__main__':
    unittest.main()
