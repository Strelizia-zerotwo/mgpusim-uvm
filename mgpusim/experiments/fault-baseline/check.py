"""Check correctness, conservation, finite resources, and real host contention."""
import json
import math
import sqlite3
import sys
from pathlib import Path


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def read_case(directory, name):
    require("Passed!" in (directory / f"{name}.log").read_text(), f"{name}: failed FIR verification")
    uri = (directory / f"{name}.sqlite3").resolve().as_uri() + "?mode=ro"
    with sqlite3.connect(uri, uri=True) as conn:
        rows = conn.execute("SELECT Location, What, Value FROM mgpusim_metrics").fetchall()
    return {(loc, what): value for loc, what, value in rows}


def check_case(name, metrics, gpu_ids=(1, 2)):
    get = lambda loc, key: metrics[loc, "vm_" + key]
    gpus = sorted(loc for loc, key in metrics if key == "vm_local_faults")
    require(gpus == [f"GPU[{gpu}].MMU" for gpu in gpu_ids], f"{name}: incorrect MMU topology")
    faults = 0
    for gpu in gpus:
        require(get(gpu, "requests") == get(gpu, "completed"), f"{name}/{gpu}: missing responses")
        require(get(gpu, "local_walks") > 0, f"{name}/{gpu}: no page walks")
        require(get(gpu, "local_walks") == get(gpu, "local_hits") + get(gpu, "local_faults"), "walk accounting")
        require(get(gpu, "requests") == get(gpu, "local_walks") + get(gpu, "coalesced"), "coalescing accounting")
        require(get(gpu, "outstanding_end") == 0, f"{name}/{gpu}: pending transactions at exit")
        require(get(gpu, "fault_requests") == get(gpu, "fault_resolved") == get(gpu, "local_faults"), "fault accounting")
        require(get(gpu, "outstanding_peak") <= get(gpu, "transaction_slots"), "GPU slot overflow")
        require(get(gpu, "local_faults") > 0, f"{name}/{gpu}: baseline did not exercise faults")
        minimum = (2*get(gpu, "link_cycles") + get(gpu, "install_cycles") + get("HostMMU", "service_cycles"))*1e-9
        require(get(gpu, "fault_average") >= minimum, f"{name}: fault delay bypassed")
        require(metrics[gpu.removesuffix(".MMU") + ".CommandProcessor", "kernel_time"] > 0,
                f"{name}/{gpu}: GPU did not execute a kernel")
        prefix = gpu.removesuffix(".MMU") + "."
        misses = sum(v for (loc, key), v in metrics.items() if loc.startswith(prefix) and "TLB" in loc and key == "miss")
        require(misses > 0, f"{name}/{gpu}: TLB misses disappeared")
        faults += get(gpu, "local_faults")
    require(get("HostMMU", "received") == get("HostMMU", "completed") == faults, "host request conservation")
    require(get("HostMMU", "queue_end") == get("HostMMU", "active_end") == 0, "host pending work")
    require(get("HostMMU", "queue_peak") <= get("HostMMU", "queue_capacity"), "host queue overflow")
    require(get("HostMMU", "active_peak") <= get("HostMMU", "walkers"), "host walker overflow")
    if faults:
        require(math.isclose(get("HostMMU", "service_average"), get("HostMMU", "service_cycles")*1e-9,
                             rel_tol=1e-10), "incorrect host service timing")
    result = {"case": name, "kernel_us": metrics["Driver", "kernel_time"]*1e6,
              "faults": int(faults), "host_queue_average_ns": get("HostMMU", "queue_wait_average")*1e9,
              "host_queue_peak": int(get("HostMMU", "queue_peak")),
              "host_active_peak": int(get("HostMMU", "active_peak")),
              "host_queue_full_cycles": int(get("HostMMU", "queue_full_cycles"))}
    print(json.dumps(result, ensure_ascii=False))
    return result


def main(directory):
    cases = {name: read_case(directory, name) for name in ("demand", "congested")}
    results = [check_case(name, data) for name, data in cases.items()]
    require(results[0]["faults"] == results[1]["faults"], "resource pressure changed mapping faults")
    pressure = cases["congested"]
    require(pressure["HostMMU", "vm_queue_wait_average"] > 0, "congestion test never queued")
    require(pressure["HostMMU", "vm_queue_full_cycles"] > 0, "congestion test never applied backpressure")
    require(pressure["Driver", "kernel_time"] > cases["demand"]["Driver", "kernel_time"], "no end-to-end congestion impact")
    (directory / "summary.json").write_text(json.dumps(results, ensure_ascii=False, indent=2)+"\n")
    print("PASS: correct FIR output, conserved requests, finite resource limits, and host contention exercised.")
    print("These are fixed-placement mapping faults under configurable timing assumptions, not calibrated UVM hardware.")


if __name__ == "__main__":
    main(Path(sys.argv[1]))
