#!/usr/bin/env python3
"""Screen actual request masks before paying for policy replays; no timing prediction."""
import argparse
import hashlib
import json
from pathlib import Path


def analyze(plan, ownership):
    homes = {p["page"]: p["home_gpu"] for p in ownership}
    states = {}
    out = {"epochs": len(plan["epochs"]), "pages": len(homes), "active_gpus": [],
           "write_page_epochs": 0, "multiple_writer_page_epochs": 0,
           "write_sector_sum": 0, "owner_write_transactions": 0, "non_owner_write_transactions": 0,
           "replica_cold_reads": 0, "replica_refills": 0, "invalidation_events": 0,
           "refill_dirty_sectors": 0, "refill_required_stale_sectors": 0,
           "s3_preexisting_clean_read_transactions": 0}
    gpus = set()
    sectors = plan["page_bytes"] // plan["sector_bytes"]
    for epoch, e in enumerate(plan["epochs"]):
        pages = {}
        for a in e["accesses"] or []:
            pages.setdefault(a["page"], []).append(a)
            if a["reads"] or a["writes"]:
                gpus.add(a["gpu"])
        for page, accesses in pages.items():
            home = homes[page]
            st = states.setdefault(page, {"last_write": {}, "copies": {}})
            writers = {a["gpu"] for a in accesses if a["writes"]}
            dirty = set().union(*(set(a["writes"] or []) for a in accesses))
            if dirty:
                out["write_page_epochs"] += 1
                out["write_sector_sum"] += len(dirty)
                out["multiple_writer_page_epochs"] += len(writers) > 1
                for a in accesses:
                    key = "owner_write_transactions" if a["gpu"] == home else "non_owner_write_transactions"
                    out[key] += a["write_transactions"]
                    c = st["copies"].get(a["gpu"])
                    if (a["gpu"] != home and c and c["valid"] and len(writers) == 1
                            and a["reads"] and not set(a["reads"]) & dirty):
                        out["s3_preexisting_clean_read_transactions"] += a["read_transactions"]
                for c in st["copies"].values():
                    if c["valid"]:
                        out["invalidation_events"] += 1
                    c["valid"] = False
                for x in dirty:
                    st["last_write"][x] = epoch
                continue
            for a in accesses:
                if not a["reads"] or a["gpu"] == home:
                    continue
                c = st["copies"].get(a["gpu"])
                if c is None:
                    out["replica_cold_reads"] += 1
                elif not c["valid"]:
                    out["replica_refills"] += 1
                    stale = {x for x, when in st["last_write"].items() if when > c["version"]}
                    out["refill_dirty_sectors"] += len(stale)
                    out["refill_required_stale_sectors"] += len(stale & set(a["reads"]))
                st["copies"][a["gpu"]] = {"valid": True, "version": epoch}
    out["active_gpus"] = sorted(gpus)
    writes, refills = out["write_page_epochs"], out["replica_refills"]
    out["mean_written_fraction_per_write_page_epoch"] = out["write_sector_sum"]/(writes*sectors) if writes else None
    out["mean_dirty_fraction_per_refill"] = out["refill_dirty_sectors"]/(refills*sectors) if refills else None
    out["inv_refill_bytes"] = refills*plan["page_bytes"]
    out["all_stale_refill_bytes"] = out["refill_dirty_sectors"]*plan["sector_bytes"]
    out["required_stale_refill_bytes"] = out["refill_required_stale_sectors"]*plan["sector_bytes"]
    out["scope"] = "written request masks, fixed actual homes, unlimited INV shadow, global epoch barriers; not performance"
    if len(gpus) < 2:
        out["screen"] = "reject: no multi-GPU activity"
    elif refills == 0:
        out["screen"] = "no S1 evidence: no non-owner read refill in this schedule"
    elif out["mean_dirty_fraction_per_refill"] >= .9:
        out["screen"] = "weak S1 candidate: reread replicas are mostly overwritten"
    else:
        out["screen"] = "S1 candidate only: sparse dirty set and cross-GPU reread; needs timing validation"
    return out


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("records", nargs="+", type=Path, help="record directories containing observed-plan.json and stats.json")
    ap.add_argument("--output-dir", required=True, type=Path)
    args = ap.parse_args()
    args.output_dir.mkdir(parents=True, exist_ok=True)
    rows = []
    for p in args.records:
        plan_path, stats_path = p/"observed-plan.json", p/"stats.json"
        plan, stats = json.loads(plan_path.read_text()), json.loads(stats_path.read_text())
        result = analyze(plan, stats["page_ownership"])
        result.update(record=str(p.resolve()), model_revision=stats["model_revision"],
                      plan_sha256=hashlib.sha256(plan_path.read_bytes()).hexdigest(),
                      ownership_sha256=hashlib.sha256(stats_path.read_bytes()).hexdigest())
        rows.append(result)
        print(json.dumps(result, ensure_ascii=False), flush=True)
    (args.output_dir/"screen.json").write_text(json.dumps(rows, indent=2, allow_nan=False)+"\n")
    columns = ["record", "pages", "active_gpus", "write_page_epochs", "mean_written_fraction_per_write_page_epoch",
               "replica_refills", "mean_dirty_fraction_per_refill", "inv_refill_bytes", "all_stale_refill_bytes", "screen"]
    (args.output_dir/"screen.tsv").write_text("\t".join(columns)+"\n"+
        "".join("\t".join(str(s[k]) for k in columns)+"\n" for s in rows))


if __name__ == "__main__":
    main()
