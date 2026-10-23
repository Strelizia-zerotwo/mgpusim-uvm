"""Recommended presets, input provenance and actual policy command routing."""
import contextlib
import copy
import importlib.util
import io
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch
import run_benchmarks as runner
import run_recommended as recommended
import online_policy_runner as online
import run_infinity as infinity

ROOT=Path(__file__).resolve().parents[1]

class RecommendedInputs(unittest.TestCase):
    def catalog(self):return runner.read(ROOT/'benchmark/mybenchmark/catalog.json')['benchmarks']
    def preset(self,scale):return runner.read(ROOT/'benchmark/mybenchmark/presets'/('recommended-'+scale+'.json'))
    def test_first_tier_default_and_target_scan(self):
        args=recommended.suite_arguments([])
        self.assertEqual(args[args.index('--policies')+1],'inv,infinity')
        self.assertEqual(args[args.index('--bench')+1],'BFS,FW,gaussian,lud')
        self.assertTrue(args[args.index('--input-config')+1].endswith('recommended-start.json'))
        args=recommended.suite_arguments(['--tier','all','--scale','target','--scan'])
        self.assertEqual(len(args[args.index('--bench')+1].split(',')),7)
        self.assertIn('4096,65536,2097152',args)
        self.assertTrue(args[args.index('--input-config')+1].endswith('recommended-target.json'))
    def test_infinity_entrypoint_uses_recommended_inputs_and_both_policies(self):
        args=infinity.suite_arguments([])
        self.assertEqual(args[args.index('--bench')+1],'BFS,NW,lud,FW')
        self.assertEqual(args[args.index('--policies')+1],'inv,infinity')
        self.assertIn('--protocol',args)
        self.assertIn('kernel-internal',args)
        self.assertTrue(args[args.index('--input-config')+1].endswith('recommended-start.json'))
        args=infinity.suite_arguments(['--bench','NW,lud','--page-bytes','65536','--dry-run'])
        self.assertEqual(args[args.index('--bench')+1],'NW,lud')
        self.assertEqual(args[args.index('--page-bytes')+1],'65536')
        self.assertIn('--dry-run',args)
    def test_fw_pass_counts_and_full_bfs_search(self):
        start,target=self.preset('start')['benchmarks'],self.preset('target')['benchmarks']
        self.assertEqual(start['FW'],['-node=256','-iter=256'])
        self.assertEqual(target['FW'],['-node=512','-iter=256'])
        self.assertIn('-depth=0',start['BFS']);self.assertIn('-depth=0',target['BFS'])
        self.assertEqual(start['NW'],['-length=2048'])
        for scale,complete in [('start',True),('target',False)]:
            catalog=self.catalog();runner.apply_input_config(catalog,'small',self.preset(scale))
            b=next(b for b in catalog if b['id']=='FW')
            row=runner.base_row(b,SimpleNamespace(profile='small',mode='policy'),Path('/test'))
            self.assertEqual(row['fw_complete_apsp'],complete)
            self.assertEqual(row['fw_requested_passes'],256)
    def test_config_validation_and_no_mutation_of_other_inputs(self):
        catalog=self.catalog();old=copy.deepcopy(catalog)
        runner.apply_input_config(catalog,'small',self.preset('start'))
        self.assertEqual(next(b for b in catalog if b['id']=='MM'),next(b for b in old if b['id']=='MM'))
        with self.assertRaises(ValueError):runner.apply_input_config(catalog,'small',dict(name='bad',benchmarks={'missing':['-n=1']}))
    def test_real_graph_replaces_synthetic_flags_and_is_identified(self):
        b=next(b for b in self.catalog() if b['id']=='BFS')
        runner.apply_bfs_graph(b,'small',Path('/data/road graph.txt'),'knownhash')
        flags=b['profiles']['small'];self.assertIn('-load-graph=/data/road graph.txt',flags)
        self.assertFalse(any(f.startswith(('-node=','-degree=')) for f in flags))
        self.assertEqual(b['external_input_sha256'],'knownhash')
    def test_fw_nw_use_actual_in_kernel_adapter(self):
        for name,sample in [('FW','floydwarshall'),('NW','nw')]:
            b=next(b for b in self.catalog() if b['id']==name)
            self.assertTrue(b['core_replica']);self.assertEqual(b['sample'],sample)
    def test_different_input_fingerprints_never_share_speedup(self):
        base=dict(benchmark='BFS',profile='recommended-start',mode='policy',repeat=0,status='OK')
        rows=[dict(base,policy='inv',input_sha256='graph1',application_us=100),dict(base,policy='delta',input_sha256='graph2',application_us=50)]
        with tempfile.TemporaryDirectory() as d:runner.summarize(rows,Path(d))
        self.assertNotIn('speedup_vs_inv',rows[1])
    def test_all_seven_presets_reach_policy_commands_without_replay(self):
        catalog=self.catalog();runner.apply_input_config(catalog,'small',self.preset('start'))
        selected=[b for b in catalog if b['id'] in recommended.FIRST+recommended.SECOND]
        a=SimpleNamespace(policies=['inv','delta','delta-sub'],pages=[4096],links=[200],faults=[900],repeats=1,
            bandwidth_gbs=300,replica_pages_per_gpu=0,gpus='1,2,3,4',gpu_ids=[1,2,3,4],profile='small',mode='policy',timeout=1,root=ROOT)
        commands=[]
        with tempfile.TemporaryDirectory() as d:
            out=Path(d)
            def dump(path,obj):path.parent.mkdir(parents=True,exist_ok=True);runner.dump(path,obj)
            def execute(cmd,*_):commands.append(cmd);return dict(status='TIMEOUT')
            api=dict(build=lambda _,sample,__:Path('/fake')/sample,dump=dump,execute=execute,base_row=runner.base_row,load_legacy=lambda _:None)
            with patch('builtins.print'):
                for b in selected:online.run_online(b,a,out,out/b['id'],api)
        self.assertEqual(len(commands),21)
        for cmd in commands:
            self.assertIn('-core-protocol=ideal',cmd)
            self.assertFalse(any(f.startswith('-core-replica-plan=') for f in cmd))
        fw=next(c for c in commands if c[0].endswith('/floydwarshall'))
        self.assertIn('-node=256',fw);self.assertIn('-iter=256',fw)

if __name__=='__main__':unittest.main()
