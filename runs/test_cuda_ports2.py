"""V2 identity, preservation of v1 inputs, and runner/report routing."""
import contextlib
import io
import json
from pathlib import Path
from types import SimpleNamespace
import tempfile
import unittest
from unittest.mock import patch

import online_policy_runner as online
import run_benchmarks as runner
import run_cuda_ports
import run_cuda_ports2

ROOT=Path(__file__).resolve().parents[1]


class CUDAPorts2(unittest.TestCase):
    def test_separate_defaults_and_mixed_selection(self):
        old=run_cuda_ports.suite_arguments([])
        new=run_cuda_ports2.suite_arguments([])
        self.assertEqual(old[old.index('--bench')+1],'SSSP,CC,COLOR,MIS,EMBEDDING')
        self.assertEqual(new[new.index('--bench')+1],'SSSP-2,CC-2,COLOR-2,MIS-2,EMBEDDING-2')
        self.assertEqual(new[new.index('--policies')+1],'inv,infinity')
        args=run_cuda_ports2.suite_arguments(['--bench','sssp,SSSP-2','--page-bytes','4096,65536'])
        self.assertEqual(args[args.index('--bench')+1],'SSSP,SSSP-2')
        for selection in ['SSSP-2,SSSP-2','SSSP-3']:
            with contextlib.redirect_stderr(io.StringIO()),self.assertRaises(SystemExit):
                run_cuda_ports2.suite_arguments(['--bench',selection])

    def test_versioned_metadata_cannot_be_mixed(self):
        with tempfile.TemporaryDirectory() as tmp:
            path=Path(tmp)
            for version,id_ in [('cuda-derived-research-adaptation-v1','SSSP'),('cuda-derived-inplace-grouped-v2','SSSP-2')]:
                record=dict(algorithm=id_,implementation=version,converged=True)
                (path/'workload.json').write_text(json.dumps(record),encoding='utf-8')
                self.assertEqual(online.cuda_workload_metadata(path,dict(id=id_,implementation=version)),record)
                with self.assertRaises(RuntimeError):
                    online.cuda_workload_metadata(path,dict(id=id_,implementation='wrong-version'))
                with self.assertRaises(RuntimeError):
                    online.cuda_workload_metadata(path,dict(id='wrong-id',implementation=version))

    def test_all_v2_commands_and_profiles(self):
        catalog=runner.read(ROOT/'benchmark/mybenchmark/catalog.json')['benchmarks']
        by_id={b['id']:b for b in catalog}
        self.assertEqual(len([b for b in catalog if b['group']=='A']),14)
        args=SimpleNamespace(policies=['inv','infinity'],pages=[4096,65536],links=[200],faults=[900],
            repeats=1,bandwidth_gbs=300,replica_pages_per_gpu=0,gpus='1,2,3,4',gpu_ids=[1,2,3,4],
            profile='small',mode='policy',timeout=86400,root=ROOT)
        commands=[]
        def execute(command,directory,timeout):
            commands.append(command)
            return dict(status='TIMEOUT')
        with tempfile.TemporaryDirectory() as tmp:
            out=Path(tmp)
            def dump(path,data):
                path.parent.mkdir(parents=True,exist_ok=True)
                runner.dump(path,data)
            api=dict(build=lambda _,sample,__:Path('/fake')/sample,dump=dump,execute=execute,
                     base_row=runner.base_row,load_legacy=lambda _:None)
            for name in run_cuda_ports2.BENCHMARKS:
                new,old=by_id[name],by_id[name[:-2]]
                self.assertEqual(new['profiles'],old['profiles'])
                self.assertEqual(new['implementation'],'cuda-derived-inplace-grouped-v2')
                self.assertEqual(old['implementation'],'cuda-derived-research-adaptation-v1')
                with patch('builtins.print'):
                    rows=online.run_online(new,args,out,out/name,api)
                self.assertTrue(all('/'+name+'/' in r['directory'].replace('\\','/') for r in rows))
        self.assertEqual(len(commands),20)
        for command in commands:
            self.assertIn('-unified-gpus=1,2,3,4',command)
            self.assertIn('-core-protocol=ideal',command)
            self.assertIn('-verify',command)
            self.assertTrue(command[0].endswith('2'))


if __name__=='__main__':
    unittest.main()
