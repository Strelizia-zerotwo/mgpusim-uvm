"""Build and verify the fixed-placement UVM mapping-fault baseline."""
import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess

import check


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, help="New result directory (must not exist)")
    args = parser.parse_args()
    source = Path(__file__).resolve().parents[2]
    stamp = datetime.datetime.now().strftime("%Y%m%d-%H%M%S")
    output = (args.output or source.parent / "results" / ("uvm-validation-" + stamp)).resolve()
    output.mkdir(parents=True, exist_ok=False)
    print(f"Results: {output}", flush=True)
    env = dict(os.environ)
    env["PATH"] = "/usr/local/go/bin:/home/only/go/bin:" + env.get("PATH", "")
    go = shutil.which("go", path=env["PATH"])
    if not go:
        raise RuntimeError("Go 1.27+ is required")
    commands = []

    def run(command, name, expected=0):
        commands.append({"name": name, "command": list(map(str, command)), "cwd": str(output)})
        (output / "commands.json").write_text(json.dumps(commands, indent=2) + "\n")
        with (output / f"{name}.log").open("w") as log:
            result = subprocess.run(command, cwd=output, env=env, stdout=log, stderr=subprocess.STDOUT,
                                    timeout=300, check=False)
        check.require((result.returncode == 0) if expected == 0 else (result.returncode != 0),
                      f"{name}: unexpected exit {result.returncode}; see {output / (name + '.log')}")
        return (output / f"{name}.log").read_text()

    (output / "go-version.txt").write_text(subprocess.check_output([go, "version"], env=env, text=True))
    inputs = [*source.rglob("*.go"), source / "go.mod", source / "go.sum",
              *(source.parent / "akita-patched").rglob("*.go"), source.parent / "akita-patched/go.mod"]
    hashes = {str(p.relative_to(source.parent)): hashlib.sha256(p.read_bytes()).hexdigest() for p in inputs}
    (output / "source-sha256.json").write_text(json.dumps(hashes, indent=2, sort_keys=True) + "\n")
    # Go resolves packages from the module directory; the simulations run in output.
    for sample in ("fir", "simpleconvolution", "bitonicsort"):
        command = [go, "build", "-o", str(output / sample), "./amd/samples/" + sample]
        with (output / f"build-{sample}.log").open("w") as log:
            subprocess.run(command, cwd=source, env=env, stdout=log, stderr=subprocess.STDOUT,
                           timeout=300, check=True)

    common = ["-timing", "-verify", "-disable-rtm", "-use-unified-memory", "-report-all"]
    cases = [
        ("demand", "fir", "1,2", ["-length=16384"], []),
        ("congested", "fir", "1,2", ["-length=16384"], ["-vm-host-walkers=1", "-vm-host-queue=2"]),
        ("fir-four", "fir", "1,2,3,4", ["-length=4096"], []),
        ("sc-four", "simpleconvolution", "1,2,3,4", ["-width=126", "-height=126", "-mask-size=3"], []),
        ("bs-four", "bitonicsort", "1,2,3,4", ["-length=1024", "-order-asc=true"], []),
    ]
    summaries = []
    for name, sample, gpus, options, extra in cases:
        command = [str(output / sample), *common, "-vm-mode=demand", "-gpus=" + gpus,
                   "-metric-file-name=" + str(output / name), *options, *extra]
        text = run(command, name)
        check.require("Passed!" in text, f"{name}: numerical verification did not pass")
        ids = tuple(map(int, gpus.split(",")))
        metrics = check.read_case(output, name)
        summary = check.check_case(name, metrics, ids)
        if len(ids) == 4:
            for gpu in ids[1:]:
                check.require(metrics[f"GPU[{gpu}].RDMA", "outgoing_trans_count"] > 0,
                              f"{name}/GPU{gpu}: remote memory did not go through RDMA")
        check.require(not any("L3TLB" in loc for loc, _ in metrics), f"{name}: unexpected L3 component")
        summaries.append(summary)
        print(f"PASS {name}", flush=True)
    check.main(output)

    # Exercise the retained upstream timing path and functional emulator too.
    for name, command in [
        ("sc-shared", [str(output / "simpleconvolution"), *common, "-vm-mode=shared", "-gpus=1,2,3,4",
                       "-width=62", "-height=62", "-metric-file-name=" + str(output / "sc-shared")]),
        ("fir-functional", [str(output / "fir"), "-verify", "-disable-rtm", "-gpus=1,2",
                            "-use-unified-memory", "-length=1024",
                            "-metric-file-name=" + str(output / "fir-functional")]),
    ]:
        check.require("Passed!" in run(command, name), f"{name}: verification failed")
        print(f"PASS {name}", flush=True)

    # Removed modes must fail explicitly instead of silently using another model.
    removed = {
        "removed-ideal": (["-timing", "-vm-mode=ideal"], "unknown -vm-mode"),
        "removed-l3": (["-timing", "-vm-mode=demand-l3"], "unknown -vm-mode"),
        "removed-local": (["-timing", "-ideal-local-page-table"], "flag provided but not defined"),
        "removed-profile": (["-timing", "-tlb-profile=libra-capacity"], "flag provided but not defined"),
        "removed-l3-flag": (["-timing", "-l3-tlb-entries=1024"], "flag provided but not defined"),
    }
    for name, (flags, message) in removed.items():
        text = run([str(output / "fir"), *flags], name, expected=1)
        check.require(message in text, f"{name}: rejected for the wrong reason")
    report = {"status": "PASS", "timing_cases": summaries,
              "compatibility_cases": ["sc-shared", "fir-functional"],
              "removed_options_rejected": list(removed), "source": str(source)}
    (output / "validation.json").write_text(json.dumps(report, indent=2) + "\n")
    (output.parent / "latest-validation.txt").write_text(str(output) + "\n")
    print("PASS: all mapping-fault, RDMA, numerical, compatibility and removed-option checks.", flush=True)


if __name__ == "__main__":
    main()
