#!/usr/bin/env python3
"""Differential fuzzing of the domain frontend (lexer, parser, include
splitting, linking, validation, IR lowering and code-generation checks).

Random token-level mutations are applied to the repository's domain files and
each mutated domain is passed to the original HTNTranslator and to one or more
port translators, in --check mode and in full translation mode. The exit code
and the complete diagnostic output must be identical.

  python3 tools/fuzz/frontend_fuzz.py --oracle build/oracle/HTNTranslator \
      --port go=path/to/htn-translator --iterations 2000 --seed 1
"""
import argparse
import os
import random
import re
import shutil
import subprocess
import sys
import tempfile

TOKEN = re.compile(r'//[^\n]*|"(?:[^"\\\n])*"?|[()]|\s+|[^\s()"]+')
NOISE = ['(', ')', '"', '$', '?x', '@', '#', '!', '&', ':', '0', '1.5', '-', '+', '*', '/', '%',
         '(and', '(or', '(not', '(alt', '(call', '(=', '(==', '(<', '(!x)', '?', '"s"', ';', '99999999999999',
         ':method', ':axiom', ':constants', ':domain', ':include', 'top_level_method', 'top_level_domain',
         '(split_list', 'front', 'back', '(>', '(>=', '(!=', '::', 'base', '@undefined', '#undefined']


def mutate(text, rng):
    tokens = TOKEN.findall(text)
    words = [t for t in tokens if t.strip() and t not in '()' and not t.startswith('//')]
    for _ in range(rng.randint(1, 3)):
        if not tokens:
            break
        i = rng.randrange(len(tokens))
        op = rng.randrange(8)
        if op == 0:
            del tokens[i]
        elif op == 1:
            tokens.insert(i, tokens[i])
        elif op == 2 and i + 1 < len(tokens):
            tokens[i], tokens[i + 1] = tokens[i + 1], tokens[i]
        elif op == 3 and words:
            tokens[i] = rng.choice(words)
        elif op == 4:
            tokens.insert(i, ' ' + rng.choice(NOISE) + ' ')
        elif op == 5:
            parens = [k for k, t in enumerate(tokens) if t in '()']
            if parens:
                del tokens[rng.choice(parens)]
        elif op == 6:
            numbers = [k for k, t in enumerate(tokens) if re.fullmatch(r'\d+(\.\d+)?', t)]
            if numbers:
                tokens[rng.choice(numbers)] = rng.choice(['0', '-1', '2147483648', '3.4e39', '1.0', 'x'])
        else:
            variables = [k for k, t in enumerate(tokens) if t.startswith('?')]
            if variables:
                k = rng.choice(variables)
                tokens[k] = rng.choice(['?inp_x', '?out_y', '?io_z', '?any_w', '?', tokens[k] + '2'])
    return ''.join(tokens)


def run(command, cwd):
    result = subprocess.run(command, cwd=cwd, capture_output=True, text=True, timeout=60)
    return result.returncode, result.stdout + '\x00' + result.stderr


def normalize(output, workdir):
    output = output.replace('HTNTranslator', 'htn-translator').replace(workdir, '<work>')
    output = re.sub(r'_generated\.nim\b', '.generated.<ext>', output)
    output = re.sub(r'\.generated\.(c|go)\b', '.generated.<ext>', output)
    return output


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--oracle', required=True)
    parser.add_argument('--port', action='append', required=True, help='name=path of a port translator')
    parser.add_argument('--iterations', type=int, default=500)
    parser.add_argument('--seed', type=int, default=1)
    parser.add_argument('--keep', default=None, help='directory receiving mismatching domains')
    parser.add_argument('--full-every', type=int, default=4, help='run a full translation every N iterations')
    args = parser.parse_args()
    root = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
    ports = [p.split('=', 1) for p in args.port]
    rng = random.Random(args.seed)
    workdir = tempfile.mkdtemp(prefix='htn-frontend-fuzz-')
    shutil.copytree(os.path.join(root, 'Domains'), os.path.join(workdir, 'Domains'))
    shutil.copytree(os.path.join(root, 'HTNDiagnosticTests'), os.path.join(workdir, 'HTNDiagnosticTests'))
    files = []
    for base in ('Domains', 'HTNDiagnosticTests'):
        for dirpath, _, names in os.walk(os.path.join(workdir, base)):
            files += [os.path.relpath(os.path.join(dirpath, n), workdir) for n in names if n.endswith('.domain')]
    files.sort()
    roots = [f for f in files if '/Includes/' not in f]
    mismatches = 0
    for iteration in range(args.iterations):
        target = rng.choice(files)
        path = os.path.join(workdir, target)
        original = open(path, encoding='utf-8', errors='surrogateescape').read()
        mutated = mutate(original, rng)
        with open(path, 'w', encoding='utf-8', errors='surrogateescape') as f:
            f.write(mutated)
        domain = target if target in roots else rng.choice(roots)
        commands = [['--check', domain]]
        if iteration % args.full_every == 0:
            commands.append([domain, 'CreateFuzzHTN', 'out', '--runtime-backtracking-support=enabled'])
        for command in commands:
            expected = run([args.oracle] + command, workdir)
            expected = (expected[0], normalize(expected[1], workdir))
            for name, translator in ports:
                actual = run([translator] + command, workdir)
                actual = (actual[0], normalize(actual[1], workdir))
                if actual != expected:
                    mismatches += 1
                    print(f'MISMATCH [{name}] iteration {iteration}: {" ".join(command)} (mutated {target})')
                    print('--- oracle', expected[0]); print(expected[1][:2000])
                    print('--- port', actual[0]); print(actual[1][:2000])
                    if args.keep:
                        os.makedirs(args.keep, exist_ok=True)
                        with open(os.path.join(args.keep, f'mismatch_{args.seed}_{iteration}.domain'), 'w',
                                  encoding='utf-8', errors='surrogateescape') as f:
                            f.write(mutated)
        with open(path, 'w', encoding='utf-8', errors='surrogateescape') as f:
            f.write(original)
    shutil.rmtree(workdir)
    print(f'{args.iterations} iterations, {mismatches} mismatches')
    sys.exit(1 if mismatches else 0)


if __name__ == '__main__':
    main()
