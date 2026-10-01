#!/usr/bin/env python3
"""Writes the translator golden corpus (testdata/translator).

The case domains below come from the original frontend/compiler tests. Every
line of testdata/translator/cases.txt is one htn-translator command line (run
from the repository root, $OUT is a scratch output directory). The expected
exit code, stdout and stderr are recorded from the ORIGINAL HTNTranslator:

  python3 tools/translator/make_goldens.py --oracle build/oracle/HTNTranslator
"""
import argparse
import os
import re
import subprocess
import tempfile

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
CASES = os.path.join(ROOT, 'testdata', 'translator')

VALID = "(:domain Root top_level_domain (:method (run) top_level_method (ready () ((!act)))))"
LEX_PREFIX = "(:domain Root top_level_domain\r\n  (:method (run) top_level_method (ready () ((!act "

SOURCES = {
    # Malformed sources (HTNFrontendDiagnosticsTest).
    'empty': "",
    'comment_only': "// no domain",
    'unclosed_domain': "(:domain Root top_level_domain",
    'broken_method': "(:domain Root top_level_domain (:method (run) top_level_method (broken)))",
    'unknown_declaration': "(:domain Root top_level_domain (:unknown))",
    'extra_form': VALID + "\n(extra)",
    'comment_eof_1': VALID + "//",
    'comment_eof_2': VALID + "// comment without newline",
    'comment_eof_3': VALID + "// comment\n",
    'comment_eof_4': VALID + "// comment\r\n",
    'lex_unrecognized': LEX_PREFIX + "$))))",
    'lex_unclosed_string': LEX_PREFIX + "\"unclosed",
    'lex_int_bounds': LEX_PREFIX + "999999999999999999999999))))",
    'lex_float_bounds': LEX_PREFIX + "999999999999999999999999999999999999999999999999.0))))",
    'lex_float_underflow': LEX_PREFIX + "0.0000000000000000000000000000000000000000000007))))",
    'lex_float_underflow_zero': LEX_PREFIX + "0.000000000000000000000000000000000000000000000000001))))",
    'lex_float_denormal': LEX_PREFIX + "0.0000000000000000000000000000000000000000000014))))",
    'lex_float_zero': LEX_PREFIX + "0.000))))",
    'lex_int_max': LEX_PREFIX + "2147483647))))",
    'lex_int_overflow': LEX_PREFIX + "2147483648))))",
    'include_bad_path': "// header\r\n  (:include bad)",
    'include_unterminated': "// header\r\n  (:include \"unterminated",
    'include_empty_path': "// header\r\n  (:include \"\")",
    'include_bad_end': "// header\r\n  (:include \"Base.domain\" bad)",
    'include_comments': "// (:include ignored)\r\n(:include // path follows\r\n \"Base.domain\" // closing follows\r\n)\r\n"
                        "(:domain Root top_level_domain (:method (run) top_level_method (ready () ((!act \"(:include\")))))"
                        "// (:include also ignored)",
    'include_misplaced': VALID + "\r\n  (:include \"Base.domain\")",
    'include_cycle_root': "// header\r\n  (:include \"include_cycle_child.domain\")\r\n" + VALID,
    'include_cycle_child': "// child\r\n (:include \"include_cycle_root.domain\")\r\n(:domain Base base)",
    'include_missing_root': "// header\r\n  (:include \"include_missing_child.domain\")\r\n" + VALID,
    'include_missing_child': "// child\r\n (:include \"__htn_missing_diagnostic_fixture__.domain\")\r\n(:domain Base base)",
    'include_malformed_root': "// header\r\n  (:include \"include_malformed_child.domain\")\r\n" + VALID,
    'include_malformed_child': "// child\r\n(:domain Base base $)",
    'Base': "(:domain Base base)// no final newline",
    # Compiler validation (HTNDecompositionTest).
    'invalid_parameter': "(:domain InvalidParameter top_level_domain\n  (:method (run) top_level_method (start () ((child 1))))\n"
                         "  (:method (child ?value) (body () ((!act ?value))))\n)\n",
    'invalid_axiom_parameter': "(:domain InvalidAxiom top_level_domain\n  (:axiom (bad ?value) ())\n"
                               "  (:method (run) top_level_method (start () ((!act))))\n)\n",
    'declarations_any_order': "(:domain UnorderedDeclarations top_level_domain\n"
                              "  (:method (run) top_level_method (ready (and (#is_ready 7)) ((!act))))\n"
                              "  (:axiom (is_ready ?inp_value) (and (ready_value ?inp_value)))\n"
                              "  (:constants (expected 7))\n)\n",
    'lexer_error_location': "(:domain BrokenLexer top_level_domain\n"
                            "  (:method (run) top_level_method (ready () ((!act $value))))\n)\n",
    'invalid_method_signature': "(:domain InvalidCompilerSignature top_level_domain\n"
                                "  (:method (run ?out_target) top_level_method (ready () ((!act ?out_target))))\n)\n",
    'independent_syntax_errors': "(:domain BrokenCompiler top_level_domain\n  (:axiom (broken_axiom) (unknown_condition))\n"
                                 "  (:method (run) top_level_method (broken_branch))\n)\n",
    'incomplete_1': "(:domain Broken top_level_domain (:method (run) top_level_method (branch (and) ((!act))",
    'incomplete_2': "(:domain Broken top_level_domain (:method (run) top_level_method (branch (and) ((!act ?)))) )",
    'incomplete_3': "(:domain Broken top_level_domain (:method (run) top_level_method (branch (and (call)) ((!act)))))",
    'any_singleton': "(:domain AnyPrefix top_level_domain\n  (:method (run) top_level_method\n"
                     "    (branch_main () ((!capture ?any_threat)))\n  )\n)\n",
    'any_company': "(:domain AnyPrefix top_level_domain\n  (:method (run) top_level_method\n"
                   "    (branch_main () ((!capture ?company_value)))\n  )\n)\n",
    'any_bare': "(:domain AnyPrefix top_level_domain\n  (:method (run) top_level_method\n"
                "    (branch_main () ((!capture ?any)))\n  )\n)\n",
    'any_distinct': "\n(:domain AnySingletonGenerated top_level_domain\n    (:method (run) top_level_method\n"
                    "        (branch_main\n            (and\n                (threat ?any_threat_0)\n"
                    "                (visible ?any_threat_1))\n            ((!capture))\n        )\n    )\n)\n",
    'overload_duplicate': "(:domain Bad top_level_domain (:method (run) top_level_method) (:method (run)))",
    'overload_arity': "(:domain Bad top_level_domain (:method (run) top_level_method (b () ((work 1 2)))) (:method (work)) (:method (work ?inp_x)))",
    'overload_qualified': "(:domain Bad top_level_domain (:method (run) top_level_method (b () ((Bad::work 1)))) (:method (work)))",
    'overload_deferred': "(:domain Bad top_level_domain (:method (run) top_level_method (b () ((&work 1)))) (:method (work)))",
    'axiom_duplicate': "(:domain Bad top_level_domain (:method (run) top_level_method) (:axiom (a ?inp_x) ()) (:axiom (a ?out_y) ()))",
    'axiom_arity': "(:domain Bad top_level_domain (:method (run) top_level_method) (:method (work) (b (and (#a 1 2)) ())) (:axiom (a) ()) (:axiom (a ?inp_x) ()))",
    'axiom_qualified': "(:domain Bad top_level_domain (:method (run) top_level_method) (:axiom (a) (and (#Bad::a 1))))",
    'axiom_missing': "(:domain Bad top_level_domain (:method (run) top_level_method) (:axiom (a) (and (not (#missing 1)))))",
    'axiom_cycle': "(:domain Bad top_level_domain (:method (run) top_level_method) (:axiom (a) (and (#a 1))) (:axiom (a ?inp_x) (and (#a))))",
    'axiom_overloads_good': "(:domain Good top_level_domain (:method (run) top_level_method (b (and (#a) (#a 1)) ()))"
                            " (:axiom (a) (and (#a 1))) (:axiom (a ?inp_x) (and (== ?inp_x 1))))",
    'parser_unclosed': "(:domain Broken",
    'parser_incomplete': "(:domain Broken (:method))",
    'parser_axiom_task': "(:domain Broken (:method (run) (branch () ((#later)))))",
    'parser_arith_arity': "(:domain Broken (:method (run) (branch () ((!done (++ 1 2))))))",
    'parser_valid_minimal': "(:domain Valid)",
    'arith_in_fact': "(:domain Arith top_level_domain (:method (run) top_level_method (b (and (value (+ 1 2))) ((!act)))))",
    'arith_in_call_bind': "(:domain Arith top_level_domain (:method (run) top_level_method (b (and (= ?x (+ 1 2.5))) ((!act ?x)))))",
    'arith_modulo_float': "(:domain Arith top_level_domain (:method (run) top_level_method (b () ((!act (% 5 2.0))))))",
    'call_unbound_output': "(:domain Calls top_level_domain (:method (run) top_level_method (b (and (= ?x (call f ?y))) ((!act ?x)))))",
}

