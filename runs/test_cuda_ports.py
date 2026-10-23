"""Provenance and real command routing for the five new research workloads."""
import contextlib
import hashlib
import io
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import run_benchmarks as runner
import run_cuda_ports as ports
import online_policy_runner as online
import build_cuda_ports

ROOT = Path(__file__).resolve().parents[1]


class CUDAResearchPorts(unittest.TestCase):
    def test_failure_summary_reports_the_actual_panic(self):
        with tempfile.TemporaryDirectory() as tmp:
            folder=Path(tmp)
            (folder/'run.log').write_text('initialization\nPanic: faultvm: no usable authoritative allocation\nstack\n',encoding='utf-8')
            self.assertEqual(online.failure_summary(folder,'PROCESS_FAILED',86400),'Panic: faultvm: no usable authoritative allocation')
            self.assertIn('86400s',online.failure_summary(folder,'TIMEOUT',86400))

    def test_compiler_exports_resource_counts_from_each_kernel_descriptor(self):
        assembly = '.amdhsa_kernel one\n.amdhsa_next_free_sgpr 38\n.amdhsa_next_free_vgpr 14\n.end_amdhsa_kernel\n.amdhsa_kernel two\n.amdhsa_next_free_sgpr 18\n.amdhsa_next_free_vgpr 8\n.end_amdhsa_kernel\n'
        symbols, counts = build_cuda_ports.resource_symbols(assembly)
        self.assertEqual(counts['one'], dict(numbered_sgpr=38,num_vgpr=14))
        self.assertIn('.globl one.numbered_sgpr',symbols)
        self.assertIn('.set two.num_vgpr, 8',symbols)
        with self.assertRaises(ValueError):
            build_cuda_ports.resource_symbols('.amdhsa_kernel bad\n.end_amdhsa_kernel\n')

    def test_wrapper_defaults_and_explicit_overrides(self):
        args = ports.suite_arguments([])
        self.assertEqual(args[args.index('--bench')+1], 'SSSP,CC,COLOR,MIS,EMBEDDING')
        self.assertEqual(args[args.index('--policies')+1], 'inv,infinity')
        self.assertEqual(args[args.index('--timeout')+1], '86400')
        args = ports.suite_arguments(['--bench', 'sssp,embedding', '--timeout', '172800', '--page-bytes', '65536'])
        self.assertEqual(args[args.index('--bench')+1], 'SSSP,EMBEDDING')
        self.assertEqual(args[args.index('--timeout')+1], '172800')
        self.assertEqual(args[args.index('--page-bytes')+1], '65536')
        for selection in ['SSSP,SSSP', 'SSSP,unknown', '']:
            with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
                ports.suite_arguments(['--bench', selection])

    def test_pinned_sources_and_licenses_match_recorded_hashes(self):
        folder = ROOT/'benchmark/mybenchmark/cuda-ports'
        sources = runner.read(folder/'SOURCES.json')
        self.assertEqual({r['algorithm'] for r in sources['records']}, set(ports.BENCHMARKS))
        for source in sources['records']:
            self.assertEqual(hashlib.sha256((folder/source['snapshot']).read_bytes()).hexdigest(), source['source_sha256'])
            self.assertIn(source['commit'], source['source_url'])
        self.assertEqual(len(sources['licenses']), 3)
        for license in sources['licenses']:
            self.assertEqual(hashlib.sha256((folder/license['path']).read_bytes()).hexdigest(), license['sha256'])

    def test_all_five_reach_actual_four_gpu_policy_commands_at_both_page_sizes(self):
        catalog = runner.read(ROOT/'benchmark/mybenchmark/catalog.json')['benchmarks']
        selected = [b for b in catalog if b['id'] in ports.BENCHMARKS]
        self.assertEqual(len(selected), 5)
        self.assertEqual(len([b for b in catalog if b['group']=='A']), 14)
        args = SimpleNamespace(policies=['inv','infinity'],pages=[4096,65536],links=[200],faults=[900],
            repeats=1,bandwidth_gbs=300,replica_pages_per_gpu=0,gpus='1,2,3,4',gpu_ids=[1,2,3,4],
            profile='small',mode='policy',timeout=86400,root=ROOT)
        commands = []
        def execute(command, directory, timeout):
            commands.append((command, timeout))
            return dict(status='TIMEOUT')
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp)
            def dump(path, value):
                path.parent.mkdir(parents=True,exist_ok=True)
                runner.dump(path,value)
            api=dict(build=lambda _,sample,__:Path('/fake')/sample,dump=dump,execute=execute,
                     base_row=runner.base_row,load_legacy=lambda _:None)
            with patch('builtins.print'):
                for workload in selected:
                    self.assertTrue(workload['core_replica'])
                    self.assertTrue(workload['cuda_port'])
                    self.assertEqual(workload['arch'],'cdna3')
                    self.assertEqual(workload['gpu'],'mi300x')
                    online.run_online(workload,args,output,output/workload['id'],api)
        self.assertEqual(len(commands),20)
        for command,timeout in commands:
            self.assertEqual(timeout,86400)
            for flag in ['-arch=cdna3','-gpu=mi300x','-unified-gpus=1,2,3,4','-core-protocol=ideal','-verify']:
                self.assertIn(flag,command)
            self.assertFalse(any(flag.startswith('-core-replica-plan=') for flag in command))


if __name__ == '__main__':
    unittest.main()
