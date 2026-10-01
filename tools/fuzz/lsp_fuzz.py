#!/usr/bin/env python3
"""Differential fuzzing of the language server.

Random LSP sessions (document lifecycle, definition, completion and compile
requests on mutated domains, plus malformed JSON-RPC traffic) are fed to the
original HTNLanguageServer and to one or more port servers. The exit code
and the raw stdout and stderr bytes must be identical.

  python3 tools/fuzz/lsp_fuzz.py --oracle build/oracle/HTNLanguageServer \
      --port go=path/to/htn-lsp --iterations 500 --seed 1
"""
import argparse
import json
import os
import random
import re
import shutil
import subprocess
import sys
import tempfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from frontend_fuzz import mutate  # noqa: E402

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
CORPUS_DIRECTORIES = ['Domains', 'HTNDiagnosticTests', os.path.join('testdata', 'translator', 'cases')]
IDENTIFIER = re.compile(r'[#@&?:]?[A-Za-z_][A-Za-z0-9_:\-]*')
DECLARATION = re.compile(r'\(:(?:method|axiom)\s+\(([^\s()]+)')
UNICODE_NOISE = ['é', '😀', 'ß', '中', ' ', '\t', '\r\n', '\x01']
BAD_JSON = [
    '', ' ', '{', '}', '[', '{"method"', '{"method":}', '{"method":"x",}', '[1,]', '{"a" 1}', '{"a":1 "b":2}',
    'tru', 'nul', 'falsy', '"unterminated', '"bad \\q escape"', '"\\u12"', '"\\u12G4"', '01', '-', '-a', '1.',
    '1e', '1e+', '1e400', '-1e400', '1e-400', '9223372036854775808', '-9223372036854775809', '{"id":1} x',
    '{"method":"initialize","id":1}{"x":1}', '\x00', '{"method":"initialize","id":1e400}',
]


def utf16_position(text, offset):
    line = text.count('\n', 0, offset)
    start = text.rfind('\n', 0, offset) + 1
    return line, len(text[start:offset].encode('utf-16-le', 'surrogatepass')) // 2


