#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Shared checks for shipped service configuration examples, not user files.

The apps keep a copy as scripts/test_service_config.py, like the mutation engine.
Runtime parsers continue accepting existing bare and single-quoted values.
"""
from pathlib import Path
import random
import json
import os
import shlex
import sys
import re
import subprocess
import unittest
import tempfile

ROOT = Path(__file__).resolve().parents[1]
APP = re.search(r'^module\s+(\S+)', (ROOT / 'go.mod').read_text(), re.MULTILINE)[1].split('/')[-1]
APPS = {'imvault', 'witmoot', 'songstead', 'folkweave', 'tidings'}
ASSIGNMENT = re.compile(r'([A-Z][A-Z0-9_]*)=(.*)')
QUOTED = re.compile(r'"(?:[^"\\$`\r\n]|\\["\\$`])*"')


def quote(value):
    return '"' + re.sub(r'(["\\$`])', r'\\\1', value) + '"'


def configuration_errors(text, app, manager):
    path = '/etc/conf.d/' + app if manager == 'OpenRC' else '/etc/' + app + '/' + app + '.env'
    errors = []
    if not text.startswith('# ' + manager + ' configuration: ' + path + '\n'):
        errors.append('configuration must identify its service manager and installed path')
    for number, line in enumerate(text.splitlines(), 1):
        candidate = line.strip()
        if candidate.startswith('#'):
            candidate = candidate[1:].strip()
            if not ASSIGNMENT.match(candidate) and not candidate.startswith('export '):
                continue
        if not candidate:
            continue
        match = ASSIGNMENT.fullmatch(candidate)
        if match is None:
            errors.append(f'line {number}: use a plain KEY="value" assignment without export')
        elif QUOTED.fullmatch(match[2]) is None:
            errors.append(f'line {number}: {match[1]} must use one double-quoted literal value')
    return errors


class ConventionTests(unittest.TestCase):
    def test_strings_numbers_booleans_and_empty_values_share_one_format(self):
        for manager in ('OpenRC', 'systemd'):
            path = '/etc/conf.d/app' if manager == 'OpenRC' else '/etc/app/app.env'
            header = '# ' + manager + ' configuration: ' + path + '\n'
            for value in ('/var/lib/app', '127.0.0.1:8084', '7', 'true', '',
                          'Display Name <mail@example.org>', 'literal $value and `text`', 'a"b\\c'):
                text = header + 'APP_VALUE=' + quote(value) + '\n# APP_EXAMPLE=' + quote(value) + '\n'
                self.assertEqual(configuration_errors(text, 'app', manager), [])

    def test_format_mutations_and_unsafe_input_are_rejected(self):
        header = '# OpenRC configuration: /etc/conf.d/app\n'
        valid = header + 'APP_VALUE="value"\n'
        self.assertEqual(configuration_errors(valid, 'app', 'OpenRC'), [])
        for broken in ('APP_VALUE=value', "APP_VALUE='value'", 'export APP_VALUE="value"',
                       'APP_VALUE="unterminated', 'APP_VALUE="one""two"',
                       'APP_VALUE="$(touch /tmp/never-run)"', 'APP_VALUE="`id`"',
                       'APP_VALUE="${OTHER}"', 'APP_VALUE="literal"; command', 'APP_VALUE="bad\\q"'):
            for prefix in ('', '# '):
                with self.subTest(broken=broken, prefix=prefix):
                    self.assertTrue(configuration_errors(header + prefix + broken + '\n', 'app', 'OpenRC'))
        self.assertTrue(configuration_errors(valid, 'other', 'OpenRC'))
        self.assertTrue(configuration_errors(valid, 'app', 'systemd'))

    def test_errors_do_not_echo_credentials(self):
        secret = 'private-placeholder-value'
        errors = configuration_errors('# OpenRC configuration: /etc/conf.d/app\nAPP_PASSWORD=' + secret, 'app', 'OpenRC')
        self.assertTrue(errors)
        self.assertNotIn(secret, '\n'.join(errors))

    def test_literal_values_match_a_real_shell(self):
        rng = random.Random(20261107)
        alphabet = 'ab 012/#;$`"\\<>()[]!*?=é'
        values = [''.join(rng.choices(alphabet, k=rng.randrange(40))) for _ in range(100)]
        lines = [f'APP_{index}={quote(value)}' for index, value in enumerate(values)]
        script = '\n'.join(lines) + '\n' + '\n'.join(f'printf "%s\\0" "$APP_{index}"' for index in range(len(values)))
        result = subprocess.run(['/bin/sh', '-c', script], env={'PATH': ''}, capture_output=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr.decode())
        self.assertEqual(result.stdout.decode().split('\0')[:-1], values)

    @unittest.skipUnless(APP in APPS, 'the shared library has no app configuration')
    def test_shipped_configuration(self):
        directory = 'deploy' if APP in {'folkweave', 'tidings'} else 'contrib'
        conf = APP + ('.conf' if directory == 'deploy' else '.confd')
        for manager, path in [('OpenRC', ROOT/directory/'openrc'/conf),
                              ('systemd', ROOT/directory/'systemd'/(APP+'.env'))]:
            with self.subTest(path=path):
                self.assertEqual(configuration_errors(path.read_text(), APP, manager), [])

    @unittest.skipUnless(APP in APPS, 'the shared library has no release packages')
    def test_generated_package_configuration(self):
        prepare = ROOT/'scripts/release/prepare.sh'
        with tempfile.TemporaryDirectory() as out:
            result = subprocess.run(['/bin/sh', str(prepare)], cwd=ROOT,
                                    env={**os.environ, 'RELEASE_DIR': out},
                                    capture_output=True, text=True, timeout=120)
            self.assertEqual(result.returncode, 0, result.stderr)
            generated = Path(out)/(APP+'.env')
            # Songstead packages its systemd template directly.
            path = generated if generated.exists() else ROOT/'contrib/systemd'/(APP+'.env')
            text = path.read_text()
            self.assertEqual(configuration_errors(text, APP, 'systemd'), [])
            keys = [match[1] for line in text.splitlines()
                    if (match := ASSIGNMENT.fullmatch(line))]
            self.assertEqual(len(keys), len(set(keys)), 'duplicate packaged defaults')

    @unittest.skipUnless(APP in APPS, 'the shared library has no OpenRC service')
    def test_plain_assignments_reach_the_service_process(self):
        directory = 'deploy' if APP in {'folkweave', 'tidings'} else 'contrib'
        conf = APP + ('.conf' if directory == 'deploy' else '.confd')
        path = ROOT/directory/'openrc'/conf
        assignments = []
        keys = []
        controls = {APP.upper() + suffix for suffix in ('_USER', '_GROUP', '_BIN', '_LOG_FILE', '_SANDBOX')}
        for line in path.read_text().splitlines():
            candidate = line.removeprefix('# ').strip()
            match = ASSIGNMENT.fullmatch(candidate)
            if match:
                assignments.append(candidate)
                if match[1] not in controls:
                    keys.append(match[1])
        self.assertTrue(keys)
        probe = 'import json, os; print(json.dumps({k:os.environ.get(k) for k in ' + repr(keys) + '}))'
        script = '\n'.join(assignments) + '\n' + shlex.join([sys.executable, '-c', probe])
        # A baseline exported fixture gives the values a real shell assigns.
        baseline = script.replace('\n' + shlex.quote(sys.executable), '\nexport ' + ' '.join(keys) + '\n' + shlex.quote(sys.executable), 1)
        child = '\n'.join(assignments) + '\n. ' + shlex.quote(str(ROOT/directory/'openrc'/APP)) + '\n' + shlex.join([sys.executable, '-c', probe])
        env = {key:value for key,value in os.environ.items() if not key.startswith(APP.upper()+'_') and key != 'SSL_CERT_FILE'}
        expected = subprocess.run(['/bin/sh', '-c', baseline], env=env, capture_output=True, text=True, timeout=10)
        actual = subprocess.run(['/bin/sh', '-c', child], env=env, capture_output=True, text=True, timeout=10)
        self.assertEqual(expected.returncode, 0, expected.stderr)
        self.assertEqual(actual.returncode, 0, actual.stderr)
        self.assertEqual(json.loads(actual.stdout), json.loads(expected.stdout))


if __name__ == '__main__':
    unittest.main(verbosity=2)
