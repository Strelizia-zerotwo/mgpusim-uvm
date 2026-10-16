#!/usr/bin/env python3
"""Record once and replay fair oracle policies on a small selected workload."""
import argparse
import datetime
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import tarfile
import uuid

ROOT = Path(__file__).resolve().parents[2]
SOURCE = ROOT / "mgpusim"
REVISION = "replica-owner-v3"


def dump(path, value):
    path.write_text(json.dumps(value, indent=2, allow_nan=False) + "\n")


def integers(text):
    values = [int(x) for x in text.split(",")]
    if not values or any(x < 0 for x in values) or len(values) != len(set(values)):
        raise ValueError("expected unique nonnegative comma-separated integers")
    return values


def execute(cmd, directory):
    directory.mkdir(parents=True, exist_ok=False)
    dump(directory / "command.json", cmd)
    with (directory / "run.log").open("w") as log:
        proc = subprocess.run(cmd, cwd=directory, stdout=log, stderr=subprocess.STDOUT)
    log = (directory / "run.log").read_text()
    if proc.returncode or "Passed!" not in log:
        print(log, flush=True)
        raise RuntimeError(f"simulation or numerical verification failed: {directory}")
    stats = json.loads((directory / "stats.json").read_text())
    if stats["model_revision"] != REVISION or stats["fabric_model"]!="destination-ready-v2" or not stats["epochs"]:
        raise RuntimeError("unexpected revision or empty simulation")
    if stats["application_ps"] != sum(stats[k] for k in ("kernel_ps", "barrier_flush_ps", "recovery_wait_ps")):
        raise RuntimeError("elapsed-time accounting mismatch")
    return stats