class Session:
    def __init__(self, rng, documents):
        self.rng = rng
        self.documents = documents  # list of (uri, initial text)
        self.texts = {}
        self.payloads = []
        self.next_id = 1

    def new_id(self):
        rng = self.rng
        self.next_id += 1
        choice = rng.randrange(20)
        if choice == 0:
            return None
        if choice == 1:
            return 'request-%d' % self.next_id
        if choice == 2:
            return rng.choice([1.5, 0.1, 1e21, -0.0, 3e-7, 2.0])
        if choice == 3:
            return rng.choice([[1, 'a'], {'nested': True}, -5, 0, 9223372036854775807])
        return self.next_id

    def send(self, value):
        rng = self.rng
        text = json.dumps(value, ensure_ascii=rng.random() < 0.3,
                          separators=rng.choice([(',', ':'), (', ', ': ')]))
        self.payloads.append(text.encode('utf-8', 'surrogatepass'))

    def send_raw(self, text):
        self.payloads.append(text.encode('utf-8', 'surrogatepass') if isinstance(text, str) else text)

    def request(self, method, params=None, has_params=True):
        message = {'jsonrpc': '2.0', 'id': self.new_id(), 'method': method}
        if has_params:
            message['params'] = params
        self.send(message)

    def notify(self, method, params=None, has_params=True):
        message = {'jsonrpc': '2.0', 'method': method}
        if has_params:
            message['params'] = params
        self.send(message)

    def mutated_text(self, uri, base):
        rng = self.rng
        text = self.texts.get(uri, base)
        choice = rng.randrange(20)
        if choice < 12:
            return text
        if choice < 18:
            return mutate(text, rng)
        if choice == 18 and text:
            i = rng.randrange(len(text) + 1)
            return text[:i] + rng.choice(UNICODE_NOISE) + text[i:]
        return base

    def version(self):
        rng = self.rng
        choice = rng.randrange(12)
        if choice == 0:
            return 'v2'
        if choice == 1:
            return -3
        if choice == 2:
            return 2.5
        if choice == 3:
            return None
        return rng.randrange(1, 100)

    def position(self, uri):
        rng = self.rng
        text = self.texts.get(uri, '')
        choice = rng.randrange(14)
        if choice == 0:
            return {'line': rng.randrange(-2, 400), 'character': rng.randrange(-2, 200)}
        if choice == 1:
            return rng.choice([{'line': '1', 'character': 0}, {'line': 1.0, 'character': 2}, {'line': 1},
                               {'character': 3}, {'line': 4294967297, 'character': 1}, None, [1, 2]])
        matches = list(IDENTIFIER.finditer(text))
        if not matches:
            return {'line': 0, 'character': 0}
        declared = set(DECLARATION.findall(text))
        references = [m for m in matches if m.group().lstrip('#@&').split('::')[-1] in declared or m.group()[0] in '#@']
        if references and rng.random() < 0.7:
            matches = references
        match = rng.choice(matches)
        offset = rng.randint(match.start(), match.end())
        line, character = utf16_position(text, offset)
        return {'line': line, 'character': character}

    def pick(self):
        return self.rng.choice(self.documents)

    def operation(self):
        rng = self.rng
        uri, base = self.pick()
        op = rng.randrange(100)
        if op < 14:
            text = self.mutated_text(uri, base)
            document = {'uri': uri, 'languageId': 'htn', 'version': self.version(), 'text': text}
            if rng.random() < 0.05:
                del document[rng.choice(['uri', 'text', 'version'])]
            elif rng.random() < 0.03:
                document[rng.choice(['uri', 'text'])] = rng.choice([7, None, ['x']])
            if isinstance(document.get('text'), str) and isinstance(document.get('uri'), str):
                self.texts[uri] = text
            self.notify('textDocument/didOpen', {'textDocument': document})
        elif op < 28:
            text = self.mutated_text(uri, base)
            params = {'textDocument': {'uri': uri, 'version': self.version()}, 'contentChanges': [{'text': text}]}
            variant = rng.randrange(25)
            if variant == 0:
                params['contentChanges'] = []
            elif variant == 1:
                params['contentChanges'] = [{'range': {}}]
            elif variant == 2:
                params['contentChanges'] = {'text': text}
            elif variant == 3:
                params['contentChanges'].append({'text': 'ignored'})
            elif variant == 4:
                del params['textDocument']
            if variant in (3, 5, 6) or variant > 4:
                self.texts[uri] = text
            self.notify('textDocument/didChange', params)
        elif op < 33:
            self.texts.pop(uri, None)
            self.notify('textDocument/didClose', {'textDocument': {'uri': uri}})
        elif op < 58:
            method = rng.choice(['textDocument/definition', 'textDocument/completion'])
            params = {'textDocument': {'uri': uri}}
            position = self.position(uri)
            if position is not None:
                params['position'] = position
            if rng.random() < 0.03:
                self.request(method, has_params=False)
            elif rng.random() < 0.03:
                self.request(method, rng.choice([None, [], 'x', {'textDocument': {}}]))
            else:
                self.request(method, params)
        elif op < 70:
            if rng.random() < 0.05:
                self.request('htn/compile', rng.choice([None, {}, {'textDocument': {'uri': 3}}]),
                             has_params=rng.random() < 0.5)
            else:
                self.request('htn/compile', {'textDocument': {'uri': uri}})
        elif op < 74:
            self.request(rng.choice(['textDocument/hover', 'workspace/symbol', '$/cancelRequest', '']), {})
        elif op < 77:
            self.notify(rng.choice(['$/setTrace', 'workspace/didChangeConfiguration', 'initialized']), {})
        elif op < 85:
            self.send_raw(rng.choice(BAD_JSON))
        elif op < 88:
            self.send_raw(rng.choice([
                '{"method":"initialize","id":1,"id":2}',
                '{"method":"shutdown","method":"x","id":3}',
                '{"method":7,"id":4}', '{"id":5}', '[{"method":"initialize","id":1}]', '"initialize"', '42',
                '{"method":"initialize","id":"\\u00e9\\ud83d\\ude00\\u0001"}',
                '{"method":"initialize","id":"tab\\tquote\\"slash\\/back\\\\"}',
                '\t{"method" : "initialize" ,\n"id" :\r 6 }\x0b\x0c',
                '{"method":"textDocument/definition","id":8,"params":{"textDocument":{"uri":"%s"},'
                '"position":{"line":-0,"character":01}}}' % uri.replace('"', '\\"'),
            ]))
        elif op < 92:
            self.request('initialize', {'capabilities': {}})
        elif op < 95:
            # Text with raw control characters and escapes in the document.
            text = self.texts.get(uri, base) + rng.choice(['\x01', '\x1f', '\x7f', '"', '\\', '\b\f'])
            self.texts[uri] = text
            self.notify('textDocument/didOpen', {'textDocument': {'uri': uri, 'version': 1, 'text': text}})
        else:
            # Completion and definition right after a fresh open of an included document.
            self.texts[uri] = base
            self.notify('textDocument/didOpen', {'textDocument': {'uri': uri, 'version': 1, 'text': base}})
            self.request('textDocument/definition', {'textDocument': {'uri': uri}, 'position': self.position(uri)})

    def build(self):
        rng = self.rng
        if rng.random() < 0.9:
            self.request('initialize', {'processId': None, 'capabilities': {}})
            self.notify('initialized', {})
        for _ in range(rng.randint(3, 30)):
            self.operation()
        ending = rng.randrange(10)
        if ending < 7:
            self.request('shutdown', has_params=False)
            self.notify('exit', has_params=False)
        elif ending == 7:
            self.notify('exit', has_params=False)
        stream = bytearray()
        for payload in self.payloads:
            header = b'Content-Length: %d\r\n\r\n' % len(payload)
            variant = rng.randrange(40)
            if variant == 0:
                header = b'content-length:\t%d\r\nContent-Type: application/vscode-jsonrpc; charset=utf-8\r\n\r\n' % len(payload)
            elif variant == 1:
                header = b'CONTENT-LENGTH:  %d\n\n' % len(payload)
            stream += header + payload
        if rng.random() < 0.05:
            stream += rng.choice([b'Content-Length: x\r\n\r\n{}', b'Content-Length:\r\n\r\n', b'Content-Length: 9\r\n\r\n{}',
                                  b'X-Header: 1\r\n\r\n{}', b'Content-Length: 2\r\r\n\r\n{}'])
        return bytes(stream)


