#!/usr/bin/env python3
"""Bounded live acceptance in Saved Messages; stdout contains labels only."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import tempfile
import uuid


class ProbeFailure(Exception):
    pass


def require(condition, label):
    if not condition:
        raise ProbeFailure(label)


def report(label):
    print(json.dumps({'check': label, 'result': 'passed'}), flush=True)


class Probe:
    def __init__(self, binary, account, timeout=45):
        require(bool(re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9_-]{0,63}', account)), 'invalid account name')
        require(Path(binary).is_absolute() and os.access(binary, os.X_OK), 'binary must be an absolute executable path')
        self.base = [binary, '--account', account, '--lock-wait', '5']
        self.timeout = timeout
        self.created = []
        self.self_id = None
        self.token = 'tgctl-acceptance-' + uuid.uuid4().hex

    def call(self, label, args, remember=False, expected=None):
        process = subprocess.Popen(self.base + args + ['--json'], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        try:
            output, _ = process.communicate(timeout=self.timeout)
        except (subprocess.TimeoutExpired, KeyboardInterrupt):
            process.send_signal(signal.SIGINT)
            try:
                process.communicate(timeout=15)
            except subprocess.TimeoutExpired:
                process.kill()
                process.communicate()
            raise ProbeFailure(label + ': timeout; inspect the write ledger before retrying') from None
        try:
            result = json.loads(output)
        except (ValueError, TypeError):
            raise ProbeFailure(label + ': invalid JSON response') from None
        require(isinstance(result, dict), label + ': invalid result')
        if expected:
            require(process.returncode != 0 and result.get('error', {}).get('code') == expected, label + ': expected rejection missing')
            return result
        if process.returncode or not result.get('ok'):
            code = result.get('error', {}).get('code', '')
            if not re.fullmatch(r'[A-Z_]{1,48}', code):
                code = 'UNEXPECTED_ERROR'
            raise ProbeFailure(label + ': ' + code)
        data = result.get('data', {})
        require(isinstance(data, dict), label + ': invalid response data')
        if remember:
            ids = data.get('message_ids', data.get('new_message_ids', [data.get('message_id')]))
            require(isinstance(ids, list) and ids and all(isinstance(x, int) and not isinstance(x, bool) and x > 0 for x in ids), label + ': no confirmed message IDs; inspect the write ledger')
            self.created.extend(x for x in ids if x not in self.created)
        return data

    def identity(self):
        cached = self.call('cached identity', ['me', '--offline', '--read-only'])
        live = self.call('live identity', ['me', '--read-only'])
        require(isinstance(live.get('user_id'), int) and live['user_id'] > 0 and cached.get('user_id') == live['user_id'], 'account identity mismatch')
        resolved = self.call('Saved Messages selection', ['resolve', 'self', '--source', 'telegram', '--read-only'])
        require(resolved['peer']['chat_id'] == live['user_id'], 'Saved Messages did not resolve to this account')
        self.self_id = live['user_id']
        report('matching cached/live identity and Saved Messages target')

    def get(self, mid):
        return self.call('retrieve probe', ['get-msg', 'self', str(mid), '--source', 'telegram', '--read-only'])['message']

    def write(self, label, args):
        require(self.self_id is not None, 'identity must be verified before writes')
        return self.call(label, args + ['--allow-write', '--idempotency-key', self.token + '-' + label], remember=True)

    def cleanup(self):
        if not self.created:
            return
        require(self.self_id is not None, 'cannot confirm cleanup without verified identity')
        ids = list(self.created)
        self.call('delete owned probes', ['delete-msg', 'self', ','.join(map(str, ids)), '--for-everyone', '--confirm', str(self.self_id), '--allow-write'])
        for mid in ids:
            require(self.get(mid).get('deleted') is True, 'cleanup did not confirm deletion')
        self.created.clear()
        report('all test-created messages deleted and deletion verified')

    def reads(self):
        self.identity()
        self.call('account capabilities', ['account-limits', '--read-only'])
        for command in ['doctor', 'stats', 'accounts-list', 'accounts-show', 'operations-list']:
            self.call(command, [command, '--read-only'])
        history = self.call('Saved Messages history', ['list-msgs', 'self', '--source', 'telegram', '--limit', '2', '--read-only'])
        require(history.get('source') == 'telegram' and len(history['messages']) <= 2, 'remote history bounds/source')
        self.call('write gate', ['send', 'self', self.token], expected='WRITE_DISALLOWED')
        self.call('read-only override', ['send', 'self', self.token, '--allow-write', '--read-only'], expected='WRITE_DISALLOWED')
        preview = self.call('send preview', ['send', 'self', self.token, '--allow-write', '--dry-run'])
        require(preview.get('dry_run') is True, 'send preview did not report dry-run')
        report('bounded reads, account diagnostics, and write gates')

    def text(self):
        body = self.token + ' text'
        args = ['send', 'self', body]
        sent = self.write('text', args)
        replay = self.call('idempotent replay', args + ['--allow-write', '--idempotency-key', self.token + '-text'])
        require(replay.get('idempotent_replay') is True and replay['message_id'] == sent['message_id'], 'idempotent replay changed the message')
        mid = sent['message_id']
        require(self.get(mid)['text'] == body, 'sent text mismatch')
        self.call('edit own probe', ['edit-msg', 'self', str(mid), body + ' edited', '--allow-write'])
        require(self.get(mid)['text'] == body + ' edited', 'edited text mismatch')
        reply = self.write('reply', ['send', 'self', body + ' reply', '--reply-to', str(mid)])
        require(self.get(reply['message_id'])['reply_to_msg_id'] == mid, 'reply root mismatch')
        forwarded = self.write('forward', ['forward', 'self', 'self', str(mid)])
        require(self.get(forwarded['new_message_ids'][0])['text'] == body + ' edited', 'forwarded content mismatch')
        found = self.call('search probes', ['search', 'self', self.token, '--source', 'telegram', '--limit', '20', '--read-only'])
        require({mid, reply['message_id']}.issubset({m['message_id'] for m in found['messages']}), 'remote search omitted controlled probes')
        self.call('pin probe', ['pin-msg', 'self', str(mid), '--allow-write'])
        pins = self.call('verify pin', ['chat-pinned-list', 'self', '--read-only'])
        require(any(m['message_id'] == mid for m in pins['pinned']), 'probe pin missing')
        self.call('unpin probe', ['unpin-msg', 'self', str(mid), '--allow-write'])
        pins = self.call('verify unpin', ['chat-pinned-list', 'self', '--read-only'])
        require(all(m['message_id'] != mid for m in pins['pinned']), 'probe pin retained')
        report('text, edit, reply, forward, search, replay, and pin/unpin state')

    def media(self, directory, ffmpeg):
        root = Path(directory)
        def generate(name, args):
            path = root / name
            result = subprocess.run([ffmpeg, '-nostdin', '-hide_banner', '-loglevel', 'error'] + args + [str(path)], capture_output=True, timeout=30)
            require(result.returncode == 0 and path.is_file() and path.stat().st_size > 0, 'synthetic media generation failed')
            return path
        photo = generate('photo.jpg', ['-f', 'lavfi', '-i', 'testsrc2=size=320x240:rate=1', '-frames:v', '1'])
        video = generate('video.mp4', ['-f', 'lavfi', '-i', 'testsrc2=size=320x240:rate=24', '-f', 'lavfi', '-i', 'sine=frequency=440', '-t', '2', '-c:v', 'libx264', '-pix_fmt', 'yuv420p', '-c:a', 'aac', '-movflags', '+faststart'])
        voice = generate('voice.ogg', ['-f', 'lavfi', '-i', 'sine=frequency=440', '-t', '2', '-c:a', 'libopus', '-ac', '1'])
        audio = [generate('audio' + str(n) + '.mp3', ['-f', 'lavfi', '-i', 'sine=frequency=' + str(hz), '-t', '2', '-c:a', 'libmp3lame']) for n, hz in enumerate([440, 660])]
        docs = [root / ('document' + str(n) + '.txt') for n in range(2)]
        for n, path in enumerate(docs):
            path.write_text(self.token + ' document ' + str(n))
        for kind, path in [('photo', photo), ('video', video), ('voice', voice), ('document', docs[0])]:
            sent = self.write(kind, ['upload-' + kind, 'self', str(path), '--caption', self.token, '--max-size-mb', '5'])
            message = self.get(sent['message_id'])
            require(message['has_media'] and message['media_type'] == kind, kind + ' media type mismatch')
            require(sent.get('hash_indexed') is True, kind + ' original hash missing')
            downloaded = self.call('download ' + kind, ['download-media', 'self', str(sent['message_id']), '--output', str(root / ('download-' + kind)), '--max-size-mb', '5', '--allow-write'])
            artifact = Path(downloaded['media_path'])
            require(artifact.is_file() and artifact.stat().st_size > 0, kind + ' download missing')
            if kind != 'photo':
                require(hashlib.sha256(artifact.read_bytes()).digest() == hashlib.sha256(path.read_bytes()).digest(), kind + ' round-trip bytes changed')
        report('valid photo/video/voice/document uploads and downloaded bytes')
        for kind, files in [('auto', [photo, video]), ('audio', audio), ('document', docs)]:
            sent = self.write('album-' + kind, ['upload-album', 'self'] + list(map(str, files)) + ['--media-kind', kind, '--caption', self.token, '--max-size-mb', '5'])
            ids = sent['message_ids']
            require(len(ids) == 2 and ids[0] != ids[1] and sent.get('grouped_id'), 'album IDs/grouping missing')
            messages = [self.get(mid) for mid in ids]
            require(all(m['grouped_id'] == sent['grouped_id'] for m in messages), 'album grouping mismatch')
            require(messages[0]['text'] == self.token and not messages[1]['text'], 'album caption placement mismatch')
        report('photo/video, audio-only, and document-only album grouping/captions')


def main():
    def interrupt(_signum, _frame):
        raise KeyboardInterrupt
    signal.signal(signal.SIGTERM, interrupt)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', required=True)
    parser.add_argument('--account', required=True)
    parser.add_argument('--writes', action='store_true', help='Create and clean only this run’s Saved Messages probes')
    parser.add_argument('--media', action='store_true', help='Also test small valid media; requires ffmpeg and --writes')
    args = parser.parse_args()
    probe = None
    failed = False
    try:
        require(not args.media or args.writes, '--media requires --writes')
        ffmpeg = shutil.which('ffmpeg') if args.media else None
        require(not args.media or ffmpeg is not None, 'ffmpeg is required for media probes')
        probe = Probe(args.binary, args.account)
        probe.reads()
        if args.writes:
            probe.text()
        if args.media:
            with tempfile.TemporaryDirectory(prefix='tgctl-acceptance-') as directory:
                probe.media(directory, ffmpeg)
    except (ProbeFailure, KeyError, TypeError, ValueError, OSError, subprocess.SubprocessError, KeyboardInterrupt) as error:
        failed = True
        reason = str(error) if isinstance(error, ProbeFailure) else 'probe interrupted or unexpected response; inspect locally before retrying'
        print(json.dumps({'result': 'failed', 'reason': reason}), flush=True)
    finally:
        if probe is not None:
            try:
                probe.cleanup()
            except Exception:
                failed = True
                print(json.dumps({'cleanup': 'failed', 'reason': 'test messages may remain; inspect the write ledger locally before retrying'}), flush=True)
    if not failed:
        report('selected live acceptance scope complete')
    return int(failed)


if __name__ == '__main__':
    raise SystemExit(main())
