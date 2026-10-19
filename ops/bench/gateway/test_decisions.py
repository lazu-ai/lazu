"""Exercise optional Decisions command construction without Docker/network."""
import ast
import json
import pathlib
import unittest
from types import SimpleNamespace


class DecisionCommandTests(unittest.TestCase):
    def test_models_endpoints_and_nonstreaming_are_provider_specific(self):
        tree = ast.parse(pathlib.Path(__file__).with_name('summary.py').read_text())
        fn = next(node for node in tree.body if isinstance(node, ast.FunctionDef) and node.name == 'bench')
        commands = []
        def execute(command, **kwargs):
            commands.append(command)
            return '{}'
        namespace = dict(out=pathlib.Path('/synthetic'), gateway='http://127.0.0.1:10070',
                         upstream='http://127.0.0.1:10062', duration='3s', keys='synthetic',
                         sample_files=[], json=json, subprocess=SimpleNamespace(check_output=execute))
        exec(compile(ast.Module(body=[fn], type_ignores=[]), '<real-bench-command>', 'exec'), namespace)
        for api, target, endpoint, model in (
            ('decisions-openai','gateway','/v1/decisions','gpt-6-luna'),
            ('decisions-openai','direct','/v1/decisions','gpt-6-luna'),
            ('decisions-legacy','gateway','/v1/decisions','jev-1.13'),
            ('decisions-legacy','direct','/v1/systemone','jev-1.13.0'),
        ):
            namespace['bench'](api, 1, 8, target)
            command = commands[-1]
            self.assertEqual(command[command.index('-url')+1], namespace['gateway' if target == 'gateway' else 'upstream']+endpoint)
            self.assertEqual(command[command.index('-model')+1], model)
            self.assertIn('-stream=false', command)
            self.assertEqual('-samples' in command, target == 'gateway')
        namespace['bench']('responses', 1, 1, 'gateway')
        self.assertNotIn('-stream=false', commands[-1])
        self.assertNotIn('-model', commands[-1])


if __name__ == '__main__':
    unittest.main()