COMMANDS = []


def add(*args):
    COMMANDS.append(' '.join(args))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--oracle', required=True)
    args = parser.parse_args()
    os.makedirs(os.path.join(CASES, 'cases'), exist_ok=True)
    for name, text in SOURCES.items():
        with open(os.path.join(CASES, 'cases', name + '.domain'), 'w', newline='') as f:
            f.write(text)
    for name in sorted(SOURCES):
        if name == 'Base' or name.endswith('_child'):
            continue
        add('--check', f'testdata/translator/cases/{name}.domain')
    for name in ['declarations_any_order', 'any_distinct', 'axiom_overloads_good', 'arith_in_fact', 'arith_in_call_bind',
                 'arith_modulo_float', 'call_unbound_output', 'parser_valid_minimal', 'include_comments']:
        add(f'testdata/translator/cases/{name}.domain', 'CreateCaseHTN', '$OUT', '--runtime-backtracking-support=enabled')
    for dirpath, _, names in sorted(os.walk(os.path.join(ROOT, 'Domains'))):
        for n in sorted(names):
            if n.endswith('.domain'):
                add('--check', os.path.relpath(os.path.join(dirpath, n), ROOT))
    for n in sorted(os.listdir(os.path.join(ROOT, 'HTNDiagnosticTests'))):
        if n.endswith('.domain'):
            add('--check', f'HTNDiagnosticTests/{n}')
    add('--check', 'HTNDiagnosticTests/Includes/DiagnosticBrokenInclude.domain')
    add('--check', 'testdata/translator/cases/does_not_exist.domain')
    add()
    add('Domains/Test/human.domain')
    add('Domains/Test/human.domain', 'CreateHumanHTN', '$OUT', '--backtracking-policy=bogus')
    add('Domains/Test/human.domain', 'CreateHumanHTN', '$OUT', '--backtracking-capacity=0')
    add('Domains/Test/human.domain', 'CreateHumanHTN', '$OUT', '--call-frame-capacity=x')
    add('Domains/Test/human.domain', 'CreateHumanHTN', '$OUT', '--runtime-backtracking-support=maybe')
    add('Domains/Test/human.domain', 'CreateHumanHTN', '$OUT', '--frobnicate')
    add('Domains/Test/human.domain', 'CreateHumanHTN', '$OUT', '$OUT')
    add('Domains/Test/human.domain', 'CreateHumanHTN', '$OUT', '--backtracking-policy=fixed-capacity', '--backtracking-capacity=7',
        '--call-frame-capacity=99')
    add('HTNDiagnosticTests/DiagnosticSemanticErrors.domain', 'CreateBadHTN', '$OUT')
    with open(os.path.join(CASES, 'cases.txt'), 'w') as f:
        f.write('# htn-translator command lines; see tools/translator/make_goldens.py\n')
        for command in COMMANDS:
            f.write(command + '\n')
    golden = []
    out = tempfile.mkdtemp(prefix='htn-translator-golden-')
    for command in COMMANDS:
        argv = [a.replace('$OUT', out) for a in command.split()]
        result = subprocess.run([args.oracle] + argv, cwd=ROOT, capture_output=True, text=True)
        def norm(text):
            text = text.replace(out, '$OUT').replace(ROOT, '$ROOT').replace('HTNTranslator', 'htn-translator')
            return re.sub(r'\.generated\.c\b', '.generated.<ext>', text)
        golden.append(f'case {command}\nexit {result.returncode}\n--- stdout\n{norm(result.stdout)}--- stderr\n{norm(result.stderr)}--- end\n')
    with open(os.path.join(CASES, 'cases.golden'), 'w') as f:
        f.write(''.join(golden))


if __name__ == '__main__':
    main()
