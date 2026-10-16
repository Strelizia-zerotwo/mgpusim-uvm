import unittest
from screen_plan import analyze


def access(gpu, reads=(), writes=()):
    return dict(page=4096, gpu=gpu, reads=list(reads), writes=list(writes),
                read_transactions=bool(reads), write_transactions=bool(writes))


class ScreeningTest(unittest.TestCase):
    def test_dirty_fraction_requires_actual_reread_of_a_previous_replica(self):
        epochs = [[access(2, [0, 1])], [access(1, writes=[0])], [access(1, writes=[1])], [access(2, [1])]]
        plan = dict(page_bytes=4096, sector_bytes=32, epochs=[dict(accesses=a) for a in epochs])
        out = analyze(plan, [dict(page=4096, home_gpu=1)])
        self.assertEqual(out["replica_refills"], 1)
        self.assertEqual(out["all_stale_refill_bytes"], 64)
        self.assertEqual(out["required_stale_refill_bytes"], 32)
        self.assertEqual(out["invalidation_events"], 1)

    def test_sparse_writes_without_reread_do_not_pass_the_screen(self):
        plan = dict(page_bytes=4096, sector_bytes=32, epochs=[dict(accesses=[access(2, [0])]),
                     dict(accesses=[access(1, writes=[0])])])
        out = analyze(plan, [dict(page=4096, home_gpu=1)])
        self.assertEqual(out["replica_refills"], 0)
        self.assertIn("no S1 evidence", out["screen"])