def prepare_workdir(workdir):
    for directory in CORPUS_DIRECTORIES:
        shutil.copytree(os.path.join(ROOT, directory), os.path.join(workdir, directory))
    os.symlink(os.path.join(workdir, 'Domains'), os.path.join(workdir, 'linked'))
    shutil.copytree(os.path.join(ROOT, 'Domains', 'Test'), os.path.join(workdir, 'with space%25 é'))
    documents = []
    for dirpath, _, names in os.walk(workdir):
        for name in sorted(names):
            if name.endswith('.domain'):
                documents.append(os.path.join(dirpath, name))
    return sorted(documents)


def uri_for(path, rng):
    encoded = ''.join(c if c.isascii() and (c.isalnum() or c in '-_.~/:') else
                      ''.join('%%%02X' % b for b in c.encode('utf-8')) for c in path)
    choice = rng.randrange(20)
    if choice == 0:
        return path  # Not a file:// URI: used as a path verbatim.
    if choice == 1:
        return 'file://' + path.replace('/Test/', '/Test/./', 1)
    if choice == 2:
        return 'file://' + encoded.lower() if '%' in encoded else 'file://' + encoded
    return 'file://' + encoded


def run(binary, data, cwd):
    result = subprocess.run([binary], input=data, cwd=cwd, capture_output=True, timeout=120)
    return result.returncode, result.stdout, result.stderr


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--oracle', required=True)
    parser.add_argument('--port', action='append', default=[], help='name=path of a port language server')
    parser.add_argument('--iterations', type=int, default=200)
    parser.add_argument('--seed', type=int, default=1)
    parser.add_argument('--keep', help='directory for failing sessions')
    args = parser.parse_args()
    ports = [entry.split('=', 1) for entry in args.port]
    workdir = tempfile.mkdtemp(prefix='htn-lsp-fuzz-')
    try:
        paths = prepare_workdir(workdir)
        failures = 0
        oracle_crashes = 0
        for iteration in range(args.iterations):
            rng = random.Random(args.seed * 1000003 + iteration)
            pool = paths if rng.random() < 0.3 else [p for p in paths if '/Domains/' in p or '/with space' in p]
            chosen = rng.sample(pool, rng.randint(1, 3))
            if rng.random() < 0.3:
                chosen.append(os.path.join(os.path.dirname(chosen[0]), 'unsaved_new.domain'))
            if rng.random() < 0.1:
                chosen.append(os.path.join(workdir, 'linked', 'Test', 'human.domain'))
            documents = []
            for path in chosen:
                try:
                    with open(path, encoding='utf-8', errors='surrogateescape') as f:
                        text = f.read()
                except FileNotFoundError:
                    text = '(:domain New top_level_domain (:method (run) top_level_method (b () ((!act)))))'
                documents.append((uri_for(path, rng), text))
            data = Session(rng, documents).build()
            expected = run(args.oracle, data, workdir)
            if expected[0] < 0:
                # The original aborts on some inputs (an include that names a
                # directory throws std::ios_base::failure from its file
                # reader); the ports deliberately do not reproduce crashes.
                oracle_crashes += 1
                reason = expected[2].decode('utf-8', 'replace').strip().splitlines()[-1:]
                print(f'oracle crashed iteration={iteration}: {reason}', flush=True)
                continue
            for name, binary in ports:
                actual = run(binary, data, workdir)
                if actual != expected:
                    failures += 1
                    print(f'MISMATCH iteration={iteration} port={name}', flush=True)
                    if args.keep:
                        os.makedirs(args.keep, exist_ok=True)
                        base = os.path.join(args.keep, f'{iteration}-{name}')
                        with open(base + '.in', 'wb') as f:
                            f.write(data)
                        for suffix, value in (('.expected', expected), ('.actual', actual)):
                            with open(base + suffix, 'wb') as f:
                                f.write(b'exit %d\n--- stdout\n' % value[0] + value[1] + b'\n--- stderr\n' + value[2])
        print(f'{args.iterations} sessions, {failures} mismatches, {oracle_crashes} skipped (oracle crashed)')
        return 1 if failures else 0
    finally:
        shutil.rmtree(workdir, ignore_errors=True)


if __name__ == '__main__':
    sys.exit(main())
