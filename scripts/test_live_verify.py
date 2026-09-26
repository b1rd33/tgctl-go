import contextlib
import io
import json
import subprocess
import unittest
from unittest.mock import patch

from live_verify import Probe, ProbeFailure


class ProbeTests(unittest.TestCase):
    def probe(self):
        with patch('live_verify.os.access', return_value=True):
            return Probe('/synthetic/tg', 'synthetic-test')

    def test_bad_account_fails_before_process(self):
        with patch('live_verify.subprocess.Popen') as popen:
            with self.assertRaises(ProbeFailure):
                Probe('/synthetic/tg', '../other-account')
            popen.assert_not_called()

    def test_write_requires_verified_identity(self):
        probe = self.probe()
        with patch('live_verify.subprocess.Popen') as popen:
            with self.assertRaises(ProbeFailure):
                probe.write('text', ['send', 'self', 'synthetic'])
            popen.assert_not_called()

    def test_private_errors_and_outputs_are_not_printed(self):
        probe = self.probe()
        output = io.StringIO()
        with patch('live_verify.subprocess.Popen') as popen, contextlib.redirect_stdout(output):
            process = popen.return_value
            process.returncode = 1
            process.communicate.return_value = (json.dumps({'ok': False, 'error': {'code': 'FLOOD_WAIT', 'message': 'private-response-marker'}}), 'private-stderr-marker')
            with self.assertRaises(ProbeFailure) as error:
                probe.call('test label', ['send', 'self', 'private-input-marker'], remember=True)
            self.assertEqual(str(error.exception), 'test label: FLOOD_WAIT')
            self.assertEqual(output.getvalue(), '')
            self.assertEqual(probe.created, [])
            self.assertEqual(popen.call_args.args[0][:5], ['/synthetic/tg', '--account', 'synthetic-test', '--lock-wait', '5'])

    def test_cleanup_only_deletes_confirmed_ids_and_checks_remote_state(self):
        probe = self.probe()
        probe.self_id = 71
        probe.created = [11, 12]
        with patch.object(probe, 'call') as call, patch.object(probe, 'get', return_value={'deleted': True}) as get:
            with contextlib.redirect_stdout(io.StringIO()):
                probe.cleanup()
            args = call.call_args.args[1]
            self.assertEqual(args, ['delete-msg', 'self', '11,12', '--for-everyone', '--confirm', '71', '--allow-write'])
            self.assertEqual([c.args[0] for c in get.call_args_list], [11, 12])
            self.assertEqual(probe.created, [])

    def test_cleanup_does_not_pass_on_failed_deletion_assertion(self):
        probe = self.probe()
        probe.self_id = 71
        probe.created = [11]
        with patch.object(probe, 'call'), patch.object(probe, 'get', return_value={'deleted': False}):
            with self.assertRaises(ProbeFailure):
                probe.cleanup()
            self.assertEqual(probe.created, [11])

    def test_timeout_stops_instead_of_retrying_unknown_write(self):
        probe = self.probe()
        with patch('live_verify.subprocess.Popen') as popen:
            process = popen.return_value
            process.communicate.side_effect = [subprocess.TimeoutExpired('synthetic', 45), ('', '')]
            with self.assertRaises(ProbeFailure):
                probe.call('send probe', ['send', 'self', 'synthetic'], remember=True)
            self.assertEqual(popen.call_count, 1)
            process.send_signal.assert_called_once()
            self.assertEqual(probe.created, [])

    def test_interrupt_reaps_process_before_cleanup(self):
        probe = self.probe()
        probe.created = [11]
        with patch('live_verify.subprocess.Popen') as popen:
            process = popen.return_value
            process.communicate.side_effect = [KeyboardInterrupt, subprocess.TimeoutExpired('synthetic', 15), ('', '')]
            with self.assertRaises(ProbeFailure):
                probe.call('send probe', ['send', 'self', 'synthetic'], remember=True)
            process.send_signal.assert_called_once()
            process.kill.assert_called_once()
            self.assertEqual(process.communicate.call_count, 3)
            self.assertEqual(probe.created, [11])

    def test_bad_confirmed_ids_cannot_reach_cleanup(self):
        probe = self.probe()
        with patch('live_verify.subprocess.Popen') as popen:
            process = popen.return_value
            process.returncode = 0
            process.communicate.return_value = ('{"ok":true,"data":{"message_ids":[true,-12]}}', '')
            with self.assertRaises(ProbeFailure):
                probe.call('album', ['upload-album'], remember=True)
            self.assertEqual(probe.created, [])


if __name__ == '__main__':
    unittest.main()
