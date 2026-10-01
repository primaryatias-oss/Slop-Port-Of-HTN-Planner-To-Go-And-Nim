#!/usr/bin/env python3
"""Writes the language server golden sessions (testdata/lsp).

Every <name>.session holds the JSON-RPC payloads a client sends, one record
per "--- message" line ("--- message lowercase" frames it with a lowercase
header and an extra Content-Type header). $ROOT stands for the repository
root and $ROOT_URI for its file:// URI. <name>.golden holds the payloads the
ORIGINAL HTNLanguageServer answers (one per "--- message" record), its stderr
and its exit code, recorded with the server running in the repository root:

  python3 tools/lsp/make_goldens.py --oracle build/oracle/HTNLanguageServer
"""
import argparse
import json
import os
import re
import subprocess

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
LSP = os.path.join(ROOT, 'testdata', 'lsp')
WORKSPACE = '$ROOT/testdata/lsp/workspace'
WORKSPACE_URI = '$ROOT_URI/testdata/lsp/workspace'


def percent_encode_path(path):
    return ''.join(chr(b) if chr(b).isascii() and (chr(b).isalnum() or chr(b) in '-_.~/:') else '%%%02X' % b
                   for b in path.encode('utf-8'))


def position(text, needle, delta=0, occurrence=0):
    """LSP position of the first character of the occurrence-th needle."""
    offset = -1
    for _ in range(occurrence + 1):
        offset = text.index(needle, offset + 1)
    offset += delta
    line = text.count('\n', 0, offset)
    start = text.rfind('\n', 0, offset) + 1
    return {'line': line, 'character': len(text[start:offset].encode('utf-16-le')) // 2}


class Session:
    def __init__(self):
        self.records = []
        self.next_id = 0

    def raw(self, payload, variant=''):
        self.records.append((variant, payload))

    def send(self, message, variant=''):
        self.raw(json.dumps(message, ensure_ascii=False, separators=(',', ':')), variant)

    def request(self, method, params=None, request_id=None):
        if request_id is None:
            self.next_id += 1
            request_id = self.next_id
        message = {'jsonrpc': '2.0', 'id': request_id, 'method': method}
        if params is not None:
            message['params'] = params
        self.send(message)

    def notify(self, method, params=None):
        message = {'jsonrpc': '2.0', 'method': method}
        if params is not None:
            message['params'] = params
        self.send(message)

    def open(self, uri, text, version=1):
        self.notify('textDocument/didOpen', {'textDocument': {'uri': uri, 'languageId': 'htn', 'version': version,
                                                                'text': text}})

    def change(self, uri, text, version):
        self.notify('textDocument/didChange', {'textDocument': {'uri': uri, 'version': version},
                                               'contentChanges': [{'text': text}]})

    def close(self, uri):
        self.notify('textDocument/didClose', {'textDocument': {'uri': uri}})

    def at(self, method, uri, pos):
        self.request(method, {'textDocument': {'uri': uri}, 'position': pos})

    def compile(self, uri):
        self.request('htn/compile', {'textDocument': {'uri': uri}})

    def text(self):
        return ''.join(f'--- message{" " + variant if variant else ""}\n{payload}\n'
                       for variant, payload in self.records)


def editing_session():
    s = Session()
    main_uri = WORKSPACE_URI + '/main.domain'
    base_uri = WORKSPACE_URI + '/base.domain'
    with open(os.path.join(LSP, 'workspace', 'main.domain')) as f:
        main = f.read()
    with open(os.path.join(LSP, 'workspace', 'base.domain')) as f:
        base = f.read()
    s.request('initialize', {'processId': None, 'rootUri': '$ROOT_URI', 'capabilities': {}})
    s.notify('initialized', {})
    s.open(main_uri, main)
    for needle, delta, occurrence in [('Base::do_combat', 3, 0), ('&work', 1, 0), ('(work)', 2, 0),
                                      ('(work 1)', 1, 0), ('@speed', 2, 0), ('#is_safe', 0, 0),
                                      ('#Base::is_hurt', 8, 0), ('@cover', 6, 0), ('(enemy', 2, 0),
                                      ('!move', 1, 0), ('top_level_method', 0, 0), ('?distance', 1, 0)]:
        s.at('textDocument/definition', main_uri, position(main, needle, delta, occurrence))
    for needle, delta in [('(!move', 0), ('(and (enemy', 0), ('(distance', 0), ('(!work', 0), ('(:axiom', 0),
                          ('?inp_entity) (cover', 3)]:
        s.at('textDocument/completion', main_uri, position(main, needle, delta))
    s.compile(main_uri)
    broken = main.replace('((!rest))', '((!rest ?unknown))')
    s.change(main_uri, broken, 2)
    s.compile(main_uri)
    s.open(base_uri, base.replace('do_combat', 'fight'))
    s.change(main_uri, main, 3)
    s.at('textDocument/definition', main_uri, position(main, 'Base::do_combat', 6))
    s.compile(main_uri)
    s.compile(base_uri)
    s.close(base_uri)
    s.compile(main_uri)
    s.at('textDocument/definition', main_uri, position(main, 'Base::do_combat', 6))
    s.close(main_uri)
    s.at('textDocument/definition', main_uri, position(main, 'Base::do_combat', 6))
    s.at('textDocument/completion', main_uri, position(main, '(!move'))
    s.compile(main_uri)
    s.request('shutdown')
    s.notify('exit')
    return s


def protocol_session():
    s = Session()
    unicode_uri = WORKSPACE_URI + '/unsaved%20unicode.domain'
    unicode_text = ('// émoji 😀 comment\n(:domain Unicode top_level_domain\n'
                    '    (:method (run) top_level_method (b () ((go "😀") (go "x")))) // 中\n'
                    '    (:method (go ?inp_x) (b () ((!say ?inp_x)))))\n')
    s.request('initialize', {'capabilities': {}}, request_id='init')
    s.send({'jsonrpc': '2.0', 'id': None, 'method': 'initialize'})
    s.raw('{"jsonrpc":"2.0","id":1.5,"method":"shutdown-not","params":{}}')
    s.raw('{"jsonrpc":"2.0","id":[1,{"b":2,"a":1}],"method":"unknown/request"}')
    s.notify('unknown/notification', {})
    s.notify('$/setTrace', {'value': 'off'})
    s.request('textDocument/definition')
    s.request('textDocument/completion', {'textDocument': {'uri': unicode_uri}})
    s.request('textDocument/definition', {'textDocument': {'uri': unicode_uri}, 'position': {'line': '1', 'character': 0}})
    s.request('textDocument/definition', {'textDocument': {'uri': unicode_uri}, 'position': {'line': 1.0, 'character': 0}})
    s.request('htn/compile', {'textDocument': {'uri': 7}})
    s.request('htn/compile')
    for payload in ['', '{', 'tru', '[1,]', '{"a" 1}', '{"a":1 "b":2}', '1e400', '"\\q"', '"\\u12G4"', '01x',
                    '{"id":1}garbage', '"unterminated', '-', '1e-400']:
        s.raw(payload)
    s.raw('{"jsonrpc":"2.0"}')
    s.raw('{"jsonrpc":"2.0","method":7,"id":3}')
    s.raw('{"jsonrpc":"2.0","method":"textDocument/hover","method":"initialize","id":"dup"}')
    s.raw('\t{ "jsonrpc" : "2.0" ,\n "id" : -0 , "method" : "initialize" }\x0b')
    s.raw('{"jsonrpc":"2.0","id":"\\u00e9\\ud83d\\ude00\\t\\u0001\\"\\/","method":"nope"}')
    s.raw('{"jsonrpc":"2.0","id":9223372036854775807,"method":"nope"}', 'lowercase')
    s.raw('{"jsonrpc":"2.0","id":3e-7,"method":"nope"}')
    s.open(unicode_uri, unicode_text)
    for needle, delta in [('(go "', 1), ('(go "x', 2), ('(!say', 2), ('?inp_x)', 1)]:
        s.at('textDocument/definition', unicode_uri, position(unicode_text, needle, delta))
    s.at('textDocument/completion', unicode_uri, position(unicode_text, '(!say'))
    s.at('textDocument/definition', unicode_uri, {'line': 2, 'character': 100})
    s.at('textDocument/definition', unicode_uri, {'line': 99, 'character': 0})
    s.at('textDocument/definition', unicode_uri, {'line': -1, 'character': 5})
    s.at('textDocument/definition', unicode_uri, {'line': 4294967298, 'character': 4})
    s.compile(unicode_uri)
    s.notify('textDocument/didChange', {'textDocument': {'uri': unicode_uri, 'version': 'x'},
                                        'contentChanges': [{'text': unicode_text + '(extra'}]})
    s.notify('textDocument/didChange', {'textDocument': {'uri': unicode_uri, 'version': 3}, 'contentChanges': []})
    s.notify('textDocument/didChange', {'textDocument': {'uri': unicode_uri}, 'contentChanges': [{'range': {}}]})
    s.raw('{"jsonrpc":"2.0","method":"textDocument/didChange","params":{"textDocument":{"uri":"%s","version":4},'
          '"contentChanges":[{"text":"// \\u00e9\\ud83d\\ude00\\u0001\\n(:domain Escaped top_level_domain $)"}]}}'
          % unicode_uri)
    s.compile(unicode_uri)
    s.notify('textDocument/didOpen', {'textDocument': {'uri': unicode_uri, 'version': 5}})
    s.notify('textDocument/didOpen', {'textDocument': {'uri': 42, 'text': ''}})
    s.open('untitled:Untitled-1', '(:domain Untitled top_level_domain (:method (run) top_level_method (b () ((!x ?y))))',
           version=-4)
    s.compile('untitled:Untitled-1')
    s.open(WORKSPACE_URI + '/./sub/../main.domain', '(:include "base.domain")\n(:domain Relative top_level_domain)')
    s.compile(WORKSPACE_URI + '/main.domain')
    s.close(WORKSPACE_URI + '/sub/../main.domain')
    s.compile(WORKSPACE_URI + '/main.domain')
    s.notify('exit')
    s.request('initialize', {})
    return s


def frame(session_text, root):
    root_uri = 'file://' + percent_encode_path(root)
    data = bytearray()
    for record in session_text.split('--- message')[1:]:
        header, _, payload = record.partition('\n')
        payload = payload[:-1] if payload.endswith('\n') else payload
        payload = payload.replace('$ROOT_URI', root_uri).replace('$ROOT', root).encode('utf-8')
        if header.strip() == 'lowercase':
            data += b'content-length: %d\r\nContent-Type: application/vscode-jsonrpc; charset=utf-8\r\n\r\n' % len(payload)
        else:
            data += b'Content-Length: %d\r\n\r\n' % len(payload)
        data += payload
    return bytes(data)


def normalize(text, root):
    root_uri = 'file://' + percent_encode_path(root)
    return text.replace(root_uri.encode(), b'$ROOT_URI').replace(root.encode(), b'$ROOT')


def golden(output, stderr, code, root):
    records = bytearray()
    offset = 0
    while offset < len(output):
        match = re.compile(rb'Content-Length: (\d+)\r\n\r\n').match(output, offset)
        if not match:
            raise ValueError('unexpected server output framing')
        length = int(match.group(1))
        payload = output[match.end():match.end() + length]
        records += b'--- message\n' + normalize(payload, root) + b'\n'
        offset = match.end() + length
    return bytes(records) + b'--- stderr\n' + normalize(stderr, root) + b'--- exit %d\n' % code


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--oracle', required=True)
    args = parser.parse_args()
    for name, session in [('editing', editing_session()), ('protocol', protocol_session())]:
        text = session.text()
        with open(os.path.join(LSP, name + '.session'), 'w', newline='') as f:
            f.write(text)
        result = subprocess.run([args.oracle], input=frame(text, ROOT), cwd=ROOT, capture_output=True)
        with open(os.path.join(LSP, name + '.golden'), 'wb') as f:
            f.write(golden(result.stdout, result.stderr, result.returncode, ROOT))


if __name__ == '__main__':
    main()