def verify_replay(stats, plan, gpu_ids, args):
    if not stats["oracle_mapping_preinstall"] or stats["mapping_faults"] != 0:
        raise RuntimeError("fair oracle replay must preinstall managed mappings without demand faults")
    installs = sum(len(e["accesses"] or []) for e in plan["epochs"])
    if stats["mapping_installs"] != installs or stats["tlb_flush_epochs"] != len(plan["epochs"]):
        raise RuntimeError("mapping/TLB treatment differs from the common plan")
    if stats["config"]["policy"] in ["i1", "i1-full-stale", "i1-adaptive"]:
        assert stats["write_epoch_new_copies"] == 0
        assert stats["write_epoch_new_copy_bytes"] == 0
        assert stats["write_epoch_new_copy_faults"] == 0
        assert stats["write_epoch_new_replica_reads"] == 0
    if args.workload.startswith("parallel-"):
        if args.home_placement == "owner" and args.align_owner_pages:
            if stats["non_owner_write_transactions"] != 0:
                raise RuntimeError("owner-aligned grid still performed remote writes")
            if set(map(int, stats["home_pages_by_gpu"])) != set(gpu_ids):
                raise RuntimeError("owner placement did not distribute actual physical homes")
        for epoch in stats["epoch_activity"]:
            active = {int(g) for g, n in epoch["instructions_by_gpu"].items() if n}
            if active != set(gpu_ids):
                raise RuntimeError("a partition GPU did not execute actual instructions")
            if epoch["inflight_gpu_duration_ps"].get(str(len(gpu_ids)), 0) <= 0:
                raise RuntimeError("all GPU partitions did not overlap in CU instruction execution")


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--workload", choices=["probe", "parallel-stencil", "parallel-stencil-overlap", "parallel-fw", "fw", "stencil"], default="probe")
    ap.add_argument("--gpus", default="1,2,3,4")
    ap.add_argument("--size", type=int)
    ap.add_argument("--columns", type=int, default=258)
    ap.add_argument("--iterations", type=int)
    ap.add_argument("--page-bytes", type=int, default=4096)
    ap.add_argument("--probe-pages", type=int, choices=[1, 2], default=1)
    ap.add_argument("--probe-case", choices=["standard", "cold-write", "fallback"], default="standard")
    ap.add_argument("--fault-ns", default="900,20000")
    ap.add_argument("--policies", default="norep,inv,i1,i1-full-stale,i1-adaptive")
    ap.add_argument("--home-placement", choices=["owner", "gpu1"], default="owner")
    ap.add_argument("--unaligned-owner-pages", action="store_false", dest="align_owner_pages",
                    help="Use the original grid layout; pages crossing partitions use majority ownership")
    ap.add_argument("--repeats", type=int, default=1)
    ap.add_argument("--record-only", action="store_true")
    ap.add_argument("--replica-pages-per-gpu", type=int, default=0)
    ap.add_argument("--transfer-gran-bytes", type=int, default=32)
    ap.add_argument("--bandwidth-gbs", type=float, default=300)
    ap.add_argument("--link-ns", default="200", help="Additional one-way fabric ns, e.g. 0,100,200,500")
    ap.add_argument("--sensitivity", action="store_true")
    args = ap.parse_args()
    if args.size is None:
        args.size = 258 if args.workload=="parallel-stencil-overlap" else (258 if args.workload=="parallel-stencil" else (128 if args.workload=="parallel-fw" else 32))
    if args.iterations is None:
        args.iterations = 4 if args.workload.startswith("parallel-stencil") else 8
    if args.repeats < 1:
        ap.error("repeats must be positive")
    if args.workload.startswith("parallel-stencil") and args.align_owner_pages:
        pitch_bytes = ((args.columns + 15)//16)*16*4
        count = len(args.gpus.split(","))
        if ((args.size-2)//count)*pitch_bytes % args.page_bytes:
            ap.error("partition bytes must be page-aligned; use --size 258 with 4KiB pages or --unaligned-owner-pages")
    faults, links = integers(args.fault_ns), integers(args.link_ns)
    gpu_ids = integers(args.gpus)
    if any(g < 1 for g in gpu_ids):
        ap.error("GPU IDs must be positive")
    go = shutil.which("go") or "/usr/local/go/bin/go"
    run = ROOT / "results/replica-recovery" / args.workload / (
        datetime.datetime.now().strftime("%Y-%m-%d_%H-%M-%S") + "_v3_" + uuid.uuid4().hex[:6])
    run.mkdir(parents=True)
    binary = run / "replica"
    print(f"Results: {run}", flush=True)
    subprocess.run([go, "build", "-o", str(binary), "./amd/samples/replica"], cwd=SOURCE, check=True)
    common = [str(binary), "-timing", "-disable-rtm", "-use-unified-memory", "-vm-mode=demand", "-verify",
              f"-gpus={args.gpus}", f"-workload={args.workload}", f"-size={args.size}",
              f"-columns={args.columns}", f"-iterations={args.iterations}", f"-page-bytes={args.page_bytes}",
              f"-home-placement={args.home_placement}", f"-align-owner-pages={str(args.align_owner_pages).lower()}",
              f"-probe-pages={args.probe_pages}", f"-probe-case={args.probe_case}",
              "-metric-file-name=metrics", "-report-all", "-replica-plan-out=observed-plan.json", "-replica-stats=stats.json"]
    cfg = json.loads((ROOT / "benchmark/replica-recovery/configs/inv.json").read_text())
    cfg.update(page_bytes=args.page_bytes, transfer_gran_bytes=args.transfer_gran_bytes,
               bandwidth_gbs=args.bandwidth_gbs, link_ns=links[0], fault_ns=faults[0],
               replica_pages_per_gpu=args.replica_pages_per_gpu)
    hashes = {}
    for directory in [SOURCE / "amd", ROOT / "akita-patched"]:
        for path in sorted(directory.rglob("*.go")):
            hashes[str(path.relative_to(ROOT))] = hashlib.sha256(path.read_bytes()).hexdigest()
    dump(run / "source-sha256.json", hashes)
    with tarfile.open(run/"source-archive.tar.gz", "w:gz") as archive:
        for name in hashes:
            archive.add(ROOT/name, arcname=name)
    dump(run / "experiment-sha256.json", {
        str(p.relative_to(ROOT)): hashlib.sha256(p.read_bytes()).hexdigest()
        for p in [Path(__file__), *sorted((ROOT/"benchmark/replica-recovery/configs").glob("*.json"))]})
    scope = "explicit barriers; selected probe or rotating single-GPU kernels"
    if args.workload=="parallel-fw":
        scope="concurrent row-partitioned conditional-write Floyd-Warshall, static owner placement"
    if args.workload.startswith("parallel-stencil"):
        scope = ("concurrent row-partitioned 9-point Jacobi; " +
 ("private snapshots, overlapping interior computation/halo reads, boundary stage" if args.workload.endswith("overlap") else "double-buffered one-row halo"))
    dump(run / "manifest.json", {"revision": REVISION, "arguments": vars(args), "scope": scope,
                                 "go": subprocess.check_output([go, "version"], text=True).strip(),
                                 "link_ns_meaning": "additional per-message one-way delay, beyond original RDMA stages; assumption, not calibrated"})
    record_cfg = run / "record-config.json"
    dump(record_cfg, {**cfg, "policy": "norep"})
    record = run / "record"
    execute(common + [f"-replica-config={record_cfg}"], record)
    plan_path = record / "observed-plan.json"
    plan = json.loads(plan_path.read_text())
    if not sum(len(e["accesses"] or []) for e in plan["epochs"]):
        raise RuntimeError("empty plan")
    dump(record / "page-ownership.json", json.loads((record/"stats.json").read_text())["page_ownership"])
    if args.record_only:
        print(f"Record only: {plan_path}", flush=True)
        return
    cases = []
    for link in links:
        for fault in faults:
            for policy in args.policies.split(","):
                cases.append((f"{policy}_link-{link}ns_fault-{fault}ns",
                              {**cfg, "policy": policy, "fault_ns": fault, "link_ns": link}))
    if args.sensitivity:
        for link in links:
            for policy in ["inv", "i1"]:
                cases.append((f"{policy}_link-{link}ns_infinite-bw",
                              {**cfg, "policy": policy, "link_ns": link, "bandwidth_gbs": 0}))
                cases.append((f"{policy}_link-{link}ns_zero-fault",
                              {**cfg, "policy": policy, "link_ns": link, "fault_ns": 0}))
    cases = [(name + (f"_repeat-{repeat}" if args.repeats>1 else ""), c)
             for repeat in range(args.repeats) for name,c in cases]
    rows = []
    ownership = json.loads((record/"page-ownership.json").read_text())
    for name, case in cases:
        if case["policy"] == "i2":
            case.update(metadata_ns=2, shootdown_ns=100)
        config_path = run / (name + ".json")
        dump(config_path, case)
        directory = run / name
        stats = execute(common + [f"-replica-config={config_path}", f"-replica-plan={plan_path}"], directory)
        verify_replay(stats, plan, gpu_ids, args)
        if stats["page_ownership"] != ownership:
            raise RuntimeError("physical ownership changed across strategies")
        stats["case"] = name
        stats["repeat"] = int(name.rsplit("_repeat-",1)[1]) if "_repeat-" in name else 0
        rows.append(stats)
        print(f"{name}: Passed; {stats['application_ps']/1e6:.3f} us, copies={stats['copy_bytes']} B, "
              f"mapping-faults={stats['mapping_faults']}, fallback={stats['fallback_page_epochs']}, "
              f"write-new={stats['write_epoch_new_copies']}", flush=True)
    dump(run / "summary.json", rows)
    write_envelope(run, rows)
    columns = ["case", "policy", "link_ns", "fault_ns", "application_us", "kernel_us", "barrier_us",
               "recovery_us", "copy_bytes", "copy_faults", "mapping_faults", "fallback_pages",
               "multi_writer_pages", "rw_overlap_pages", "S3_eligible_reads", "write_existing_reads",
               "write_new_reads", "write_new_copies", "write_new_bytes", "managed_pages", "peak_pages", "evictions",
               "owner_writes", "non_owner_writes", "non_owner_reads", "replica_reads", "remote_home_reads",
               "owner_shared_copy_bytes", "owner_shared_recovery_us", "repeat"]
    lines = ["\t".join(columns)]
    for s in rows:
        c = s["config"]
        values = [s["case"], c["policy"], c["link_ns"], c["fault_ns"], s["application_ps"]/1e6,
                  s["kernel_ps"]/1e6, s["barrier_flush_ps"]/1e6, s["recovery_wait_ps"]/1e6,
                  s["copy_bytes"], s["copy_faults"], s["mapping_faults"], s["fallback_page_epochs"],
                  s["multiple_writer_page_epochs"], s["read_write_overlap_page_epochs"],
                  s["s3_preexisting_replica_read_transactions"], s["write_epoch_existing_replica_reads"],
                  s["write_epoch_new_replica_reads"], s["write_epoch_new_copies"], s["write_epoch_new_copy_bytes"],
                  s["managed_pages"], s["peak_replica_pages"], s["evictions"],
                  s["owner_write_transactions"],s["non_owner_write_transactions"],s["non_owner_read_transactions"],
                  s["replica_read_transactions"],s["remote_home_read_transactions"],
                  s["copy_bytes_by_role"].get("owner-written-shared-read",0),
                  (s["recovery_wait_ps_by_role"] or {}).get("owner-written-shared-read",0)/1e6,s["repeat"]]
        lines.append("\t".join(map(str, values)))
    (run / "summary.tsv").write_text("\n".join(lines) + "\n")
    print(f"Complete: {run / 'summary.tsv'}", flush=True)


def write_envelope(run, rows):
    """Whole-run selection with INV included, never a fabricated hybrid timing."""
    groups = {}
    for s in rows:
        c=s["config"]
        key=(c["link_ns"],c["fault_ns"],c["bandwidth_gbs"],s["repeat"])
        groups.setdefault(key,[]).append(s)
    out=[]
    candidates={"inv","i1","i1-full-stale","i1-adaptive"}
    for (link,fault,bw,repeat),items in groups.items():
        base=next((s for s in items if s["config"]["policy"]=="inv"),None)
        if base is None: continue
        eligible=[s for s in items if s["config"]["policy"] in candidates]
        best=min(eligible,key=lambda s:(s["application_ps"],s["case"]))
        assert best["application_ps"] <= base["application_ps"]
        out.append({"selection_scope":"whole completed application, INV included",
                    "link_ns":link,"fault_ns":fault,"bandwidth_gbs":bw,"repeat":repeat,
                    "selected_policy":best["config"]["policy"],"selected_case":best["case"],
                    "application_us":best["application_ps"]/1e6,"inv_application_us":base["application_ps"]/1e6,
                    "saving_percent":100*(base["application_ps"]-best["application_ps"])/base["application_ps"],
                    "candidate_cases":[s["case"] for s in eligible]})
    dump(run/"oracle-envelope.json",out)
    columns=["link_ns","fault_ns","bandwidth_gbs","repeat","selected_policy","application_us","inv_application_us","saving_percent"]
    (run/"oracle-envelope.tsv").write_text("\t".join(columns)+"\n"+
        "".join("\t".join(str(s[k]) for k in columns)+"\n" for s in out))


if __name__ == "__main__":
    main()
